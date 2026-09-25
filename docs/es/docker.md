# Instalar con Docker Compose

Una alternativa al [instalador de una línea](despliegue.md): todo CMS en
una máquina Linux como contenedores, con las imágenes que se publican con
cada versión (`ghcr.io/d4nd3r/contest-management-system/cms` y `/worker`,
para amd64 y arm64). Los archivos están en [`deploy/docker/`](../../deploy/docker/).

Requisitos: la misma máquina que para el instalador — Linux en KVM o en
hardware dedicado con **grupos de control v2**, amd64 o arm64 (ver
[requisitos del servidor](despliegue.md#requisitos-del-servidor)) — con
Docker Engine 24 o posterior y el plugin compose. Docker Desktop (Windows,
macOS) no puede evaluar.

## Instalar

```sh
git clone --depth 1 https://github.com/D4ND3R/Contest-Management-System.git
cd Contest-Management-System/deploy/docker
./setup.sh --domain cms.ejemplo.org      # HTTPS para cms., ranking. y admin.cms.ejemplo.org
./setup.sh                               # o HTTP simple: concurso :80, ranking :8080, admin :8081
```

`setup.sh` escribe `.env` la primera vez (contraseñas aleatorias de la base
de datos y de Valkey, clave secreta y token del ranking; la distribución de
CPU y memoria de esta máquina), descarga las imágenes, inicia todo y crea
el administrador `admin` con una contraseña aleatoria **que muestra una
sola vez**. Se puede volver a correr cuando quieras: los secretos y el
administrador se conservan. Luego evalúa la batería de seguridad y las
soluciones de ejemplo dos veces dentro del worker:

```sh
docker compose exec worker cms ctl judge-selftest
```

Debe terminar con `RESULT: OK`; nunca inicies un concurso si no.

## Qué corre

| Servicio | Imagen | Notas |
|----------|--------|-------|
| `postgres` | postgres:16 | commits durables, sin workers paralelos, memoria según `.env` |
| `valkey` | valkey 8 | append-only, nunca descarta (las colas de evaluación) |
| `init` | cms | aplica las migraciones antes de que arranque lo demás |
| `contest-web`, `admin-web`, `ranking-web`, `dispatcher`, `monitor` | cms | en `WEB_CPUS` |
| `worker` | worker | la sandbox, en `JUDGE_CPUS` (un espacio de evaluación por CPU) |
| `caddy` | caddy 2 | HTTPS con certificados automáticos, o HTTP simple |
| `printing` | cms | solo con `docker compose --profile printing up -d` |

La configuración sin secretos es `cms.yaml` (edítala y luego
`docker compose up -d`); los secretos y la distribución de la máquina están
en `.env` (mantenlo privado). Los datos viven en volúmenes con nombre:
`pgdata`, `valkeydata`, `blobs` (envíos, casos de prueba), `ranking`,
`backups`, `caddy` (certificados).

**El worker no es privilegiado.** Recibe lo que isolate necesita y nada
más: `CAP_SYS_ADMIN` (espacios de nombres y montajes; el perfil seccomp
predeterminado de Docker entonces los permite), `CAP_NET_ADMIN` (la
interfaz loopback de cada caja), sin confinamiento de AppArmor (el perfil de
Docker prohíbe los montajes) y un espacio de nombres de cgroup propio, cuyo
árbol cgroup v2 el entrypoint vuelve escribible y entrega a isolate. El
worker en sí corre luego con el usuario sin privilegios `cms`, como los
demás contenedores.

## Respaldos

El servidor web del admin toma respaldos en el volumen `backups` (uno por
día, cada 15 minutos alrededor de un concurso; **Respaldos** en el admin, o
`docker compose exec admin-web cms ctl dump`). Guarda copias fuera de la
máquina:

```sh
docker compose cp admin-web:/var/lib/cms/backups ./backups   # copiarlos afuera
```

Para restaurar uno ([detalles](respaldos.md)):

```sh
docker compose stop contest-web admin-web ranking-web dispatcher monitor worker
docker compose cp ./cms-backup-....tar.zst admin-web:/var/lib/cms/backups/
docker compose run --rm init ctl restore -force /var/lib/cms/backups/cms-backup-....tar.zst
docker compose up -d
```

## Actualizar

```sh
./setup.sh --version 1.3.0     # o pon CMS_VERSION en .env
```

descarga las imágenes nuevas y recrea los contenedores; `init` aplica las
migraciones antes de que arranquen los servicios. Toma un respaldo antes
(arriba) y no actualices durante un concurso. Volver a una versión
anterior solo es posible restaurando un respaldo tomado con ella.

## Desinstalar

```sh
docker compose down            # detiene y elimina los contenedores; los volúmenes (datos) quedan
docker compose down -v         # borra también cada volumen: base de datos, archivos, respaldos
```

¿Se perdió la contraseña del administrador? `docker compose run --rm init ctl admin-password`.
