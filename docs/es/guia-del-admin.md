# Guía del administrador

Un concurso desde cero en el panel de administración
(`https://admin.<tu dominio>`). Si nunca usaste el sistema, empieza por
[primeros pasos](primeros-pasos.md), que recorre lo mismo con más detalle.
Las demás guías profundizan: [crear un problema](crear-un-problema.md),
[configuración del concurso](configuracion-del-concurso.md),
[tipos de problema](task-types.md), [paquetes de problema](paquete-de-problema.md),
[clarificaciones](clarificaciones.md), [día del concurso](dia-del-concurso.md),
[respaldos](respaldos.md). Las palabras técnicas están en el
[glosario](glosario.md).

## 0. Cómo está organizado el panel

- **Arriba**: el concurso que estás viendo, su fase y el tiempo restante;
  a la derecha **Preguntas** (con el número de pendientes), tu cuenta y
  **Cerrar sesión**.
- **Primera fila del menú**, todo el servidor: Panel, Concursos, Banco de
  problemas, Usuarios, Equipos, Clarificaciones, Jueces, Lenguajes,
  Respaldos, Administradores, Registro de auditoría, Servidor.
- **Segunda fila**, el concurso actual (el que estás viendo; en la página de
  inicio, el que está en curso o el siguiente): su panel, Problemas,
  Participantes, Envíos, Clasificación, Anuncios, Estadísticas, Apelaciones,
  Impresión y Globos (si aplican), Plagio, Certificados, Sedes,
  Configuración.
- **Pie**: idioma, tema y tamaño del texto (se aplican al instante), y la
  zona horaria en la que se muestran las horas.

## 1. Administradores

**Administradores**: agrega a cada persona con su rol: *all* (todo),
*messaging* (preguntas, anuncios, globos, impresión; lectura del resto),
*task setter* (prepara problemas: enunciados, datasets, casos, graders, el
probador; poner un dataset en vivo, reevaluar y borrar quedan para *all*),
*read only* (solo lectura) o *leader* para líderes de delegación: vinculado
a un equipo, ve solo los envíos, el código y los resultados que ven sus
concursantes (**Mi delegación**), y no puede enviar. Cada administrador puede activar un segundo
factor (**Mi cuenta → Autenticación de dos factores**, cualquier app TOTP);
un administrador con rol *all* puede reiniciar el dispositivo perdido de
otro. Todo cambio queda en el **Registro de auditoría**.

## 2. El concurso

0. **Servidor → Zona horaria**: la zona de todas las horas de todos los
   sitios (y de las que escribes en los formularios). Se puede cambiar en
   cualquier momento.
1. **Concursos → Nuevo concurso**: un nombre (letras, dígitos, `.`, `_`,
   `-`; es la dirección, `https://<dominio>/<nombre>/`), un título, una
   descripción, el inicio y el fin.
2. Estado *borrador* mientras lo preparas (los concursantes no lo ven, los
   administradores sí); *publicado* cuando está listo; *archivado* al
   terminar (solo lectura, fuera de las listas).
3. El formulario tiene tres partes a la vista —**General**, **Envíos** (la
   espera entre envíos, 20 s por omisión; el máximo de envíos; qué puntajes
   ven los concursantes) y **Clasificación** (quién la ve, cuándo, el
   congelamiento, las columnas)— y el resto plegado en **Todos los demás
   ajustes**: lenguajes, ventanas por concursante, práctica, apelaciones,
   acceso y registro, equipos, tokens, impresión. Todo se describe en
   [configuración del concurso](configuracion-del-concurso.md).
4. **Sedes** (opcional): lugares con su propia hora de inicio; sirven para
   filtrar el ranking y los globos.
5. Para reutilizar el concurso del año anterior: **Copiar este concurso** al
   final de su página de Configuración (problemas y configuración,
   opcionalmente los participantes), o importa su
   [archivo](respaldos.md#archivos-de-un-concurso).

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

Un problema se arma por partes, en cualquier orden:
**Problemas → Nuevo problema** con solo un nombre, y después, en su página,
la lista **Preparación** dice qué falta (enunciado, casos, checker,
puntuación, una solución de referencia con el puntaje completo). Los casos
se agregan **uno por uno** (entrada escrita o subida; salida escrita,
subida, vacía o escrita por la solución de referencia) o con un
**generador** que se ejecuta una vez por línea de parámetros. Todo esto se
explica paso a paso en [crear un problema](crear-un-problema.md).

También se puede importar un **paquete de problema**: **Banco de problemas →
Importar un paquete de problema**, suelta el zip, revisa la vista previa
(tipo, límites, subtareas, casos, enunciados, soluciones de referencia) y
confirma. Las soluciones de referencia se evalúan enseguida y el reporte de
validación dice si cada una obtiene el veredicto que anuncia su nombre. Los
paquetes de CMS (italy_yaml) y de Polygon se convierten. El formato y un
ejemplo por tipo están en [paquetes de problema](paquete-de-problema.md).

Lo que hay en la página del problema y en la de cada dataset:

1. **Configuración** del problema: título, archivos del envío (`sol.%l`:
   `%l` se reemplaza por la extensión del lenguaje), lenguajes (ninguno
   marcado = los del concurso), modo de puntuación (mejor por subtarea,
   como en la IOI desde 2017; mejor envío; máximo entre los envíos con
   token y el último; o el último envío que compiló), precisión,
   retroalimentación, y si los concursantes ven los mensajes propios del
   checker o solo el mensaje estándar de cada resultado.
2. **Enunciados**, **Ejemplos** y **Adjuntos** ([enunciados](enunciados.md)).
3. **Datasets**: el que está *en vivo* puntúa los envíos. En su página:
   - **Tipo, límites y checker**: tipo de problema y sus opciones (abajo),
     tiempo, memoria, salida, tamaño del fuente.
   - **Puntuación y subtareas**: *Sum* (puntos por caso), *GroupMin* (una
     subtarea puntúa solo si pasan todos sus casos), *GroupMul*,
     *GroupThreshold*; el editor de subtareas pide los puntos y los casos de
     cada subtarea (una expresión regular sobre los nombres, una cantidad o
     una lista). Con GroupMin/GroupMul, **Cortocircuito** omite el resto de
     una subtarea en cuanto un caso obtiene 0 (ver [evaluación](evaluacion.md)).
   - **Managers**: checker, graders, stubs, headers, interactor; la página
     indica los que todavía faltan.
   - **Agregar casos** y **Casos de prueba**.
   - **Poner en vivo** un dataset nuevo cuando esté correcto; un segundo
     dataset puede evaluar los envíos nuevos en segundo plano para comparar
     antes de cambiar; **comparar con el vivo** lista los puntajes que
     cambiarían.
4. **Probador de problemas**: envía cualquier fuente como administrador y ve
   el veredicto por caso sin que cuente en ningún lado; el **Reporte de
   validación** evalúa las soluciones de referencia en cada dataset.

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
  curso) se actualiza solo cada 20 segundos: arriba lo que requiere
  atención (preguntas sin responder, envíos que no se pudieron evaluar,
  trabajos atascados, registros por aprobar, el final cerca, una
  clasificación congelada); después, en tablas, cada problema (quién lo
  resolvió, parciales, envíos), los conteos por veredicto, el estado de
  jueces, cola, base de datos, discos y respaldos, lo alto de la
  clasificación y los últimos eventos.
- **Jueces**: colas, trabajos en curso, trabajos atascados (reencolar),
  errores del sistema, CPU/memoria/disco.
- **Clarificaciones** y **Anuncios**: responder a una persona o a todos,
  avisos generales y mensajes privados ([clarificaciones](clarificaciones.md)).
  Los concursantes los reciben al instante.
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
- **Extender el concurso** o **pausar los envíos** desde su página de
  Configuración; **cerrar envíos** de un solo problema en Problemas;
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
