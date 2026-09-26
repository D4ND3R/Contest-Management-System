# Configuración del concurso

Todo lo que sigue está en la página **Configuración** del concurso del sitio de
administración (**Concursos → el concurso → Configuración**, también en el menú
de la izquierda). Los cambios se aplican al instante: las
páginas de los concursantes los toman en menos de un segundo, sin reiniciar
nada.

## Ciclo de vida

- **Estado**: un concurso en *borrador* no existe para los concursantes (sus
  páginas responden 404; los administradores pueden verlo con *ver como
  concursante*). *Publicado* es el estado normal. Un concurso *archivado* es
  de solo lectura: los concursantes pueden entrar y ver sus envíos, pero no
  enviar, probar ni preguntar, y el concurso no aparece en la lista.
- **Copiar este concurso** crea un borrador con la misma configuración, sedes
  y problemas (enunciados, adjuntos, datasets, casos de prueba), opcionalmente
  con los participantes; nunca con envíos.
- **Extender el concurso** mueve el final para todos a la vez; las páginas
  abiertas actualizan su reloj. Para un solo concursante usa *tiempo extra* en
  su participación.
- **Práctica después del concurso**: al terminar, los concursantes siguen
  enviando; esos envíos quedan marcados como *no oficiales* y nunca cuentan en
  el ranking.

## Acceso

| Opción | Efecto |
|--------|--------|
| Inicio de sesión con contraseña | Los concursantes entran con usuario y contraseña. |
| Restringir por IP | Cada participación solo funciona desde sus direcciones o subredes. |
| Inicio de sesión automático por IP | Quien tiene una sola IP en su participación entra sin contraseña desde ella. |
| Bloquear sesiones simultáneas | Un nuevo inicio de sesión cierra la sesión anterior. |
| Bloquear usuarios ocultos | Las participaciones ocultas no pueden entrar. |
| Cuentas | *las crean los organizadores* (por defecto); *registro propio, aprobado por un administrador*; *registro propio con código de invitación*. |
| Código de invitación | Necesario en el modo con código (6 a 100 caracteres). |
| Longitud mínima de la contraseña | De 4 a 128 (por defecto 8); se aplica a las contraseñas que eligen los concursantes. |
| Duración de la sesión (minutos) | La sesión termina ese tiempo después de entrar (vacío: 24 horas). |

### Registro propio

Con un modo de registro propio, la página de inicio de sesión muestra
**Registrarse**. El formulario pide un usuario (3 a 32 letras, dígitos, `.`,
`_`, `-`), nombre, correo e institución opcionales y una contraseña con al
menos la longitud mínima, con letras y dígitos, distinta del usuario.

- **Con aprobación**: la cuenta se crea pero no puede entrar hasta que un
  administrador la apruebe. El panel y la configuración del concurso muestran cuántos registros
  esperan; **Participaciones** los marca como *esperando aprobación* con los
  botones **aprobar** / **rechazar**. Rechazar borra la participación (la
  cuenta queda, sin este concurso). Los registros pendientes no aparecen en el
  ranking.
- **Con código de invitación**: un código correcto admite al concursante al
  instante y abre su sesión. Cambia el código para cerrar nuevos registros.

El registro está abierto mientras el concurso esté publicado y no haya
terminado (o mientras haya práctica). Un usuario que ya existe no puede
registrarse de nuevo: los organizadores lo agregan al concurso.

## Resultados y retroalimentación

- **Puntajes visibles para los concursantes**: en cuanto se conocen, solo al
  terminar o nunca. Ocultar los puntajes también oculta el detalle de los
  casos de prueba. El ranking tiene su propia visibilidad
  ([rankings](ranking.md)): ocúltalo también si revelaría los puntajes.
- **Mostrar los mensajes del compilador**, tanto de compilaciones fallidas
  como exitosas.
- **Tokens** (reglas del concurso y del problema): con un token el
  concursante ve el resultado completo de un envío durante el concurso. La
  página del concurso muestra los tokens disponibles y cuándo llega el
  siguiente.

## Concursos ICPC

Con el modo de puntuación **ICPC (resueltos + penalización)**:

- Un envío es *aceptado* cuando obtiene el puntaje completo del problema.
  Los concursantes solo ven un veredicto: **Aceptado**, **Respuesta
  incorrecta**, **Tiempo límite excedido**, **Memoria excedida**, **Error en
  tiempo de ejecución**, **Límite de salida excedido** (decide el primer caso
  de prueba que falló) o **Error de compilación**; nunca puntajes ni el
  detalle de los casos. Su resumen muestra los problemas aceptados y los
  intentos rechazados.
- El ranking ordena por problemas resueltos y luego por penalización: los
  minutos desde el inicio hasta cada envío aceptado más la *penalización
  ICPC por intento rechazado* (los errores de compilación no cuentan).
  Congelamiento y descongelamiento como en [rankings](ranking.md).
- **Globos** (en el menú del concurso): la lista para el staff de
  cada problema resuelto por un equipo, del más antiguo al más reciente, con
  la sede y quién lo resolvió; se marca la primera solución de cada
  problema. Se actualiza sola; el botón *entregado* pasa el globo a la lista
  de entregados (con quién y cuándo; *deshacer* lo devuelve). Filtra por
  sede para repartir sala por sala. El staff con rol de *mensajería* puede
  marcar entregas; las participaciones ocultas no reciben globos.

## Impresión

Con **Impresión** marcada (Acceso y funciones), los concursantes tienen una
página de *Impresión* durante el concurso (no en práctica ni en análisis):
envían un PDF o un archivo de texto plano (UTF-8, normalmente su código
fuente, que se imprime en letra monoespaciada con números de línea y un
encabezado con su usuario, el nombre del archivo y los números de página).
Las páginas se cuentan al enviar el archivo y los límites se aplican al
instante:

| Opción | Efecto |
|--------|--------|
| Máx. trabajos de impresión por usuario | Trabajos que puede enviar un concursante (los fallidos o cancelados no cuentan). |
| Máx. páginas por trabajo | Los documentos más largos se rechazan. |
| Máx. páginas por concursante | Páginas en total (vacío: sin límite). |

Las participaciones sin restricciones no tienen límites. El menú del
concurso enlaza a la cola de **Impresión** del staff: *impresos, por
entregar* (con **entregado** y **reimprimir** para una copia perdida),
*esperando a la impresora* (con **cancelar**), *no impresos* (con el motivo e
**imprimir de nuevo**) y *entregados* (con quién y cuándo, **deshacer**);
cada documento se puede abrir como PDF. Se actualiza sola y se filtra por
sede. La página del concursante sigue el estado de cada trabajo en vivo.
Configurar la impresora: [despliegue](despliegue.md#impresión).

## Envíos y pruebas

- **Tamaño máximo de un archivo enviado** (vacío: el valor del servidor,
  `contest_web.max_submission_bytes`).
- Cantidad máxima de envíos e intervalo mínimo entre ellos, por concurso y por
  problema. Las **pruebas de usuario** (ejecutar un código con una entrada
  propia) tienen sus propios límites de cantidad e intervalo.

## Certificados

**Certificados**, en el menú del concurso, diseña un certificado por
concursante (A4 horizontal) a partir del ranking final, sin congelar:

- un **título**, un **texto** y un **pie** con variables: `{name}`,
  `{first_name}`, `{last_name}`, `{username}`, `{institution}`, `{team}`,
  `{site}`, `{contest}` (la descripción del concurso, o su nombre),
  `{rank}`, `{participants}`, `{score}` (problemas resueltos en concursos
  ICPC), `{max_score}`, `{award}` y `{date}` (el campo *Fecha*: una fecha, o
  un lugar y una fecha). Los párrafos se separan con líneas en blanco; uno
  que empieza con `#` se imprime grande y en negrita, uno con `##` en
  negrita, y un párrafo que sus variables dejan vacío (`## {award}` sin
  premio) se omite;
- **premios** por puesto, uno por línea, `Nombre: último puesto`, con
  puestos crecientes (`Medalla de oro: 3`, `Medalla de plata: 8`, `Medalla
  de bronce: 15`, `Mención honorífica: 25`); los empates comparten puesto y
  premio;
- quién recibe uno: todos los del ranking (nunca los concursantes ocultos),
  o solo desde un **puntaje mínimo**, o **solo los concursantes con
  premio**;
- tres o cuatro **firmas** (`Nombre | Cargo` por línea) y un **logo** (PNG o
  JPEG, hasta 2 MiB) arriba.

*Vista previa del primero* muestra una página; *Descargar todos* da un PDF
con una página por concursante, y la página de cada participación tiene el
suyo. Ambas descargas quedan en el registro de auditoría. Con **los
concursantes descargan su propio certificado**, cada concursante ve un
enlace *Descargar tu certificado* en la página del concurso cuando termina
su tiempo; actívalo después de la ceremonia de clausura si los puestos
deben mantenerse en secreto hasta entonces.
