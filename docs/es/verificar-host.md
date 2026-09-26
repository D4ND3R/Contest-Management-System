# Verificar una máquina de evaluación

**No inicies un concurso en una máquina donde `scripts/verify-host.sh`
informe `RESULT: FAIL`.** Ejecútalo en cada máquina con un worker después de
instalar, después de cada actualización del kernel, de isolate o del CMS, y
el día del concurso antes de abrir las puertas.

```sh
sudo systemctl stop cms-worker        # sus trabajos alterarían los tiempos
sudo scripts/verify-host.sh --config /etc/cms/cms.yaml
sudo systemctl start cms-worker
```

Opciones: `--languages c11,cpp17,python3` (evaluar solo los lenguajes del
concurso; por defecto, todos los que tengan compilador instalado),
`--runs N` (por defecto 2), `--cms RUTA` (el binario cms), `--isolate RUTA`,
`--skip-judge` (solo las comprobaciones del entorno), `--box` /
`--box-offset` (cajas de isolate a usar; no deben pertenecer a un worker en
marcha).

## Qué comprueba

| Comprobación | FALLA cuando | Arreglo que sugiere |
|--------------|--------------|---------------------|
| privilegios | no es root | ejecutar con sudo |
| kernel | anterior a 5.4 | una distribución actual |
| isolate | falta, no es setuid root, sin configuración, `box_root` inseguro | `scripts/install-isolate.sh`, `chown`/`chmod` |
| grupos de control | cgroup v1 con isolate 2, faltan controladores (cpuset, memory, pids), `isolate.service` detenido | parámetro del kernel, `systemctl enable --now isolate.service` |
| sandbox | `isolate --cg --init` / `--run` falla | el error de isolate |
| seccomp | nunca (advertencia sin soporte del kernel o sin compilador de C) | `apt-get install gcc libc6-dev` |
| CPUs, SMT, turbo, gobernador, swap, NTP, ASLR, huge pages | nunca (advertencias) | cómo estabilizar los tiempos |
| autoprueba del juez | un veredicto inesperado, una comprobación del host fallida, un veredicto distinto entre corridas | ver abajo |

Las advertencias no bloquean el concurso pero hacen menos estables los
tiempos (hyperthreading, turbo, escalado de frecuencia, swap, aleatorización
del espacio de direcciones, transparent huge pages); en una máquina de
evaluación dedicada corrígelas todas. En un VPS la mayoría no se ve desde
dentro de la máquina virtual.

`sudo cms-host-tuning enable` (o la opción `--tune-host` del instalador)
fija el gobernador en performance y apaga el turbo y las transparent huge
pages, ahora y en cada arranque; `sudo cms-host-tuning status` muestra el
estado. La aleatorización del espacio de direcciones (ASLR) y el SMT solo
se cambian si se ponen en `off` en `/etc/cms/host-tuning.conf`: apagar ASLR
debilita todos los programas de la máquina, así que hazlo solo en una
máquina de evaluación dedicada. Estos ajustes también afectan a los demás
servicios de la máquina.

**Hyperthreading.** Dos hilos (hyperthreads) de un mismo núcleo físico
comparten sus unidades de ejecución: un programa en uno hace más lento lo
que corre en el otro. Desde la 0.3 el instalador evalúa en una CPU por
núcleo físico y deja libres sus hermanas (las muestra: "idle hyperthreads:
5 6 7"), y un `cms.yaml` existente con la distribución anterior pasa a la
nueva. La autoprueba muestra un `WARNING` si dos CPUs de evaluación
configuradas son hermanas. `--judge-all-threads` evalúa en todos los hilos
(más capacidad, tiempos menos estables).

## La autoprueba del juez

`cms ctl judge-selftest` (también se puede usar sola) evalúa, con el mismo
código que los workers y en los núcleos de evaluación configurados:

- la **batería de seguridad**: fork bombs (1 y 64 procesos permitidos),
  lectura de archivos del host, acceso a la red, escritura fuera de la caja,
  dormir para siempre, consumo excesivo de memoria y de salida, stderr
  enorme, `#include </dev/random>` y `</dev/zero>` al compilar, hilos con y
  sin permiso, `kill(-1)`, desbordamiento de pila, escalada de privilegios,
  y las llamadas que el [filtro seccomp](seguridad.md) prohíbe (namespaces de
  usuario, `clone` a una red nueva, `bpf`, `io_uring`, `ptrace`, `keyctl`,
  `perf_event_open`), que deben terminar en violación de seguridad.
  Además del veredicto comprueba el host: ningún proceso de los usuarios de
  la sandbox sobrevive, no aparece ningún archivo fuera de la caja, un
  servidor en el host no recibe conexiones;
- las **soluciones de ejemplo** (AC, WA, TLE, MLE, RE, CE) en cada lenguaje
  con compilador instalado.

Todo se evalúa `--runs` veces (2 por defecto) y cada veredicto debe ser el
mismo en todas las corridas (ambos límites de tiempo cuentan como TLE). Un
caso de seguridad que falla significa que la sandbox no es segura; un
veredicto que cambia entre corridas significa que los tiempos no son lo
bastante estables para evaluar con justicia.

Un caso de seguridad pasa con cualquier resultado que muestre que la
sandbox resistió, y las corridas se comparan con ese criterio. Las fork
bombs, por ejemplo, pueden detenerse por el límite de tiempo o por el de
memoria: cada fork que el límite de procesos rechaza igual reserva las
estructuras del kernel del hijo, se cargan a la caja y se liberan recién
después de un periodo de gracia de RCU, así que con 64 procesos haciendo
fork en un ciclo pueden llegar antes al límite de memoria. En ambos casos
se mata la caja entera y no sobrevive ningún proceso, lo que se comprueba.

El instalador pausa `cms-worker` mientras ejecuta la verificación, para que
los trabajos en cola no alteren los tiempos.
