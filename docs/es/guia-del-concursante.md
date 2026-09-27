# Guía del concursante

Todo lo que ve y hace un concursante en el sitio del concurso
(`https://<dominio>/<concurso>/`). Los organizadores pueden imprimirla o
enlazarla antes del concurso.

## La página

- **Arriba**: el nombre del concurso, la fase (*Concurso en curso*,
  *Terminado*...), el **tiempo restante**, tu usuario y **Cerrar sesión**.
- **Menú**: *Problemas*, un enlace por problema (`A. suma`, `B. caminos`...),
  *Clasificación* (si los organizadores la muestran), *Clarificaciones* (con
  un número rojo si hay algo nuevo sin leer), *Pruebas*, *Impresión* y
  *Apelaciones* (si están habilitadas) y *Documentación* (lenguajes,
  compiladores y bibliotecas).
- **Pie**: idioma, tema (claro, oscuro, alto contraste o el del sistema) y
  tamaño del texto. Se aplican al instante y se recuerdan.

## Problemas

La primera página lista los problemas con tu puntaje (`40 / 100`, en verde
si está completo, amarillo si es parcial) y cuántos envíos llevas; debajo,
los avisos de los organizadores y tus últimos envíos.

## Un problema: Enunciado y Envíos

Cada problema tiene dos pestañas:

- **Enunciado**: el texto, los límites (tiempo, memoria), los ejemplos, el
  PDF y los adjuntos para descargar. Si los organizadores lo corrigen
  durante el concurso, aparece un aviso y el enunciado se recarga solo.
- **Envíos**: el formulario para enviar y la lista de tus envíos del
  problema.

## Enviar

1. En **Envíos**, elige el archivo (`suma.*`: la extensión va según el
   lenguaje) y el **lenguaje**; o abre **O escribe el código aquí** y
   escríbelo (Tab indenta; Esc y luego Tab sale del editor; Ctrl+Enter
   envía; se guarda un borrador en el navegador).
2. **Enviar**. Aparece el aviso **Envío realizado** y el envío entra a la
   lista.
3. El botón muestra **Espera 0:20**: hay un tiempo mínimo entre envíos
   (lo fijan los organizadores) para no saturar al juez.

Antes de enviar, la página revisa que el archivo no sea demasiado grande y
que su extensión coincida con el lenguaje.

## Seguir el resultado

No hace falta recargar: la fila del envío cambia sola.

1. *Compilando…*
2. *Evaluando* con una barra de progreso (casos terminados de casos
   totales) y, si hay espera, cuántos envíos van antes que el tuyo.
3. El **veredicto** y el **puntaje**; aparece también un aviso ("Envío 4
   evaluado"), aunque estés en otra página del concurso. Si el navegador lo
   permite, también una notificación del sistema cuando la pestaña no está
   a la vista (**Activar notificaciones de escritorio**).

### Veredictos

| Código | Significado |
|--------|-------------|
| **AC** | Aceptado: todo correcto. |
| **PA** | Parcialmente correcto: algunos puntos. |
| **WA** | Respuesta incorrecta. |
| **TLE** | Tiempo límite excedido. |
| **MLE** | Memoria excedida. |
| **RE** | Error en tiempo de ejecución (el programa terminó con error). |
| **OLE** | Límite de salida excedido. |
| **SV** | Violación de seguridad (el programa intentó algo prohibido). |
| **CE** | Error de compilación: abre los detalles para ver el mensaje del compilador. |
| **SK** | Omitido: no se evaluó porque la subtarea ya había fallado. |

### Los detalles de un envío

**detalles** abre la página del envío:

- los datos (hora, lenguaje, veredicto, puntaje, archivos);
- **Subtareas**: un bloque por subtarea con sus puntos (`40 / 40`) y su
  veredicto; verde = aceptada, amarillo = parcial, rojo = falló;
- **Casos públicos**: el resultado de cada caso público (veredicto,
  mensaje, tiempo y memoria). Los casos secretos no se muestran uno por
  uno, pero cuentan en los bloques;
- **Compilación**: el resultado y los mensajes del compilador (también las
  advertencias).

Si el concurso usa **tokens**, verás el puntaje de los casos públicos
("público") hasta que uses un token en ese envío (**usar un token**), que
muestra el resultado completo. Las reglas (cuántos tienes, cada cuánto
llega uno) aparecen en la pestaña Envíos.

## Clarificaciones

**Clarificaciones** reúne en una tabla, del más nuevo al más viejo:

- los **avisos** de los organizadores para todos;
- tus **preguntas** y sus respuestas (*Tu pregunta*; *Tu pregunta,
  respondida para todos* cuando la respuesta sirve a todos);
- las **clarificaciones para todos**: preguntas de otros que los
  organizadores respondieron públicamente;
- los **mensajes privados** que te mandan los organizadores.

Para preguntar: elige **Sobre** (un problema o *General*), escribe la
**Pregunta** y **Enviar pregunta**. Cuando llega una respuesta o un aviso,
aparece un aviso en cualquier página del concurso y el número rojo del menú
aumenta. **Sonido** activa un sonido breve con cada aviso.

## Pruebas

**Pruebas** ejecuta tu programa con una entrada tuya (sin que cuente):
elige el problema, el archivo o el código, escribe la entrada y envía; la
fila muestra la salida, el tiempo y la memoria.

## Si la página no se actualiza

La página recibe las novedades por una conexión abierta con el servidor; si
se corta (red, proxy), cambia sola a consultar cada pocos segundos, así que
los resultados siguen llegando. Recargar la página nunca hace daño.
