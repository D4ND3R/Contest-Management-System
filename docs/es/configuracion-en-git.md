# Configuración del concurso en Git

La configuración de un concurso puede vivir en un repositorio Git y
aplicarse a una instalación de forma determinista: el mismo directorio da
siempre el mismo concurso, y aplicarlo dos veces no cambia nada la segunda.
Los autores de tareas revisan los cambios como diffs, y el concurso del
ensayo es exactamente el concurso del día real.

## El directorio

```
ioi2030/
├── contest.yaml
└── tasks/
    ├── sum/          un paquete de problema (ver paquete-de-problema.md)
    │   ├── problem.yaml
    │   ├── statement/es.md
    │   └── tests/...
    └── tree/
        └── ...
```

`contest.yaml`:

```yaml
format: 1
name: ioi2030
settings:
  title: Olimpíada Internacional de Informática 2030
  start_time: "2030-07-01T09:00:00Z"
  stop_time: "2030-07-01T14:00:00Z"
  languages: [C++17 / g++, Python 3]
  max_submission_number: 50
  score_visibility: full
  # ... cualquier ajuste del concurso, con los nombres que escribe `export`
tasks: [sum, tree]
```

`settings` acepta los ajustes del concurso del panel de administración.
Los que no aparecen conservan su valor (un concurso nuevo recibe los
valores por defecto). Algunos se rechazan a propósito:

- `invitation_code`: es un secreto, no va en un repositorio;
- `status`, `submissions_paused`, `pause_message`, `ranking_unfrozen`:
  estado operativo, se cambia desde el panel durante el concurso;
- la zona horaria: es la del servidor (página **Servidor**).

Los concursantes no forman parte de la configuración: sus credenciales no
deben estar en un repositorio. Impórtalos desde CSV en el panel.

Cada directorio bajo `tasks/` es un paquete de problema en el formato de
este sistema (o italy_yaml / Polygon, que se convierten); su `name` debe
ser el nombre del directorio. `tasks` da el orden de las tareas en el
concurso.

## Partir de un concurso existente

```sh
sudo -u cms cmsctl contest-config export -contest ioi2030 /srv/ioi2030
cd /srv/ioi2030 && git init && git add . && git commit -m "ioi2030 tal como está configurado"
```

`export` escribe `contest.yaml` y `tasks/` (el dataset activo de cada
tarea, con sus enunciados, adjuntos, ejemplos y soluciones de referencia) y
no toca los demás archivos del directorio (`.git`, un README). Es
determinista: exportar dos veces da archivos idénticos.

## Aplicar

```sh
sudo -u cms cmsctl contest-config apply -dry-run /srv/ioi2030   # qué cambiaría
sudo -u cms cmsctl contest-config apply /srv/ioi2030
```

Primero se leen y revisan todos los paquetes: un error en cualquier lugar y
no se aplica nada. Luego:

- se crea el concurso, o se actualizan los ajustes que difieren (la salida
  los nombra);
- una tarea nueva se importa al concurso;
- una tarea cuyo paquete cambió recibe un **dataset nuevo**, y su título,
  enunciados, adjuntos, ejemplos y ajustes de `problem.yaml` siguen al
  paquete (los enunciados quitados del paquete se quitan). Antes de que
  empiece el concurso el dataset nuevo pasa a ser el activo. Una vez
  empezado, el dataset nuevo espera: revísalo en el panel (activa su
  evaluación en segundo plano y **Comparar con el dataset activo** muestra
  cómo cambiaría cada puntaje), actívalo allí, o vuelve a aplicar con
  `-activate`;
- las tareas se numeran en el orden de `tasks`. Una tarea del concurso que
  el archivo no lista se deja como está y se informa (quitar una tarea se
  hace desde el panel).

Si una tarea cambió se decide por su contenido: el dataset que crea
`apply` lleva en su descripción el SHA-256 del directorio del paquete
(`Default @3f2a9c1b7d4e`), y un directorio escrito por `export` se reconoce
como idéntico a lo que está activo. Se ignoran los archivos ocultos
(`.git`, `.gitignore`) y los restos de editores.

## Con Ansible

El [playbook de Ansible](ansible.md) copia al servidor principal los
directorios listados en `cms_contest_configs` y los aplica, así que toda la
instalación, concurso incluido, sale de un solo repositorio.
