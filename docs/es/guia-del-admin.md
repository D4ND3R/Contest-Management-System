# Guía del administrador

Un concurso desde cero en el panel de administración
(`https://admin.<tu dominio>`). Todas las páginas están en español o en
inglés (selector de idioma al pie). Las demás guías profundizan:
[configuración del concurso](configuracion-del-concurso.md),
[tipos de problema](task-types.md), [paquetes de problema](paquete-de-problema.md),
[día del concurso](dia-del-concurso.md), [respaldos](respaldos.md).

## 1. Administradores

**Administradores**: agrega a cada persona con su rol: *all* (todo),
*messaging* (preguntas, anuncios, globos, impresión; lectura del resto) o
*read only* (solo lectura). Cada administrador puede activar un segundo
factor (**Mi cuenta → Autenticación de dos factores**, cualquier app TOTP);
un administrador con rol *all* puede reiniciar el dispositivo perdido de
otro. Todo cambio queda en el **Registro de auditoría**.

## 2. El concurso

1. **Concursos → Nuevo concurso**: un nombre (letras, dígitos, `.`, `_`,
   `-`; es la dirección, `https://<dominio>/<nombre>/`), una descripción,
   el inicio y el fin en la zona horaria del concurso.
2. Estado *borrador* mientras lo preparas (los concursantes no lo ven, los
   administradores sí); *publicado* cuando está listo; *archivado* al
   terminar (solo lectura, fuera de las listas).
3. El resto, sección por sección —lenguajes, ventanas de tiempo por
   concursante, práctica, IOI o ICPC, equipos, tokens, retroalimentación,
   visibilidad de puntajes, acceso y registro, límites de envíos, preguntas,
   impresión— está en la página **Configuración** del concurso y se
   describe en [configuración del concurso](configuracion-del-concurso.md).
   Cada campo muestra su significado al lado.
4. **Presentación** (Configuración): un título visible para todos, la
   descripción como subtítulo, el lugar y un lema forman el **banner** que
   encabeza los paneles de concursantes y administradores (y la página de
   ingreso). **Imagen del banner**: un PNG, JPEG, GIF o WebP ancho (unos
   1600×400, hasta 4 MiB); SVG se rechaza porque puede llevar scripts. Sin
   imagen el banner usa un fondo liso.
5. **Sedes** (opcional): lugares con su propia hora de inicio; sirven para
   filtrar el ranking y los globos.
6. Para reutilizar el concurso del año anterior: **Copiar este concurso** al
   final de su página de Configuración (problemas y configuración,
   opcionalmente los participantes), o importa su
   [archivo](respaldos.md#archivos-de-un-concurso).

El menú de la izquierda sigue al concurso que estás viendo (la página de
inicio muestra el que está en curso, o el siguiente): su **panel**,
problemas, envíos, clasificación, estadísticas, avisos, participantes y
configuración; la barra superior muestra su fase y el tiempo restante.

## 3. Concursantes

- **Usuarios → Importar CSV**: columnas `username, password, first_name,
  last_name, email, institution, country, region, timezone, team` (código),
  `site, ip, hidden, unrestricted, delay_time, extra_time` (fila de
  encabezado obligatoria, cualquier subconjunto y orden). La vista previa
  muestra cada fila con sus errores antes de crear nada; marca *y agregar a*
  para inscribirlos en el concurso de una vez. *Generar las contraseñas
  faltantes* completa las vacías.
- **Participaciones** del concurso: ajustes por concursante (contraseña,
  IP permitidas, tiempo extra o retraso, oculto, sin restricciones),
  acciones masivas, aprobación de registros y **Generar e imprimir** la hoja
  de credenciales (PDF, 1 a 8 por página, con la dirección del concurso).
- **Equipos**: código, nombre, bandera e institución; en concursos por
  equipos los integrantes comparten envíos y límites.

## 4. Problemas

Lo más rápido es un **paquete de problema**: **Problemas → Importar un
paquete**, suelta el zip, revisa la vista previa (tipo, límites, subtareas,
casos de prueba, enunciados, soluciones de referencia) y confirma. Las
soluciones de referencia se evalúan enseguida y el reporte de validación
dice si cada una obtiene el veredicto que anuncia su nombre. Los paquetes de
CMS (italy_yaml) y de Polygon se convierten. El formato y un ejemplo por
tipo están en [paquetes de problema](paquete-de-problema.md).

A mano, **Problemas → Crear problema** y después, en la página del
problema:

1. **General**: título, enunciados (escritos en Markdown o LaTeX en el
   editor, o subidos; uno o más marcados como principales, ver
   [enunciados](enunciados.md)), ejemplos, adjuntos (archivos que descargan
   los concursantes), archivos
   del envío (`sol.%l`: `%l` se reemplaza por la extensión del lenguaje),
   lenguajes (ninguno marcado = los del concurso).
2. **Puntuación y retroalimentación**: modo de puntuación (mejor por
   subtarea, como en la IOI desde 2017; mejor envío; o máximo entre los
   envíos con token y el último), precisión, retroalimentación (completa, o
   solo el primer fallo por subtarea).
3. **Datasets → Nuevo dataset** (copia uno existente o empieza vacío). En la
   página del dataset:
   - **Tipo de problema** y sus opciones (abajo), **Límites** (tiempo,
     memoria, salida, tamaño del fuente).
   - **Tipo de puntuación**: *Sum* (puntos por caso), *GroupMin* (una
     subtarea puntúa solo si pasan todos sus casos), *GroupMul*,
     *GroupThreshold*; el editor de subtareas pide los puntos y los casos de
     cada subtarea (una expresión regular sobre los nombres, una cantidad o
     una lista).
   - **Casos de prueba**: uno por uno (entrada, salida, público) o **Desde un
     archivo zip** con patrones de nombre (`*.in`/`*.out`,
     `input*`/`output*`).
   - **Managers**: checker, graders, stubs, headers, interactor; la página
     indica los archivos que todavía necesita la configuración elegida
     (*Faltan managers para esta configuración*).
   - **Poner en vivo** cuando esté correcto: los envíos se puntúan con el
     dataset en vivo; un segundo dataset puede evaluar los envíos nuevos en
     segundo plano para comparar antes de cambiar (*autoevaluación*).
4. **Probador de problemas**: envía cualquier fuente como administrador y ve
   el veredicto por caso sin que cuente en ningún lado; el **Reporte de
   validación** evalúa las soluciones de referencia en cada dataset.
5. Agrega el problema al concurso (la página **Problemas** del concurso
   lista sus problemas en orden).

### Cada tipo de problema, paso a paso

| Tipo | Tipo de problema y opciones | Managers a subir |
|------|-----------------------------|------------------|
| Entrada/salida estándar | *Batch*, compilación *solo*, comparación *ignorar espacios* (o *exacta*, *reales con tolerancia* con tolerancia absoluta/relativa) | ninguno |
| Checker propio | *Batch*, comparación *checker propio (protocolo CMS)* o *(testlib)* | `checker` (binario), `checker.cpp` o `checker.c`; `testlib.h` para testlib |
| Archivos en lugar de stdin/stdout | *Batch*, **Archivo de entrada** / **Archivo de salida** (p. ej. `input.txt`, `output.txt`) | ninguno |
| Función a implementar (grader) | *Batch*, compilación *grader* | `grader.c`, `grader.cpp`, `grader.java`, `grader.py`… uno por lenguaje, más los headers (`task.h`) |
| Solo salida | *Solo salida*; opcionalmente *Las salidas faltantes toman el mejor resultado anterior de cada caso*; nombres `output_%s.txt` | ninguno (los concursantes suben un archivo por caso o un zip) |
| Dos pasos | *Dos pasos* | `manager.cpp` (o `.c`, binario) |
| Comunicación (manager + N procesos) | *Comunicación*: procesos, E/S del concursante (*rutas de FIFO como argumentos* o E/S estándar), límites por proceso o en total, compilación *stub* | `manager` (binario, `.c`, `.cpp`), stubs `stub.c`, `stub.cpp`, `stub.py`… |
| Interactivo (estilo ICPC) | *Interactiva*: límites de tiempo y memoria del interactor | `interactor` (binario, `.c`, `.cpp`; `testlib.h` si usa testlib) |

Los protocolos (argumentos, códigos de salida, qué imprime el checker o el
interactor) están en [tipos de problema](task-types.md). Antes del
concurso evalúa siempre una solución correcta y una incorrecta con el
probador (o con soluciones de referencia).

## 5. Antes del concurso

Sigue el [día del concurso](dia-del-concurso.md): verifica la máquina de
evaluación, toma un respaldo y restáuralo, ensaya con un concurso corto
([simulacro](simulacro.md)), comprueba que el reporte de validación de cada
problema esté en verde.

## 6. Durante el concurso

- El **panel** del concurso (su página, y la de inicio mientras está en
  curso) se actualiza solo cada 20 segundos: lo alto de la clasificación, el
  estado de cada problema (resuelto por alguien, solo puntos parciales, sin
  resolver; envíos y quién lo resolvió primero), los últimos eventos
  (envíos, primeras soluciones, preguntas, avisos), una gráfica de envíos y
  aceptados en el tiempo, los conteos por veredicto, el estado de jueces,
  cola, base de datos, discos y respaldos, y lo que requiere atención
  (preguntas sin responder, envíos que no se pudieron evaluar, trabajos
  atascados, registros por aprobar, el final cerca, una clasificación
  congelada).
- **Workers y colas**: colas, trabajos en curso, trabajos atascados
  (reencolar), errores del sistema, CPU/memoria/disco.
- **Preguntas** y **Comunicación** (anuncios, mensajes privados); las
  respuestas pueden ser públicas.
- **Envíos**: filtros (problema, usuario, veredicto, lenguaje, puntaje,
  fechas), código con resaltado, diferencias entre dos envíos, descarga en
  zip; **reevaluar** un envío, un usuario, un problema o el concurso
  (recompilar, reevaluar o solo recalcular puntajes); **invalidar** un
  envío con un motivo (se puede restaurar).
- Los **envíos sospechosos** (código que ejecuta programas, abre sockets,
  hace llamadas directas al sistema..., o programas que el filtro seccomp
  mató) quedan marcados: una etiqueta y un filtro en los envíos, una
  notificación en el panel; ver [seguridad](seguridad.md).
- Página de la **participación**: tiempo extra, ajuste manual de puntaje con
  motivo (auditado), sesiones, "ver como el concursante".
- **Extender el concurso** para todos desde su página de Configuración;
  **Globos** e **Impresión** para concursos ICPC y presenciales.

## 7. Después del concurso

- **Ranking**: descongélalo después de la ceremonia; exporta CSV, JSON o el
  PDF para imprimir.
- **Estadísticas** por problema; reporte de **Plagio** por problema con
  vista lado a lado.
- **Certificados**: diseña la plantilla, descárgalos todos y permite que
  los concursantes descarguen el suyo
  ([configuración del concurso](configuracion-del-concurso.md#certificados)).
- **Archiva** el concurso (un zip que se puede importar después) y toma un
  respaldo final ([respaldos](respaldos.md)).
