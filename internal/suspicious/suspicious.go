// Package suspicious looks at submitted sources for code that attacks the
// judge rather than solves a task: starting programs, opening sockets,
// raw system calls, reading system files, loading native code, binary
// data. The sandbox stops all of it anyway (isolate and the seccomp
// filter); the flags tell the staff which submissions deserve a look.
// They never change a score and false positives are harmless.
package suspicious

import (
	"bytes"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Reasons (stored in English, translated where they are shown).
const (
	Syscall    = "raw system calls"
	Process    = "starts other programs"
	Network    = "uses the network"
	SystemFile = "reads system files"
	Include    = "includes a file from outside the submission"
	Debug      = "inspects other processes"
	Native     = "loads or generates native code"
	Binary     = "binary data in a source file"
	// Forbidden is what the sandbox reports (a runtime flag).
	Forbidden = "forbidden system call"
)

// Flag is one finding.
type Flag struct {
	Reason string
	// Detail says where: "sol.cpp:12: system(cmd)".
	Detail string
}

type rule struct {
	reason string
	re     *regexp.Regexp
}

func rules(reason string, exprs ...string) []rule {
	out := make([]rule, len(exprs))
	for i, e := range exprs {
		out[i] = rule{reason, regexp.MustCompile(e)}
	}
	return out
}

func join(groups ...[]rule) []rule {
	var out []rule
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

// Paths of the host a solution has no reason to name.
var systemPaths = rules(SystemFile, `["'](/proc|/sys|/etc|/root|/home|/var|/boot|/run)/`)

var byFamily = map[string][]rule{
	"c": join(
		rules(Syscall, `\bsyscall\s*\(`, `\b(asm|__asm__|__asm)\b[^\n]*\b(syscall|sysenter|int\s*\$?0x80|svc\s+#?0)\b`),
		rules(Process, `\b(fork|vfork|execl|execlp|execle|execv|execvp|execvpe|execve|fexecve|system|popen|posix_spawnp?|clone|clone3|unshare|setns)\s*\(`),
		rules(Network, `\bsocket\s*\(`, `#\s*include\s*<(sys/socket\.h|netinet/|arpa/inet\.h|netdb\.h|sys/un\.h)`),
		rules(Debug, `\bptrace\s*\(`, `#\s*include\s*<sys/ptrace\.h>`, `\bprocess_vm_(readv|writev)\s*\(`),
		rules(Include, `#\s*include\s*[<"](/|[^>"]*\.\./)`),
		rules(Native, `\bdlopen\s*\(`, `\bPROT_EXEC\b`),
		systemPaths),
	"python": join(
		rules(Process, `\bos\.(system|popen|fork|forkpty|exec\w*|spawn\w*|posix_spawn\w*)\s*\(`, `\bimport\s+(subprocess|pty|multiprocessing)\b`,
			`\bfrom\s+(subprocess|pty|multiprocessing)\s+import\b`),
		rules(Network, `\bimport\s+(socket|urllib|http|requests|ftplib|smtplib)\b`, `\bfrom\s+(socket|urllib|http|requests)\b[\w.]*\s+import\b`),
		rules(Native, `\bimport\s+(ctypes|cffi|mmap)\b`, `\bfrom\s+(ctypes|cffi)\b`),
		systemPaths),
	"java": join(
		rules(Process, `Runtime\s*\.\s*getRuntime\s*\(\s*\)\s*\.\s*exec`, `\bProcessBuilder\b`),
		rules(Network, `\bjava\.net\.`, `\b(Socket|ServerSocket|DatagramSocket|HttpURLConnection)\s*\(`),
		rules(Native, `\bSystem\s*\.\s*load(Library)?\s*\(`, `\bsun\.misc\.Unsafe\b`, `\bjdk\.internal\.`),
		systemPaths),
	"rust": join(
		rules(Process, `\bprocess::Command\b`, `\bCommand::new\s*\(`),
		rules(Network, `\bstd::net\b`, `\b(TcpStream|TcpListener|UdpSocket)\b`),
		rules(Syscall, `\basm!\s*\(`, `\blibc::syscall\b`),
		systemPaths),
	"go": join(
		rules(Process, `"os/exec"`, `\bsyscall\.(ForkExec|Exec|StartProcess)\b`, `\bos\.StartProcess\b`),
		rules(Network, `"net"`, `"net/http"`, `"net/rpc"`),
		rules(Syscall, `\b(syscall|unix)\.(Syscall6?|RawSyscall6?)\b`),
		systemPaths),
	"csharp": join(
		rules(Process, `\bSystem\.Diagnostics\.Process\b`, `\bProcess\.Start\s*\(`),
		rules(Network, `\bSystem\.Net\b`),
		rules(Native, `\bDllImport\b`),
		systemPaths),
	"haskell": join(
		rules(Process, `\bimport\s+(qualified\s+)?System\.(Process|Posix\.Process)\b`),
		rules(Network, `\bimport\s+(qualified\s+)?Network\.`),
		rules(Native, `\bforeign\s+import\b`),
		systemPaths),
	"pascal": join(
		rules(Process, `(?i)\b(fpfork|fpexecv\w*|fpsystem|executeprocess)\s*\(`, `(?i)\buses\b[^;]*\bprocess\b`),
		rules(Network, `(?i)\buses\b[^;]*\b(sockets|ssockets)\b`),
		systemPaths),
}

var families = map[string]string{
	".c": "c", ".h": "c", ".cc": "c", ".cpp": "c", ".cxx": "c", ".hpp": "c", ".hh": "c",
	".py": "python", ".java": "java", ".kt": "java", ".kts": "java", ".rs": "rust", ".go": "go",
	".cs": "csharp", ".hs": "haskell", ".pas": "pascal", ".pp": "pascal", ".dpr": "pascal",
}

// langFamilies map language ids (cpp17, pypy3, csharp...) to rules, by
// prefix, in order ("csharp" before "c").
var langFamilies = []struct{ prefix, family string }{
	{"cpp", "c"}, {"csharp", "csharp"}, {"c", "c"}, {"python", "python"}, {"pypy", "python"}, {"java", "java"},
	{"kotlin", "java"}, {"rust", "rust"}, {"go", "go"}, {"haskell", "haskell"}, {"pascal", "pascal"},
}

// family picks the rules of a language, else of a file name's extension.
func family(lang, name string) string {
	id := strings.ToLower(lang)
	for _, f := range langFamilies {
		if id != "" && strings.HasPrefix(id, f.prefix) {
			return f.family
		}
	}
	return families[strings.ToLower(path.Ext(name))]
}

// Scan inspects the files of a submission in language lang (name →
// content) and returns its findings, one per reason (the first place it
// was seen).
func Scan(lang string, files map[string][]byte) []Flag {
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	seen := map[string]bool{}
	var out []Flag
	add := func(reason, detail string) {
		if !seen[reason] {
			seen[reason] = true
			out = append(out, Flag{reason, detail})
		}
	}
	for _, name := range names {
		data := files[name]
		if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
			add(Binary, name)
			continue
		}
		rs := byFamily[family(lang, name)]
		if rs == nil {
			continue
		}
		for i, line := range strings.Split(string(data), "\n") {
			for _, r := range rs {
				if !seen[r.reason] && r.re.MatchString(line) {
					add(r.reason, name+":"+strconv.Itoa(i+1)+": "+clip(strings.TrimSpace(line)))
				}
			}
		}
	}
	return out
}

// clip shortens a source line for display.
func clip(s string) string {
	if utf8.RuneCountInString(s) <= 120 {
		return s
	}
	return string([]rune(s)[:117]) + "…"
}
