# Despliegue en un VPS (Debian / Ubuntu)

Esta guía instala todos los servicios del CMS en una máquina limpia con
Debian 12+ o Ubuntu 22.04+. El objetivo de referencia es un **VPS KVM de 2
vCPU con 4 GB de RAM**: la CPU 0 atiende los servidores web, PostgreSQL,
Valkey y el proxy HTTPS; la CPU 1 queda reservada para la sandbox, así la
cola de envíos nunca vuelve lento el sitio del concurso. Máquinas más
grandes funcionan igual (desde 6 CPUs, las CPUs 0–1 atienden la web y el
resto evalúa); más capacidad de evaluación se obtiene con [workers en otras
máquinas](worker-externo.md).

`scripts/install.sh` hace todo lo que sigue; los pasos manuales se detallan
para que sepas qué cambia y puedas adaptarlo.

## 0. Antes de empezar

- Una máquina con virtualización KVM (o física): los VPS de contenedores
  (OpenVZ, LXC) no pueden ejecutar la sandbox.
- DNS: tres nombres que apunten a la máquina, p. ej. `cms.example.org`
  (concursantes), `admin.cms.example.org` y `ranking.cms.example.org`. Para
  un concurso en una red local sin Internet usa `--lan`.
- Acceso SSH con un usuario con sudo.

## 1. Instalación rápida

```sh
sudo apt-get install -y git make golang   # solo para compilar; o copia bin/ de una versión publicada
git clone https://github.com/D4ND3R/Contest-Management-System.git /opt/cms-src
cd /opt/cms-src && make build
sudo scripts/install.sh --domain cms.example.org --email tu@example.org \
                        --admin-allow 203.0.113.0/24     # opcional: quién puede abrir el admin
sudo reboot            # solo si el script avisa que hubo que activar cgroup v2
sudo cms-verify-host --config /etc/cms/cms.yaml
```

Luego abre `https://admin.cms.example.org/`, entra como `admin` con el
`ADMIN_PASSWORD` guardado en `/etc/cms/secrets.env` y cámbialo (Cuenta →
contraseña; activa la verificación en dos pasos).

Opciones útiles: `--web nginx` (nginx + certbot en vez de Caddy), `--lan`
(HTTP sin cifrar: concurso en el puerto 80, ranking 8080, admin 8081),
`--languages full` (los doce lenguajes en vez de C, C++, Python y Java),
`--private-ip 10.8.0.1` (para que [workers externos](worker-externo.md)
lleguen a este servidor), `--no-firewall`. `--render-only DIR` escribe en
`DIR` todos los archivos que generaría, sin tocar el sistema.

El script se puede volver a ejecutar cuando quieras (tras `git pull && make
build`, para actualizar): conserva `/etc/cms/cms.yaml` y los secretos,
reescribe los demás archivos generados solo si cambian, aplica las
migraciones de la base de datos y reinicia los servicios.

## 2. Qué hace el script, paso a paso

### Paquetes

`gcc g++ python3 openjdk-17/21-jdk-headless` (más `pypy3 fp-compiler rustc
golang-go kotlin mono-mcs ghc` con `--languages full`), `postgresql`,
`valkey-server` (o `redis-server` donde Valkey no está empaquetado), `caddy`
(o `nginx certbot python3-certbot-nginx`) y `ufw`.

### isolate y cgroup v2

`scripts/install-isolate.sh` compila isolate 2 desde el código fuente, lo
instala setuid root con la configuración `/usr/local/etc/isolate` (cajas en
`/var/local/lib/isolate`, uids desde 60000) y activa `isolate.service`, que
mantiene el grupo de control que usa isolate. isolate 2 necesita la
jerarquía unificada cgroup v2 (la predeterminada en Debian 11+ y Ubuntu
21.10+); en un sistema anterior el script agrega
`systemd.unified_cgroup_hierarchy=1` a la línea de arranque del kernel y
pide reiniciar.

### Usuario y archivos

| Ruta | Contenido |
|------|-----------|
| `/usr/local/bin/cms`, `cmsctl` | los binarios |
| `/usr/local/sbin/cms-verify-host` | `scripts/verify-host.sh` |
| `/etc/cms/cms.yaml` | configuración (grupo `cms`, modo 640) |
| `/etc/cms/secrets.env` | contraseñas y tokens generados (root, modo 600) |
| `/etc/cms/languages/` | definiciones de lenguajes |
| `/var/lib/cms/{blobs,ranking,backups,worker}` | datos (usuario `cms`) |
| `/var/cache/cms/` | caché de casos de prueba del worker |

Todos los servicios corren con el usuario de sistema sin privilegios `cms`.

### PostgreSQL para 2 vCPU

`/etc/postgresql/<versión>/main/conf.d/cms.conf`: `shared_buffers` = RAM/8
(128 MB–2 GB), `effective_cache_size` = RAM/2, `work_mem 8MB`,
`max_connections 100`, **sin workers de consultas paralelas** (ocuparían el
núcleo de evaluación), `jit off` (las consultas cortas no lo amortizan),
`synchronous_commit on` (nunca se pierde un envío aceptado), `wal_compression
on`, `random_page_cost 1.1` (SSD). El rol y la base `cms` reciben una
contraseña aleatoria. Cada servicio usa como máximo 16 conexiones
(`database.max_conns`).

### Valkey

`/etc/valkey/cms.conf` (incluido desde `valkey.conf`): escucha solo en
localhost (más `--private-ip`), exige contraseña, `appendonly yes` con
`appendfsync everysec` y `maxmemory-policy noeviction`: ahí viven las colas
de evaluación y deben sobrevivir a un reinicio.

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

`cmsctl bootstrap` aplica las migraciones y crea el administrador `admin`
con la contraseña generada. Las actualizaciones aplican las migraciones
nuevas del mismo modo (`cmsctl migrate`).

## 3. Después de instalar

1. `sudo cms-verify-host --config /etc/cms/cms.yaml` debe terminar con
   `RESULT: OK` ([detalles](verificar-host.md)). **Nunca inicies un
   concurso en una máquina donde falla.**
2. Entra al admin, cambia la contraseña, activa la verificación en dos pasos.
3. Crea o importa un concurso y sus problemas ([paquetes de
   problema](paquete-de-problema.md)), agrega los usuarios.
4. Revisa **Respaldos** en el admin: se toma un respaldo por día, y cada 15
   minutos alrededor de un concurso ([respaldos](respaldos.md)).
5. Lee el [manual del día del concurso](dia-del-concurso.md).

## Monitoreo

- `https://admin.../system`: workers, colas, trabajos en curso.
- Cada servicio responde `/healthz`; métricas de Prometheus en `/metrics`
  (servicios web) y en 127.0.0.1:9101–9104 (dispatcher, worker, monitor,
  impresión). Alertas útiles: `up == 0`,
  `cms_backup_last_success_timestamp_seconds` con más de 30 minutos durante
  un concurso, una cola que crece durante minutos.
