# Aprovisionamiento con Ansible

[`deploy/ansible`](../../deploy/ansible/site.yml) aprovisiona una instalación
completa a partir de un inventario: el servidor principal, los workers de
evaluación y, opcionalmente, los concursos guardados en Git. Una ejecución
con los mismos archivos instala lo mismo, así que el repositorio que guarda
el inventario es el registro de qué corre dónde.

El playbook ejecuta en cada máquina el instalador de una versión fija
(`scripts/install.sh`, ver la [guía de despliegue](despliegue.md)). El
instalador verifica los checksums de la versión, revisa la máquina y es
idempotente; el playbook agrega el orden (primero el servidor principal,
luego los workers, de a un cuarto), los secretos que necesitan los workers,
las actualizaciones y la configuración de los concursos.

## Uso

En la máquina de control (Ansible 2.14 o más nuevo), con acceso SSH como un
usuario que puede usar `sudo` en cada máquina:

```sh
cp -r deploy/ansible ~/cms-infra && cd ~/cms-infra
cp inventory.example.ini inventory.ini    # editar: máquinas y dirección privada del servidor principal
$EDITOR group_vars/all.yml                # versión, dominio, lenguajes...
ansible-playbook site.yml
```

`inventory.ini`:

```ini
[cms_main]
cms-main.example.org cms_private_ip=10.8.0.1

[cms_workers]
judge-01 ansible_host=10.8.0.11
judge-02 ansible_host=10.8.0.12
```

`cms_private_ip` es la dirección del servidor principal en la red que
comparte con los workers (ahí escuchan Valkey y el servidor de blobs; ver
[workers externos](worker-externo.md)).

Los ajustes de `group_vars/all.yml`:

| Variable | Significado |
| --- | --- |
| `cms_version` | la versión, `X.Y.Z` (nunca `latest`: repetir la ejecución debe instalar lo mismo) |
| `cms_domain`, `cms_email` | HTTPS con Let's Encrypt; vacío: HTTP en la red local |
| `cms_admin_allow` | solo esta red (CIDR) llega al sitio de administración |
| `cms_languages` | compiladores `minimal` o `full` |
| `cms_tune_host`, `cms_worker_tune_host` | governor de rendimiento, sin turbo, sin transparent huge pages |
| `cms_worker_batch` | workers que se reinstalan a la vez (por defecto 25%) |
| `cms_install_extra_args`, `cms_worker_extra_args` | más opciones del instalador, como listas |
| `cms_contest_configs` | [directorios de concursos](configuracion-en-git.md) a aplicar |
| `cms_contest_activate` | activar los datasets nuevos de tareas cambiadas aunque el concurso ya haya empezado |

## La contraseña del administrador

La primera instalación muestra la contraseña del administrador una sola
vez. El playbook oculta la salida del instalador (`no_log`) para que nunca
quede en la salida ni en los registros de Ansible; define la contraseña
después, en el servidor principal:

```sh
sudo -u cms cmsctl admin-password
```

Los secretos que necesitan los workers (la contraseña de Valkey y el token
de blobs) se leen del `/etc/cms/secrets.env` del servidor principal y se
pasan al instalador de los workers en tareas que tampoco se registran.

## Actualizar

Cambia `cms_version` y vuelve a ejecutar el playbook. El servidor principal
pasa por `cmsctl upgrade` (primero un respaldo, migraciones, vuelta atrás
automática si algo falla); los workers no guardan datos y se reinstalan de
a tandas, así la evaluación nunca se detiene. No actualices durante un
concurso.

## Lo que el playbook no hace

- La réplica de la base, el archivado continuo y el failover se configuran
  a mano (ver [operación](operaciones.md)): dependen de tus máquinas y se
  hacen una vez.
- Las tareas del instalador siempre informan "changed": el instalador no
  dice si cambió algo, aunque repetirlo no cambia nada.
