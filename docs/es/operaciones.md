# Operación: alertas, recuperación a un instante, réplica de lectura, failover, ensayos

Lo que mantiene en pie un concurso grande cuando algo se rompe: alertas que
avisan a alguien, una base de datos que puede volver a cualquier instante,
una réplica para las lecturas pesadas y para el failover, y ensayos que
reproducen un concurso real a través del circuito de evaluación real.

## Alertas

El servicio monitor revisa el sistema cada pocos segundos. Cuando un
problema persiste, los administradores ven una alerta (una notificación
roja y la sección **Alertas** de la página Jueces), y otra vez cuando se
resuelve:

| Alerta | Cuándo |
| --- | --- |
| `queue_backlog` | más de `queue_depth` trabajos esperan evaluación, durante 2 minutos |
| `no_workers` | hay un concurso en curso y ningún worker vivo, durante 30 segundos |
| `judging_latency` | la mediana del tiempo entre envío y puntaje de los últimos 10 minutos supera `judging_latency`, durante 2 minutos |
| `disk_space` | un disco de este servidor o de un worker tiene menos de `disk_free_percent` libre |
| `timing_drift` | los núcleos de evaluación de un worker miden el tiempo distinto que los demás (ver [calibración](evaluacion.md)) |
| `wal_archiving` | PostgreSQL no puede archivar su WAL (no sería posible recuperar a un instante) |

Umbrales y un webhook opcional en `cms.yaml`:

```yaml
monitor:
  alerts:
    queue_depth: 500
    judging_latency: 3m
    disk_free_percent: 10
    # Cada alerta se envía por POST como JSON {"text": ..., "content": ...}:
    # webhooks entrantes de Slack, Mattermost y Discord, ntfy... O CMS_ALERTS_WEBHOOK.
    webhook_url: https://hooks.example.org/...
```

La URL del webhook es un secreto (quien la tiene puede publicar en tu
canal): guárdala en `/etc/cms/cms.yaml` o en el entorno, nunca en Git.

Las instalaciones que ya usan Prometheus y Alertmanager pueden cargar
[`deploy/prometheus/alerts.yml`](../../deploy/prometheus/alerts.yml): las
mismas revisiones, más errores de servidor, páginas lentas, respaldos
viejos y servicios que no responden, sobre las métricas que exporta cada
servicio.

## Archivado continuo y recuperación a un instante

Los respaldos diarios (ver [respaldos](respaldos.md)) devuelven el concurso
al momento en que se tomaron. El archivado continuo va más allá:
PostgreSQL entrega cada segmento de WAL terminado a `cms ctl wal-archive`,
y un **respaldo base** más los segmentos archivados restauran la base de
datos a cualquier instante, por ejemplo al segundo anterior a que alguien
borrara la tarea equivocada.

Los segmentos van a `<directorio de respaldos>/wal` y, si los respaldos
tienen destino S3, también a su prefijo `wal/`. Archivar dos veces el mismo
segmento no hace daño; un archivo distinto con un nombre ya archivado se
rechaza (se dispara la alerta `wal_archiving`).

### Configuración (Debian/Ubuntu, PostgreSQL instalado por el instalador)

PostgreSQL ejecuta el comando de archivado como el usuario `postgres`, que
no debe leer los secretos del CMS. Dale una configuración propia con solo
lo que necesitan los comandos:

```sh
sudo install -d -o postgres -g postgres -m 700 /var/lib/postgresql/cms-archive
sudo tee /etc/postgresql/cms-wal.yaml >/dev/null <<'EOF'
# La leen los comandos de archivado y restauración de PostgreSQL (usuario postgres).
database:
  url: postgres:///postgres?host=/var/run/postgresql
backup:
  dir: /var/lib/postgresql/cms-archive
EOF
sudo chown root:postgres /etc/postgresql/cms-wal.yaml
sudo chmod 640 /etc/postgresql/cms-wal.yaml

V=$(ls /etc/postgresql | sort -n | tail -1)
sudo tee /etc/postgresql/$V/main/conf.d/cms-archive.conf >/dev/null <<'EOF'
wal_level = replica
archive_mode = on
archive_command = '/usr/local/bin/cms ctl wal-archive -config /etc/postgresql/cms-wal.yaml %p %f'
# Como mucho un minuto de trabajo perdido aunque se escriba poco.
archive_timeout = 60
EOF
sudo systemctl restart postgresql
```

Luego toma un primer respaldo base, y uno por día (un timer de systemd o
cron, como `postgres`):

```sh
sudo -u postgres cms ctl basebackup -config /etc/postgresql/cms-wal.yaml
```

Comprobación: `sudo -u postgres psql -c 'SELECT archived_count, failed_count FROM pg_stat_archiver'`
muestra segmentos archivados, y la página Sistema no muestra la alerta
`wal_archiving`. Conserva los respaldos base de los últimos días y los
segmentos más nuevos que el más viejo que conserves; los segmentos
anteriores se pueden borrar.

### Restaurar a un instante

1. Detén el CMS en todas partes: `sudo systemctl stop cms.target` en el
   servidor principal y `sudo systemctl stop cms-worker` en los workers.
2. Detén PostgreSQL y aparta los datos dañados:

   ```sh
   sudo systemctl stop postgresql
   sudo mv /var/lib/postgresql/$V/main /var/lib/postgresql/$V/main.before-pitr
   sudo -u postgres install -d -m 700 /var/lib/postgresql/$V/main
   sudo -u postgres tar -xzf /var/lib/postgresql/cms-archive/base/<el más nuevo antes del instante>/base.tar.gz \
       -C /var/lib/postgresql/$V/main
   ```

3. Dile a PostgreSQL dónde están los segmentos y dónde detenerse:

   ```sh
   sudo tee /etc/postgresql/$V/main/conf.d/cms-recovery.conf >/dev/null <<'EOF'
   restore_command = '/usr/local/bin/cms ctl wal-restore -config /etc/postgresql/cms-wal.yaml %f %p'
   recovery_target_time = '2030-07-01 11:41:59+00'
   recovery_target_action = 'pause'
   EOF
   sudo -u postgres touch /var/lib/postgresql/$V/main/recovery.signal
   sudo systemctl start postgresql
   ```

4. Mira los datos (`sudo -u postgres psql cms`). Demasiado temprano o
   tarde: detén y repite desde el paso 2 con otra hora. Correcto: termina la
   recuperación con `SELECT pg_wal_replay_resume();` y borra
   `cms-recovery.conf`.
5. Vacía las colas de trabajos y verifica la cadena de auditoría; luego
   inicia el CMS:

   ```sh
   sudo -u cms cmsctl queue-drain
   sudo -u cms cmsctl audit-verify
   sudo systemctl start cms.target      # y cms-worker en los workers
   ```

   `queue-drain` importa: la base recuperada vuelve a entregar los ids de
   envío creados después del instante, y los trabajos que aún esperaban
   para los envíos viejos caerían sobre los nuevos. El dispatcher vuelve a
   encolar todo lo que la base todavía necesita evaluar. (`cmsctl restore`
   de un respaldo lógico vacía las colas por sí mismo.)

Este procedimiento se verificó de punta a punta: archivado con `cms ctl
wal-archive`, respaldo base, recuperación con `cms ctl wal-restore`
detenida justo antes de una sentencia destructiva, datos de vuelta.

## Réplica de lectura

Las lecturas pesadas que toleran un segundo de retraso pueden ir a una
réplica de PostgreSQL por streaming: el ranking que se envía a los
servidores de ranking, las estadísticas de tareas, las exportaciones de
resultados y el informe de plagio. El sitio del concurso y todo lo que
escribe siguen usando el primario (un concursante tiene que ver su propio
envío de inmediato).

1. En la máquina de la réplica, con la misma versión de PostgreSQL, clona
   el primario (un usuario de replicación con contraseña en `pg_hba.conf`
   del primario):

   ```sh
   sudo systemctl stop postgresql
   sudo -u postgres rm -rf /var/lib/postgresql/$V/main
   sudo -u postgres pg_basebackup -h PRIMARIO -U replicator -D /var/lib/postgresql/$V/main -R -X stream -P
   sudo systemctl start postgresql
   ```

2. En el servidor principal, en `cms.yaml` (o `CMS_DATABASE_REPLICA_URL`):

   ```yaml
   database:
     url: postgres://cms:...@primario/cms
     replica_url: postgres://cms:...@replica/cms
   ```

3. Reinicia el CMS. El `/healthz` de cada servicio informa la réplica como
   una revisión propia.

Si la réplica se detiene, las páginas que leen de ella fallan hasta que
vuelva o se quite `replica_url`: la evaluación y el sitio del concurso no se
ven afectados.

## Varios servidores de concursantes

El servidor de concursantes no guarda nada propio: las sesiones son cookies
firmadas que se comprueban contra la base de datos, y los límites de
frecuencia, las notificaciones en vivo y los resultados pasan por Valkey.
Cuando una máquina no alcanza para los concursantes, ejecuta
`cms contest-web` en más máquinas con el mismo `cms.yaml` (el mismo
`secret`, la misma base de datos y Valkey, y los blobs a través del
servidor de blobs o S3, como para [un worker externo](worker-externo.md)),
y lístalas todas en el proxy:

```
contest.example.org {
    reverse_proxy 10.0.0.11:8888 10.0.0.12:8888 {
        lb_policy least_conn
        health_uri /healthz
    }
}
```

Un concursante puede caer en cualquiera de ellas, de una petición a la
siguiente. Los servidores de administración y de ranking son procesos
aparte y escalan por su cuenta (el de ranking lo alimenta el publicador;
se listan varios servidores de ranking en `dispatcher.ranking_urls`).

## Failover

Si se pierde la máquina de la base de datos primaria:

1. Promueve la réplica: `sudo -u postgres psql -c 'SELECT pg_promote()'`.
2. Apunta el CMS hacia ella: `database.url` pasa a ser la dirección de la
   réplica y se quita `replica_url`; reinicia los servicios
   (`sudo systemctl restart cms.target`; los workers no usan la base).
3. Configura el archivado en el nuevo primario (la sección de arriba) y arma
   una nueva réplica cuando vuelva la máquina vieja. Nunca vuelvas a
   iniciar el primario viejo como primario: dos primarios son dos concursos
   que divergen.

Los envíos guardados antes de la falla y aún no evaluados no se pierden:
las colas de trabajos están en Valkey y el barrido del dispatcher encola lo
que falte. Lo que el primario viejo escribió y todavía no había enviado a
la réplica (la replicación por streaming es asíncrona: normalmente mucho
menos de un segundo) se pierde; la cadena de hashes del registro de
auditoría (`cmsctl audit-verify`) muestra dónde termina la historia.

Valkey guarda las colas en disco (archivo append-only) y sobrevive a los
reinicios. Para sobrevivir a la pérdida de su máquina, ejecuta una réplica
y tres Sentinels (en tres máquinas, p. ej. el servidor principal, la
réplica de la base de datos y un worker) y apunta CMS a los Sentinels:

```
# en la máquina de la réplica, valkey.conf
replicaof 10.0.0.1 6379
masterauth <la contraseña de Valkey>
requirepass <la contraseña de Valkey>

# sentinel.conf en cada una de las tres máquinas (valkey-sentinel)
sentinel monitor cms 10.0.0.1 6379 2
sentinel auth-pass cms <la contraseña de Valkey>
sentinel down-after-milliseconds cms 5000
sentinel failover-timeout cms 30000
```

```yaml
# cms.yaml de cada servicio y worker
redis:
  url: redis://:<la contraseña de Valkey>@unused/0
  sentinels: ["10.0.0.1:26379", "10.0.0.2:26379", "10.0.0.3:26379"]
  sentinel_master: cms
```

Cuando el primario muere, los Sentinels promueven la réplica y cada
servicio la sigue por sí solo. La replicación es asíncrona, así que se
puede perder el último instante de actividad de las colas: los trabajos son
idempotentes y el barrido del dispatcher vuelve a encolar cada envío no
evaluado, así que nada se pierde del todo. Sin Sentinels, perder la
máquina de Valkey significa instalar uno nuevo y reiniciar los servicios,
con el mismo barrido.

## Ensayos

Un ensayo reproduce los envíos de un concurso pasado en un concurso de
ensayo a través del circuito real, con el ritmo original (o más rápido), y
mide cuánto tardó la evaluación:

1. Importa el concurso pasado (un archivo de concurso, ver
   [respaldos](respaldos.md)) o usa un concurso de esta instalación.
2. Clónalo desde el panel de administración (**Clonar**, con sus
   participantes) y dale al clon la ventana horaria del ensayo.
3. Reproduce:

   ```sh
   sudo -u cms cmsctl replay -from ioi2029 -to ioi2029-ensayo -speed 10
   ```

   Los concursantes se emparejan por nombre de usuario y las tareas por
   posición; cada envío lleva los mismos archivos, lenguaje y tiempo
   relativo (dividido por `-speed`). `-limit N` se detiene tras N envíos.
   Al final, con `-wait`, informa cuántos se evaluaron y la mediana, el
   percentil 95 y el máximo del tiempo entre envío y puntaje.

Mira la página Sistema y las alertas mientras corre: un ensayo a la
velocidad esperada con todos los workers es la mejor prueba de una
instalación nueva.

## Prueba de caos

`go test -run TestChaos ./internal/dispatcher` (desarrolladores, con los
servicios de `make test`) evalúa envíos mientras dispatchers y workers se
matan y reinician al azar, y comprueba que cada envío termine con el
puntaje exacto, que ninguna evaluación se pierda ni se duplique y que no
aparezcan errores de sistema. Corre en la suite normal de pruebas.

## Ver también

- [Configuración del concurso en Git](configuracion-en-git.md)
- [Aprovisionamiento con Ansible](ansible.md)
- [Manual del día del concurso](dia-del-concurso.md)
