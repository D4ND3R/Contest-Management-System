# Cómo se defiende el juez

Un envío es código no confiable que se ejecuta en tu servidor. El CMS
detiene lo que intente en capas, y avisa al staff cuando un envío parece
un ataque.

## El sandbox

Cada compilación y cada ejecución ocurre en una caja de
[isolate](https://github.com/ioi/isolate), nueva cada vez:

- sus propios namespaces: sin red (solo un loopback privado), su propia
  lista de procesos, un sistema de archivos privado donde los directorios
  del sistema son de solo lectura y solo la caja se puede escribir;
- un usuario sin privilegios por caja, límites de grupos de control en
  tiempo de CPU, memoria y procesos (uno para la mayoría de los
  problemas), límites de tamaño de archivos, archivos abiertos y pila;
- `/dev/shm` y `/tmp` privados, que se borran al terminar.

## El filtro seccomp

Además de isolate, el worker ejecuta cada programa a través de un lanzador
pequeño que instala un **filtro seccomp** antes de ejecutarlo. El filtro
mata el programa en cuanto hace una llamada al sistema que ningún programa
de concurso necesita, las que llegan a las partes del kernel donde se
encontraron la mayoría de las fugas de sandboxes:

- namespaces nuevos (`unshare`, `setns`, `clone` con banderas de
  namespace);
- `bpf`, `io_uring`, `userfaultfd`, `perf_event_open`;
- llaveros (`keyctl`, `add_key`), `ptrace`, la memoria de otros procesos;
- montajes, `chroot`, `pivot_root`, módulos del kernel, `kexec`,
  `reboot`, swap, relojes, `syslog`, puertos de E/S.

Los hilos siguen funcionando en todos los lenguajes (`clone3` responde "no
implementado" y la biblioteca de C usa `clone`, que el filtro sí puede
revisar), igual que los compiladores. El filtro lo heredan todos los
procesos que el programa inicie y no se puede quitar.

Un programa que el filtro mata recibe el veredicto **Violación de
seguridad** ("el programa hizo una llamada al sistema prohibida"); una
compilación detenida por él falla con "Compilación detenida: llamada al
sistema prohibida".

El worker compila el lanzador con el compilador de C del sistema al
arrancar (el instalador instala `gcc` y `libc6-dev`). La opción
`worker.seccomp` (o `CMS_WORKER_SECCOMP`) elige:

| Valor | Significado |
|---|---|
| `auto` (por omisión) | lo usa; sin compilador de C, funciona sin él y avisa |
| `on` | no arranca sin él |
| `off` | nunca lo usa |

**Jueces** (administración) muestra *seccomp* o *sin seccomp* junto a cada
worker, el panel avisa cuando un worker funciona sin él, y `cms ctl
judge-selftest` comprueba que las llamadas prohibidas terminen en
violación de seguridad ([verificar una máquina](verificar-host.md)).

## Envíos sospechosos

Cuando llega un envío, se revisa su código en busca de lo que ataca al
juez en lugar de resolver el problema:

| Marca | Ejemplos |
|---|---|
| ejecuta otros programas | `fork`, `system`, `exec*`, `subprocess`, `ProcessBuilder`, `Command::new` |
| usa la red | sockets, `java.net`, `std::net`, `"net"` |
| llamadas directas al sistema | `syscall(...)`, `asm` en línea con `syscall`, `asm!` |
| lee archivos del sistema | cadenas que nombran `/proc`, `/sys`, `/etc`, `/root`, `/home`, `/var` |
| incluye un archivo que no es del envío | `#include </etc/...>`, `#include "../..."` |
| inspecciona otros procesos | `ptrace`, `process_vm_readv` |
| carga o genera código nativo | `dlopen`, `PROT_EXEC`, `ctypes`, `System.loadLibrary`, `DllImport` |
| datos binarios en un archivo fuente | bytes NUL, UTF-8 inválido |

y el sandbox agrega **llamada al sistema prohibida** cuando el filtro mató
al programa (al compilar o en un caso de prueba).

Una marca nunca cambia un puntaje: el envío se evalúa como siempre, y el
sandbox ya detuvo lo que haya intentado. Las marcas le dicen al staff
dónde mirar:

- la página del envío las muestra con el archivo, la línea y el código, o
  el caso de prueba;
- los **Envíos** del concurso llevan la etiqueta *sospechoso*, y el filtro
  de estado tiene **sospechosos**;
- las notificaciones del panel del concurso los cuentan.

Si un envío lo amerita, **invalídalo** con un motivo (sigue visible y deja
de contar) y habla con el concursante.

## Archivos subidos

- Los banners de los concursos se revisan por su contenido: PNG, JPEG,
  GIF o WebP, nunca SVG (puede llevar scripts).
- Los enunciados HTML se convierten al modelo del CMS: se quitan los
  scripts y todo lo activo.
- Los zip (envíos de solo salida, paquetes) se leen con límites de tamaño
  por archivo, así que una "bomba zip" se rechaza.
- Todas las páginas se sirven con una Content Security Policy estricta (sin
  scripts en línea), tokens CSRF en cada formulario y `nosniff`.
