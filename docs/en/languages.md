# Programming languages

Each language is one YAML file in `config/languages/`. Adding a language
means adding a file and restarting the services: no code changes. Workers
receive the definition inside every job, so all workers always use the same
commands.

## Supported out of the box

| id | Language | Toolchain used in testing |
|----|----------|---------------------------|
| `c11` | C11 | gcc 13.3 (`-std=gnu11 -O2 -static`) |
| `cpp17` | C++17 | g++ 13.3 (`-std=gnu++17 -O2 -static`) |
| `cpp20` | C++20 | g++ 13.3 (`-std=gnu++20 -O2 -static`) |
| `java` | Java | OpenJDK 21 (`-Xmx` = memory limit, SerialGC) |
| `python3` | Python 3 | CPython 3.11 |
| `pypy3` | Python 3 | PyPy 7.3 (Python 3.9) |
| `pascal` | Pascal | Free Pascal 3.2.2 |
| `rust` | Rust | rustc (edition 2021, `-O`) |
| `go` | Go | Go 1.27 (`GOMAXPROCS=1`, warmed build cache) |
| `kotlin` | Kotlin | kotlinc (JVM), OpenJDK 21 |
| `csharp` | C# | Mono 6.8 (`mcs -optimize+`) |
| `haskell` | Haskell | GHC 9.4 (`-O2`) |

Versions are those of the worker's operating system packages; pin them by
pinning the worker image or packages. The exact version is shown by each
file's `version_command`.

## File format

```yaml
id: cpp17                      # unique identifier stored with submissions
name: "C++17 / g++"            # shown to contestants
version_command: ["g++", "--version"]
source_extensions: [".cpp", ".cc"]   # the first one replaces %l in file names
header_extensions: [".h", ".hpp"]    # grader headers copied next to sources
compile:                       # commands run in order inside the sandbox
  - ["/usr/bin/g++", "-O2", "-o", "{executable}", "{sources}"]
executable: "{main}"           # main artifact kept after compilation
keep_files: ["*.class"]        # optional extra artifacts (globs)
run: ["./{executable}"]
compile_limits: {time: 10s, memory: 1GiB, processes: 32, output: 256MiB}
run_processes: 1               # minimum processes/threads at run time
env: {KEY: value}              # added to PATH, HOME=/tmp, LANG=C.UTF-8
dirs: ["/etc/java-*"]          # extra read-only host dirs (globs allowed)
no_address_space_limit: false  # true for managed runtimes (JVM, Go, .NET)
compile_seed:                  # optional: warmed compiler cache (see Go)
  dir: ".gocache"
  warmup_file: "warmup.go"
  warmup: "package main ..."
```

Placeholders: `{sources}` (all sources, grader first), `{main_source}`
(first source), `{main}` (its basename), `{executable}`, `{memory_mb}` and
`{memory_kb}` (run commands). Commands must use absolute paths (use
`/usr/bin/env tool` for a PATH lookup inside the sandbox).

## Rules applied by the judge

- Compilation runs inside the sandbox with `compile_limits`; compiler output
  is kept up to 64 KiB (truncated beyond that) and shown to the contestant.
- The time limit is CPU time summed over every thread and process (cgroup
  accounting); the wall-clock limit is `max(2×TL, TL+1s)` unless the dataset
  sets one.
- With cgroups the memory limit is the peak of the whole control group. The
  JVM gets `-Xmx` equal to the limit, so JVM overhead counts against it.
- Standard error of the contestant's program is discarded.
