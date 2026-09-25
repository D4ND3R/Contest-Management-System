# Respaldos y restauración

Un respaldo del CMS es **un único archivo** con toda la base de datos y
todos los archivos almacenados (enunciados, adjuntos, casos de prueba,
managers, envíos, salidas). Cada parte lleva su hash y una restauración solo
se confirma si todos coinciden.

## Qué contiene

El archivo (`cms-backup-<hora UTC>-<tipo>.tar.zst`) es un tar comprimido con
zstd:

| Miembro | Contenido |
|---------|-----------|
| `cms-backup.json` | versión del formato, hora, versión del CMS, migraciones aplicadas, tablas y columnas |
| `db/<tabla>/<n>` | las filas de la tabla (formato texto de `COPY` de PostgreSQL, en trozos de 8 MiB) |
| `db/sequences.json` | las secuencias de ids |
| `blobs/<sha256>` | cada archivo almacenado, con su SHA-256 como nombre |
| `manifest.json` | tamaño y SHA-256 de cada miembro, filas por tabla, cantidad de archivos |

La parte de la base de datos sale de una única instantánea consistente; los
archivos son inmutables (direccionados por contenido), así que se copian
después de liberar la instantánea y nunca bloquean la base. Junto a cada
respaldo un pequeño `.json` registra quién lo tomó, cuánto tardó, su tamaño
y el SHA-256 del archivo completo.

## Respaldos programados

Los toma el servidor web de administración (uno a la vez aunque haya varios
servidores de administración). En el archivo de configuración:

```yaml
backup:
  dir: /var/lib/cms/backups
  interval: 24h           # sin concurso en curso (0 = desactivado)
  contest_interval: 15m   # desde 30 min antes del inicio hasta 30 min después del fin
  keep: 48                # respaldos programados que se conservan; los más viejos se borran
  max_rate: 32MiB         # límite de lectura por segundo (0 = sin límite)
  s3:                     # copia externa opcional de cada respaldo
    endpoint: s3.example.org
    bucket: cms-backups
    access_key: ...
    secret_key: ...
    use_ssl: true
    prefix: backups/
```

`max_rate` mantiene rápido el servidor web del concurso en una máquina
chica: un respaldo lee como máximo esa cantidad de bytes por segundo de
PostgreSQL y del almacén de archivos. Con el valor por defecto (32 MiB/s)
una instalación de 1 GiB se respalda en alrededor de medio minuto.

Un respaldo fallido (o una copia a S3 fallida) aparece en el panel de
administración como alerta del sistema y en los logs; la métrica
`cms_backup_last_success_timestamp_seconds` permite que tu monitoreo avise
si los respaldos se detienen.

## Desde el panel de administración

**Respaldos** (menú superior) muestra la programación, el respaldo en curso
y cada respaldo con su tamaño, duración, estado de S3 y SHA-256. Los
administradores completos pueden tomar uno en el momento (**Respaldar
ahora**), descargarlo y eliminarlo. Todas estas acciones quedan en el
registro de auditoría (un respaldo contiene los hashes de las contraseñas:
guarda los archivos en un lugar seguro).

## Desde la línea de comandos

```sh
cmsctl dump                      # en backup.dir, aparece en el panel
cmsctl dump -o /mnt/usb/cms.tar.zst
cmsctl dump -o - | ssh host-respaldo 'cat > cms.tar.zst'
cmsctl backups                   # lista backup.dir
cmsctl backup-verify ARCHIVO     # comprueba cada hash (y el del archivo completo si el .json está al lado)
cmsctl restore ARCHIVO           # en una base vacía
cmsctl restore -force ARCHIVO    # reemplaza una base que ya tiene datos
```

### Restaurar

1. Detén todos los servicios del CMS (`systemctl stop 'cms-*'`); el
   servidor web del ranking puede seguir funcionando.
2. Apunta la configuración a la base de datos y al almacén de destino (en
   una máquina nueva: instala primero el CMS, ver la guía de despliegue).
3. `cmsctl restore ARCHIVO`. El comando
   - rechaza una base que ya tiene datos salvo con `-force` (entonces
     primero se borra todo su contenido);
   - aplica las migraciones con las que se tomó el respaldo, carga todas las
     tablas en una sola transacción, guarda cada archivo (comprobando su
     SHA-256), verifica el manifiesto, restaura las secuencias y las claves
     foráneas y recién entonces confirma;
   - aplica las migraciones más nuevas que el respaldo (un respaldo tomado
     con una versión anterior del CMS se restaura en una más nueva).
4. Vuelve a iniciar los servicios.

Un archivo dañado o truncado se rechaza antes de confirmar nada. Restaurar
sobre un esquema más nuevo que el del respaldo requiere `-force`; un
respaldo de una versión del CMS más nueva que la instalada se rechaza.

### Simulacro

Antes de un concurso, restaura el último respaldo en una base de prueba
para asegurarte de que funciona:

```sh
createdb cms_simulacro
CMS_DATABASE_URL=postgres://cms@localhost/cms_simulacro CMS_BLOB_DIR=/tmp/simulacro-blobs \
  cmsctl restore /var/lib/cms/backups/<último>.tar.zst
dropdb cms_simulacro; rm -rf /tmp/simulacro-blobs
```
