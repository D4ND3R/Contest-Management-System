# Lenguajes de programación

Cada lenguaje es un archivo YAML en `config/languages/`. Añadir un lenguaje
consiste en añadir un archivo y reiniciar los servicios: no se toca código.
Los workers reciben la definición dentro de cada trabajo, así que todos usan
siempre los mismos comandos.

## Incluidos

| id | Lenguaje | Toolchain usada en las pruebas |
|----|----------|--------------------------------|
| `c11` | C11 | gcc 13.3 (`-std=gnu11 -O2 -static`) |
| `cpp17` | C++17 | g++ 13.3 (`-std=gnu++17 -O2 -static`) |
| `cpp20` | C++20 | g++ 13.3 (`-std=gnu++20 -O2 -static`) |
| `java` | Java | OpenJDK 21 (`-Xmx` = límite de memoria, SerialGC) |
| `python3` | Python 3 | CPython 3.11 |
| `pypy3` | Python 3 | PyPy 7.3 (Python 3.9) |
| `pascal` | Pascal | Free Pascal 3.2.2 |
| `rust` | Rust | rustc (edición 2021, `-O`) |
| `go` | Go | Go 1.27 (`GOMAXPROCS=1`, caché de compilación precalentada) |
| `kotlin` | Kotlin | kotlinc (JVM), OpenJDK 21 |
| `csharp` | C# | Mono 6.8 (`mcs -optimize+`) |
| `haskell` | Haskell | GHC 9.4 (`-O2`) |

Las versiones son las de los paquetes del sistema del worker; se fijan
fijando la imagen o los paquetes del worker. `version_command` muestra la
versión exacta.

## Formato

```yaml
id: cpp17                      # identificador único guardado con los envíos
name: "C++17 / g++"            # nombre visible para el concursante
version_command: ["g++", "--version"]
source_extensions: [".cpp", ".cc"]   # la primera reemplaza %l en los nombres
header_extensions: [".h", ".hpp"]    # cabeceras del grader copiadas junto al código
compile:                       # comandos ejecutados en orden dentro de la sandbox
  - ["/usr/bin/g++", "-O2", "-o", "{executable}", "{sources}"]
executable: "{main}"           # artefacto principal que se conserva
keep_files: ["*.class"]        # artefactos adicionales (globs), opcional
run: ["./{executable}"]
compile_limits: {time: 10s, memory: 1GiB, processes: 32, output: 256MiB}
run_processes: 1               # procesos/hilos mínimos en ejecución
env: {CLAVE: valor}            # se suma a PATH, HOME=/tmp, LANG=C.UTF-8
dirs: ["/etc/java-*"]          # directorios del host de solo lectura (globs)
no_address_space_limit: false  # true para runtimes gestionados (JVM, Go, .NET)
time_multiplier: 1             # opcional: escala los límites de tiempo (0-10)
compile_seed:                  # opcional: caché del compilador precalentada (ver Go)
  dir: ".gocache"
  warmup_file: "warmup.go"
  warmup: "package main ..."
```

Marcadores: `{sources}` (todas las fuentes, grader primero), `{main_source}`
(primera fuente), `{main}` (su nombre base), `{executable}`, `{memory_mb}` y
`{memory_kb}` (comandos de ejecución). Los comandos usan rutas absolutas
(`/usr/bin/env herramienta` para buscar en el PATH dentro de la sandbox).

## Reglas del juez

- La compilación corre dentro de la sandbox con `compile_limits`; la salida
  del compilador se guarda hasta 64 KiB (truncada después) y se muestra.
- El límite de tiempo es tiempo de CPU sumado de todos los hilos y procesos
  (cgroups); el límite de tiempo real es `max(2×TL, TL+1s)` salvo que el
  dataset defina otro.
- `time_multiplier` (ninguno por defecto) da más tiempo a un lenguaje: con
  2, un problema de 1 s permite 2 s en ese lenguaje (también se escala el
  límite de tiempo real del dataset). Los concursantes ven el límite de cada
  lenguaje en la página del problema; los administradores, en
  **Lenguajes**. La mayoría de los concursos, la IOI incluida, usa el mismo
  límite para todos los lenguajes: defínelo solo si el reglamento lo dice.
- Con cgroups el límite de memoria es el pico de todo el grupo. La JVM recibe
  `-Xmx` igual al límite, así que su sobrecarga cuenta.
- La salida de error estándar del programa del concursante se descarta.
