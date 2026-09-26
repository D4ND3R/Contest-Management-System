# Instalar CMS en un servidor

Esta guía instala todos los servicios de CMS en un servidor Linux con un
solo comando, lo actualiza y lo desinstala. El objetivo de referencia es
un **VPS KVM de 2 vCPU con 4 GB de RAM**: la CPU 0 atiende los servidores
web, PostgreSQL, Valkey y el proxy HTTPS; la CPU 1 queda reservada para la
sandbox, así la cola de envíos nunca vuelve lento el sitio del concurso.
Máquinas más grandes funcionan igual (desde 6 CPUs, las CPUs 0–1 atienden
la web y el resto evalúa); más capacidad de evaluación se obtiene con
[workers en otras máquinas](worker-externo.md). Para correrlo en
contenedores, ver [Docker Compose](docker.md).

## Requisitos del servidor

| | Necesario |
|---|---|
| Sistema | **Linux**: Ubuntu 22.04, 24.04 o 26.04, Debian 12 o 13 (el instalador lo comprueba) |
| Máquina | una **máquina virtual KVM** (la mayoría de los VPS) o un **servidor dedicado** |
| Acceso | **root** (sudo) por SSH |
| Kernel | **grupos de control v2** (lo predeterminado en esos sistemas; el instalador lo comprueba y `--enable-cgroup-v2` los activa) |
| Arquitectura | **amd64** (x86-64) o **arm64** (aarch64) |
| Red | un dominio con tres nombres (`cms.ejemplo.org`, `admin.`, `ranking.`) para HTTPS, o una red local (HTTP simple) |

**No sirven para evaluar** (la sandbox no puede medir ni encerrar los
programas, así que los veredictos serían incorrectos o inseguros):

- **Windows y macOS** (incluidos WSL y Docker Desktop): úsalos como
  computadoras de los administradores o concursantes, no como servidor.
- **VPS de contenedores (OpenVZ, LXC, Virtuozzo)**: comparten el kernel
  del anfitrión y no pueden crear los grupos de control de la sandbox. Pide
  KVM.
- **Plataformas serverless y PaaS** (AWS Lambda, Cloud Run, Heroku,
  Vercel...): CMS necesita servicios permanentes, una base de datos y root.
- Arquitecturas de 32 bits u otras.

### Tamaños según el número de concursantes

| Concursantes | Servidor principal | Evaluación | Base |
|--------------|--------------------|------------|------|
| hasta ~500 | 2 vCPU, 4 GB de RAM, 40 GB de SSD | 1 núcleo: 70–80 envíos de C++ pesado por minuto en el pico | medido ([pruebas de carga](../../loadtest/README.md)) |
| hasta ~1,000 | 4 vCPU, 8 GB de RAM, 80 GB de SSD | 3 núcleos | web medida en un núcleo (páginas bajo 150 ms; deja entrar a los concursantes unos minutos antes del inicio) |
| 1,000–3,000 | 8 vCPU, 16 GB de RAM, 160 GB de SSD, más [workers externos](worker-externo.md) | 6 núcleos o más | estimación: confírmala con `make loadtest` en esa máquina |

La evaluación suele ser el límite más estrecho: cuenta los envíos del
cierre (cada concursante cada 30 segundos en los últimos minutos es común)
frente a unos 75 por minuto por núcleo con C++ pesado, muchos más con
programas livianos. Disco: envíos, casos de prueba y 48 respaldos; un
concurso grande con casos grandes necesita más.

## Instalar (una línea)

En el servidor, con un usuario con sudo:

```sh
curl -fsSL https://raw.githubusercontent.com/D4ND3R/Contest-Management-System/main/scripts/install.sh \
  | sudo bash -s -- --domain cms.ejemplo.org --email tu@ejemplo.org
```

El mismo script viene adjunto a cada versión, en
`https://github.com/D4ND3R/Contest-Management-System/releases/latest/download/install.sh`
(usa esa dirección en el mismo comando si el servidor no llega a
`raw.githubusercontent.com`).

Sin `--domain` sirve HTTP simple en las direcciones de la máquina
(concurso en el puerto 80, ranking 8080, admin 8081), para un concurso en
una red local. El instalador:

1. comprueba la máquina (sistema, arquitectura, que no sea un contenedor,
   grupos de control v2) y se detiene con una explicación si no puede
   evaluar;
2. descarga de GitHub la última versión para esta arquitectura y
   **verifica su suma SHA-256** (una descarga corrupta o alterada detiene
   todo);
3. instala los paquetes, los compiladores e isolate;
4. crea el usuario `cms`, ajusta PostgreSQL y Valkey a las CPUs y la
   memoria que encuentra, instala las unidades de systemd y las fija a la
   CPU web;
5. configura HTTPS con Caddy (certificados de Let's Encrypt) si se da un
   dominio, y el firewall;
6. crea el primer administrador, **`admin`, con una contraseña aleatoria
   que muestra una sola vez al final** (no se guarda en ningún lado);
7. corre [`cms-verify-host`](verificar-host.md): la batería de seguridad y
   las soluciones de ejemplo se evalúan dos veces. **Nunca inicies un
   concurso en un servidor donde falla.**

Opciones útiles: `--version 1.2.0` (una versión dada), `--dry-run`
(comprueba la máquina, descarga y verifica la versión, muestra cada cambio
y no cambia nada), `--web nginx` (nginx + certbot en lugar de Caddy),
`--admin-allow 203.0.113.0/24` (quién puede abrir el admin), `--languages
full` (las doce herramientas en lugar de C, C++, Python y Java),
`--private-ip 10.8.0.1` (para que los [workers externos](worker-externo.md)
lleguen a este servidor), `--no-firewall`, `--enable-cgroup-v2`. La lista
completa: `--help`, o el comienzo de
[scripts/install.sh](../../scripts/install.sh).

Sin acceso a Internet en el servidor, descarga `cms_<versión>_linux_<arq>.tar.gz`
y `checksums.txt` de la [página de versiones](https://github.com/D4ND3R/Contest-Management-System/releases),
copia ambos y corre `sudo bash scripts/install.sh --archive cms_..._linux_amd64.tar.gz`
desde el tarball descomprimido (los paquetes siguen viniendo del espejo de
la distribución).

El instalador se puede volver a correr en cualquier momento: conserva
`/etc/cms/cms.yaml`, los secretos y la versión instalada, reescribe los
demás archivos generados solo si cambian y reinicia los servicios. Nunca
cambia la versión: eso lo hace [`cmsctl upgrade`](#actualizar).

### Primer inicio de sesión

Abre el sitio del admin y entra como `admin` con la contraseña que mostró
el instalador. Luego activa la verificación en dos pasos (**Mi cuenta**).
Si se pierde la contraseña: `sudo -u cms cmsctl admin-password` pone y
muestra una nueva. Los administradores que entran con una contraseña por
defecto conocida (`admin`, `password`, su nombre de usuario...) deben
elegir otra antes de cualquier otra cosa.

## Actualizar

```sh
sudo cmsctl upgrade                    # la última versión
sudo cmsctl upgrade -version 1.3.0     # una versión dada
```

No corre mientras haya un concurso en curso (la ventana de alguien sigue
abierta), salvo con `-force`. Luego descarga la versión y verifica su
suma, toma un respaldo (tipo *actualización*, en **Respaldos** del admin),
detiene los servicios, cambia `/opt/cms/current` a la versión nueva,
aplica las migraciones de la base de datos, inicia los servicios y espera
a que todos respondan. **Si algo de eso falla, vuelve atrás solo**: la
versión anterior, la base de datos restaurada desde ese respaldo, los
servicios iniciados. Los servicios quedan detenidos alrededor de un
minuto; las tres últimas versiones quedan en `/opt/cms/releases/`. Volver a
una versión anterior solo es posible restaurando un respaldo tomado con
ella ([respaldos](respaldos.md)).

En un [worker externo](worker-externo.md), corre
`sudo cmsctl upgrade -version <la versión del servidor principal>` después
del servidor principal: un worker no tiene base de datos, así que solo
cambia la versión y reinicia (las evaluaciones en curso vuelven a la cola).

## Desinstalar

```sh
curl -fsSL https://raw.githubusercontent.com/D4ND3R/Contest-Management-System/main/scripts/install.sh | sudo bash -s -- --uninstall
```

detiene y elimina los servicios, los binarios y `/opt/cms`, y conserva los
datos: `/etc/cms` (configuración y secretos), `/var/lib/cms` (archivos y
respaldos) y la base de datos PostgreSQL `cms`. `--uninstall --purge`
borra también eso y el usuario `cms` (toma un respaldo antes). PostgreSQL,
Valkey, el proxy, isolate y los compiladores quedan instalados. Agrega
`--dry-run` para ver la lista primero.

## Qué hace el instalador, paso a paso

### Paquetes

`gcc g++ python3 openjdk-17/21-jdk-headless` (más `pypy3 fp-compiler rustc
golang-go kotlin mono-mcs ghc` con `--languages full`), `postgresql`,
`valkey-server` (o `redis-server` donde Valkey no está empaquetado), `caddy`
(o `nginx certbot python3-certbot-nginx`) y `ufw`. Ubuntu 22.04 no empaqueta
Caddy: allí el instalador agrega el repositorio oficial de Caddy
(`/etc/apt/sources.list.d/caddy-stable.list`).

### isolate y cgroup v2

`scripts/install-isolate.sh` compila isolate 2 desde el código fuente, lo
instala setuid root con la configuración `/usr/local/etc/isolate` (cajas en
`/var/local/lib/isolate`, uids desde 60000) y activa `isolate.service`, que
mantiene el grupo de control que usa isolate. isolate 2 necesita la
jerarquía unificada cgroup v2 (la predeterminada en Debian 11+ y Ubuntu
21.10+); en un sistema anterior el instalador se detiene y lo explica; con
`--enable-cgroup-v2` agrega `systemd.unified_cgroup_hierarchy=1` a la línea
de arranque del kernel, y reinicias y lo vuelves a correr.

### Usuario y archivos

| Ruta | Contenido |
|------|-----------|
| `/opt/cms/releases/<versión>/` | las versiones descomprimidas (las tres últimas) |
| `/opt/cms/current` | la versión en uso (`cmsctl upgrade` la cambia) |
| `/usr/local/bin/cms`, `cmsctl` | enlaces a los binarios de la versión en uso |
| `/usr/local/sbin/cms-verify-host` | enlace a su `scripts/verify-host.sh` |
| `/etc/cms/cms.yaml` | configuración (grupo `cms`, modo 640) |
| `/etc/cms/secrets.env` | contraseñas y tokens generados (root, modo 600) |
| `/etc/cms/languages/` | definiciones de lenguajes |
| `/var/lib/cms/{blobs,ranking,backups,worker}` | datos (usuario `cms`) |
| `/var/cache/cms/` | caché de casos de prueba del worker |

Todos los servicios corren con el usuario de sistema sin privilegios `cms`.

### PostgreSQL, ajustado a la máquina

`/etc/postgresql/<versión>/main/conf.d/cms.conf`: `shared_buffers` = RAM/8
(128 MB–4 GB), `effective_cache_size` = RAM/2, `maintenance_work_mem` =
RAM/32, `work_mem 8MB` (16 MB desde 16 GB de RAM), `max_worker_processes`
= las CPUs, `max_connections 100`, **sin workers de consultas paralelas**
(ocuparían los núcleos de evaluación), `jit off` (las consultas cortas no lo amortizan),
`synchronous_commit on` (nunca se pierde un envío aceptado), `wal_compression
on`, `random_page_cost 1.1` (SSD). El rol y la base `cms` reciben una
contraseña aleatoria. Cada servicio usa como máximo 16 conexiones
(`database.max_conns`).

Usa el clúster `main` del PostgreSQL más nuevo instalado, en el puerto de
ese clúster. Después de actualizar la distribución (por ejemplo, Ubuntu
24.04 a 26.04), el clúster de la versión anterior suele quedarse con el
puerto 5432 y el nuevo recibe el 5433; el instalador toma el nuevo y
escribe su puerto en `/etc/cms/cms.yaml`. Si no hay ningún clúster, crea
uno (`C.UTF-8`), y la base de datos siempre se crea en UTF-8, sea cual sea
la codificación por defecto del clúster. Si PostgreSQL no arranca, el
instalador se detiene y muestra su registro (`pg_lsclusters` lista los
clústeres, sus puertos y su estado).

### Valkey

`/etc/valkey/cms.conf` (incluido desde `valkey.conf`): escucha solo en
localhost (más `--private-ip`), exige contraseña, `appendonly yes` con
`appendfsync everysec` y `maxmemory-policy noeviction`: ahí viven las colas
de evaluación y deben sobrevivir a un reinicio. Desde 6 CPUs usa dos hilos
de E/S.

### systemd

`deploy/systemd/` tiene una unidad por servicio (`cms-contest-web`,
`cms-admin-web`, `cms-ranking-web`, `cms-dispatcher`, `cms-monitor`,
`cms-worker`, `cms-printing`, `cms-blob-server`) y `cms.target`, que las
agrupa:

```sh
sudo systemctl status cms.target 'cms-*'
sudo systemctl restart cms.target          # todos los servicios del CMS habilitados
journalctl -u cms-contest-web -f           # logs (JSON)
```

Cada unidad se reinicia sola (`Restart=always`, 2 s). Los servicios web
están aislados por systemd (sistema de solo lectura, /tmp privado, sin
nuevos privilegios, solo escriben en `/var/lib/cms`).

**Fijación de CPUs.** Unos drop-ins (`/etc/systemd/system/<unidad>.d/cpu.conf`)
ponen `CPUAffinity=0` a todos los servicios del CMS salvo el worker, y a
PostgreSQL, Valkey y el proxy. El worker no se restringe: fija cada sandbox
a sus núcleos de evaluación (`worker.cores: [1]` en `cms.yaml`) y sus
propios hilos a las demás CPUs.

### HTTPS

Con Caddy, `/etc/caddy/Caddyfile` tiene un sitio por nombre, cada uno un
`reverse_proxy` al servicio local; Caddy obtiene y renueva solo los
certificados de Let's Encrypt y transmite los server-sent events al
instante. `--admin-allow CIDR` responde 403 a cualquier otra dirección en el
sitio de administración. Con nginx, `/etc/nginx/sites-available/cms` hace de
proxy con `proxy_buffering off` (server-sent events) y `certbot --nginx`
agrega los certificados. Los servicios del CMS escuchan solo en 127.0.0.1 y
confían en `X-Forwarded-For` del proxy (`trusted_proxies`), así las
restricciones por IP y los logs ven las direcciones de los concursantes.

### Firewall

`ufw`: rechaza todo lo entrante salvo SSH, 80 y 443 (en una LAN: 80, 8080 y
8081; el puerto del admin solo desde `--admin-allow`). Con `--private-ip`,
los puertos 6379 (Valkey) y 8891 (servidor de blobs) se abren solo en esa
dirección.

### Base de datos y primer administrador

`cmsctl bootstrap -generate-password` aplica las migraciones y, solo la
primera vez, crea el administrador `admin` con una contraseña aleatoria que
el instalador muestra una vez al final; no se escribe en ningún lado
(`/etc/cms/secrets.env` guarda las contraseñas de la base de datos y de
Valkey y los tokens). Las actualizaciones aplican las migraciones nuevas
con `cmsctl upgrade`.

## Después de instalar

1. `sudo cms-verify-host --config /etc/cms/cms.yaml` debe terminar con
   `RESULT: OK` ([detalles](verificar-host.md)). **Nunca inicies un
   concurso en una máquina donde falla.**
2. Entra al admin con la contraseña mostrada y activa la verificación en dos pasos.
3. Crea o importa un concurso y sus problemas ([paquetes de
   problema](paquete-de-problema.md)), agrega los usuarios.
4. Revisa **Respaldos** en el admin: se toma un respaldo por día, y cada 15
   minutos alrededor de un concurso ([respaldos](respaldos.md)).
5. Lee el [manual del día del concurso](dia-del-concurso.md).

### Impresión

Los trabajos de impresión de los concursantes (se activan por concurso, ver
[configuración del concurso](configuracion-del-concurso.md#impresión)) los
envía a CUPS `cms-printing`, que corre en el servidor principal. Para usar
una impresora:

```sh
sudo apt install cups-client          # o cups, si la impresora está conectada aquí
lpstat -p -d                           # destinos que conoce CUPS
```

luego pon `printing.printer: <destino>` en `/etc/cms/cms.yaml` (y
`paper_size: Letter` si hace falta) y ejecuta
`sudo systemctl restart cms-printing`. Cada trabajo se imprime como un solo
trabajo de CUPS: una portada (usuario, nombre, equipo, sede, archivo,
páginas) seguida del documento. Sin impresora los trabajos se marcan como
impresos con "not printed: no printer configured", útil para un ensayo. Los
fallos de `lp` se reintentan tres veces; después el trabajo aparece como *no
impreso* en la cola del staff, donde se puede imprimir de nuevo. Corre un
solo servicio de impresión: al arrancar retoma los trabajos que estaba
imprimiendo, así que una caída puede imprimir un trabajo dos veces pero
nunca pierde uno.

## Monitoreo

- `https://admin.../system`: workers, colas, trabajos en curso.
- Cada servicio responde `/healthz`; métricas de Prometheus en `/metrics`
  (servicios web) y en 127.0.0.1:9101–9104 (dispatcher, worker, monitor,
  impresión). Alertas útiles: `up == 0`,
  `cms_backup_last_success_timestamp_seconds` con más de 30 minutos durante
  un concurso, una cola que crece durante minutos.
- Un servicio que parece trabado: `sudo systemctl kill -s USR1
  cms-dispatcher` (cualquier unidad) escribe la pila de cada goroutine en
  su journal sin detenerlo (`journalctl -u cms-dispatcher`); adjúntala a un
  reporte de error.
- Perfilar un servidor web bajo carga: `pprof: true` en `contest_web`,
  `admin_web` o `ranking_web` sirve el profiler de Go en `/debug/pprof/`
  en el puerto del propio servicio, solo a peticiones hechas desde la misma
  máquina (nunca a través del proxy HTTPS), p. ej. `go tool pprof
  http://127.0.0.1:8888/debug/pprof/profile?seconds=20`. Déjalo apagado
  el resto del tiempo.

## Seguridad y límites

Los servidores web envían una Content-Security-Policy estricta (sin código
en línea ni orígenes externos), `X-Frame-Options: DENY`, `nosniff` y una
política de referer del mismo origen; las cookies de sesión son
`HttpOnly`, `SameSite=Lax` y, con `cookie_secure: true` (lo pone el script
de instalación cuando hay HTTPS), `Secure`. Todo formulario lleva un token
CSRF por sesión y se rechazan las peticiones de otro origen. Opciones de
`cms.yaml`:

| Opción | Por defecto | Significado |
|--------|-------------|-------------|
| `contest_web.login_rate_limit_per_minute` | 20 | inicios de sesión **fallidos** por dirección y minuto (los exitosos no cuentan, así un laboratorio detrás de una sola dirección NAT entra a la vez); además un usuario se bloquea tras 10 fallos por minuto desde cualquier dirección |
| `admin_web.login_rate_limit_per_minute` | 20 | lo mismo para administradores; 5 códigos de segundo factor erróneos por minuto bloquean al administrador |
| `contest_web.rate_limit_per_minute` | 120 | envíos y user tests por concursante y minuto (además de los límites del concurso) |
| `contest_web.max_submission_bytes` | 1 MiB | una petición de envío; el límite de fuente de la tarea se aplica por archivo |
| `contest_web.max_user_test_bytes` | 8 MiB | un user test (fuentes y entrada) |
| `contest_web.max_print_bytes` | 2 MiB | un trabajo de impresión |
| `admin_web.max_upload_bytes` | 1 GiB | una subida del admin (paquetes, archivos de testcases, archivos de concurso) |
| `*.trusted_proxies` | — | proxies cuyo `X-Forwarded-For` se cree (el proxy HTTPS) |

Las peticiones que superan estos límites se rechazan con 413 antes de leer
nada en memoria o en disco; los formularios simples se limitan a 64 KiB
(concursantes) y 1 MiB (administradores). Las verificaciones de contraseña
(argon2id, 19 MiB cada una) corren como máximo una por núcleo a la vez (al
menos dos), así
una avalancha de inicios de sesión espera su turno en lugar de agotar la
memoria.
