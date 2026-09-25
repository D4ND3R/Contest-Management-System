# Configuración del concurso

Todo lo que sigue está en la página del concurso del sitio de administración
(**Concursos → el concurso**). Los cambios se aplican al instante: las
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
  administrador la apruebe. La página del concurso muestra cuántos registros
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
- **Globos** (enlace en la página del concurso): la lista para el staff de
  cada problema resuelto por un equipo, del más antiguo al más reciente, con
  la sede y quién lo resolvió; se marca la primera solución de cada
  problema. Se actualiza sola; el botón *entregado* pasa el globo a la lista
  de entregados (con quién y cuándo; *deshacer* lo devuelve). Filtra por
  sede para repartir sala por sala. El staff con rol de *mensajería* puede
  marcar entregas; las participaciones ocultas no reciben globos.

## Envíos y pruebas

- **Tamaño máximo de un archivo enviado** (vacío: el valor del servidor,
  `contest_web.max_submission_bytes`).
- Cantidad máxima de envíos e intervalo mínimo entre ellos, por concurso y por
  problema. Las **pruebas de usuario** (ejecutar un código con una entrada
  propia) tienen sus propios límites de cantidad e intervalo.
