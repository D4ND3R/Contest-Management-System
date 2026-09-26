package suspicious

import (
	"strings"
	"testing"
)

func reasons(fs []Flag) string {
	var r []string
	for _, f := range fs {
		r = append(r, f.Reason)
	}
	return strings.Join(r, ",")
}

func TestScan(t *testing.T) {
	cases := []struct {
		lang, name, src string
		want            []string
	}{
		// Ordinary solutions: nothing.
		{"cpp17", "sol.%l", "#include <bits/stdc++.h>\nusing namespace std;\nint main(){ auto f = bind(plus<int>(), 1, 2); system_clock::now(); cout << f(); }\n", nil},
		{"python3", "sol.%l", "import sys, os\nfrom collections import deque\nprint(sum(map(int, sys.stdin.read().split())))\n", nil},
		{"java", "Main.%l", "import java.util.*;\npublic class Main { public static void main(String[] a) { new Scanner(System.in); } }\n", nil},
		// Attacks.
		{"cpp17", "sol.%l", "#include <unistd.h>\nint main(){ fork(); }\n", []string{Process}},
		{"c11", "sol.%l", "int main(){ system(\"rm -rf /\"); }\n", []string{Process}},
		{"c11", "sol.%l", "int main(){ __asm__ volatile(\"syscall\"); }\n", []string{Syscall}},
		{"c11", "sol.%l", "#include <sys/syscall.h>\nint main(){ syscall(425, 8, 0); }\n", []string{Syscall}},
		{"cpp20", "sol.%l", "#include <sys/socket.h>\nint main(){ socket(2,1,0); }\n", []string{Network}},
		{"c11", "sol.%l", "#include </etc/shadow>\n", []string{Include}},
		{"c11", "sol.%l", "#include \"../../../testcases/1.out\"\n", []string{Include}},
		{"c11", "sol.%l", "FILE*f=fopen(\"/proc/self/maps\",\"r\");\n", []string{SystemFile}},
		{"c11", "sol.%l", "#include <sys/ptrace.h>\n", []string{Debug}},
		{"c11", "sol.%l", "mprotect(p, n, PROT_READ|PROT_EXEC);\n", []string{Native}},
		{"python3", "sol.%l", "import subprocess\nsubprocess.run(['ls'])\n", []string{Process}},
		{"pypy3", "sol.%l", "import os\nos.system('id')\n", []string{Process}},
		{"python3", "sol.%l", "import socket\n", []string{Network}},
		{"python3", "sol.%l", "import ctypes\nopen('/etc/passwd')\n", []string{Native, SystemFile}},
		{"java", "Main.%l", "class Main { void f() throws Exception { Runtime.getRuntime().exec(\"id\"); new java.net.Socket(\"h\", 1); } }\n", []string{Process, Network}},
		{"kotlin", "Main.%l", "fun main() { ProcessBuilder(\"id\").start() }\n", []string{Process}},
		{"rust", "main.%l", "use std::process::Command;\nfn main(){ Command::new(\"id\"); }\n", []string{Process}},
		{"go", "main.%l", "import (\n\t\"os/exec\"\n\t\"net\"\n)\n", []string{Process, Network}},
		{"csharp", "p.%l", "using System.Diagnostics; class P { static void Main(){ Process.Start(\"id\"); } }\n", []string{Process}},
		{"haskell", "m.%l", "import System.Process\n", []string{Process}},
		{"pascal", "p.%l", "uses process;\nbegin end.\n", []string{Process}},
		// Unknown language: only binary data is looked at.
		{"", "notes.txt", "system(\"id\")", nil},
		{"c11", "sol.%l", "int main(){}\x00\x01", []string{Binary}},
	}
	for _, c := range cases {
		got := Scan(c.lang, map[string][]byte{c.name: []byte(c.src)})
		if reasons(got) != strings.Join(c.want, ",") {
			t.Errorf("%s %q: got %q, want %v", c.lang, c.src, reasons(got), c.want)
		}
	}
	// One flag per reason, pointing at the first place.
	got := Scan("c11", map[string][]byte{"sol.%l": []byte("int main(){\n fork();\n fork();\n}\n")})
	if len(got) != 1 || got[0].Detail != "sol.%l:2: fork();" {
		t.Fatalf("detail: %+v", got)
	}
	if long := clip(strings.Repeat("x", 300)); len([]rune(long)) != 118 {
		t.Fatalf("clip: %d", len([]rune(long)))
	}
}
