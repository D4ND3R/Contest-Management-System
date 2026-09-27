# Primeros pasos: un concurso completo, de cero

Esta guía lleva de la mano a alguien que nunca usó el sistema: desde el
primer inicio de sesión hasta ver el primer veredicto de un concursante.
Cada paso dice **dónde** hacer clic, **qué** escribir y **qué** debería
aparecer. Si algo no sale como aquí se describe, la sección
[Problemas frecuentes](preguntas-frecuentes.md) explica las causas más
comunes.

Hace falta tener CMS instalado (ver [instalar en un servidor](despliegue.md)
o [Docker](docker.md)). Al terminar la instalación tienes tres direcciones:

| Sitio | Para quién | Dirección típica |
|-------|------------|------------------|
| Concurso | concursantes | `https://<tu dominio>/<nombre del concurso>/` |
| Administración | organizadores | `https://admin.<tu dominio>/` (o el puerto 8889) |
| Ranking público | público, proyector | `https://ranking.<tu dominio>/` (o el puerto 8890) |

Todas las páginas tienen la misma forma: arriba una línea con el nombre del
concurso, la fase y el tiempo restante; debajo, una fila de enlaces (el
menú); en medio, tablas; y al **pie de cada página**, el idioma, el tema
(claro, oscuro, alto contraste o el del sistema) y el tamaño del texto.
Esas tres preferencias se aplican en cuanto las cambias.

## 1. Entrar a la administración

1. Abre la dirección de administración. Escribe el usuario `admin` y la
   contraseña que mostró el instalador (en un sistema de desarrollo,
   `admin`/`admin`: el primer ingreso obliga a cambiarla).
2. Verás el **Panel**. La primera fila del menú tiene las secciones de todo
   el servidor: Panel, Concursos, Banco de problemas, Usuarios,
   Equipos, Clarificaciones, Jueces, Lenguajes, Respaldos, Administradores,
   Registro de auditoría y Servidor.
3. Arriba a la derecha: **Preguntas** (con un número rojo si hay preguntas
   sin responder), tu usuario (tu cuenta: contraseña y segundo factor) y
   **Cerrar sesión**.

> Consejo: crea un administrador por persona (**Administradores**) en vez de
> compartir `admin`. Cada acción queda registrada con quién la hizo.

## 2. Elegir la zona horaria del servidor

**Servidor → Zona horaria**. Escribe el nombre de tu zona (por ejemplo
`America/Mexico_City`, `America/Bogota`, `Europe/Madrid`; la lista sugiere
mientras escribes) y **Guardar**.

Desde ese momento *todas* las horas que muestran los tres sitios (páginas
del concurso, rankings, este panel, certificados) están en esa zona, y las
horas que escribes en los formularios se leen en ella. Puedes cambiarla
cuando quieras: los momentos guardados no cambian, solo cómo se muestran.
El pie de cada página dice en qué zona están las horas.

## 3. Crear el concurso

1. **Concursos → Nuevo concurso**.
2. **Nombre**: corto, sin espacios (letras, dígitos, `.`, `_`, `-`). Es la
   dirección del concurso: `omi2026` → `https://<dominio>/omi2026/`.
3. **Título visible para todos**: el nombre largo ("Olimpiada Mexicana de
   Informática 2026"). **Descripción**: una línea que aparece debajo.
4. **Estado**: déjalo en *borrador* mientras lo preparas (los concursantes
   no lo ven); cámbialo a *publicado* cuando esté listo.
5. **Inicio** y **Fin**: en la zona del servidor.
6. **Modo**: *IOI* (puntajes y subtareas) o *ICPC* (resueltos y
   penalización).
7. **Envíos**: la **espera entre envíos** viene en 20 segundos (evita que
   alguien sature al juez; el botón Enviar del concursante hace la cuenta
   regresiva). Puedes dejar vacío el máximo de envíos por problema.
8. **Clasificación**: elige quién la ve, cuándo, si se congela al final y
   qué columnas muestra (ver [rankings](ranking.md)).
9. **Todos los demás ajustes** (plegado) guarda el resto: lenguajes
   permitidos, acceso, registro, tokens, impresión... Lo normal es no
   tocarlos. Están explicados en
   [configuración del concurso](configuracion-del-concurso.md).
10. **Crear concurso**. Llegas al panel del concurso. Ahora aparece una
    segunda fila de menú con sus páginas: el concurso, Problemas,
    Participantes, Envíos, Clasificación, Anuncios, Estadísticas, ...,
    Configuración.

## 4. Crear un problema, paso a paso

La guía completa está en [crear un problema](crear-un-problema.md); aquí el
camino más corto para un problema de entrada y salida estándar.

1. En el concurso: **Problemas → Nuevo problema**. Basta un **nombre**
   corto (`suma`) y un **título** ("Suma de dos números"). **Crear
   problema**.
2. Llegas a la ventana **Configuración** del problema; la otra pestaña es
   la ventana **Casos de prueba**. Una línea arriba dice qué falta, con un
   enlace a cada parte. Puedes hacerlas en cualquier orden y en días
   distintos. (Con un paquete de problema listo, **Importar un paquete**
   lo rellena todo de una vez.)
3. **Archivos → Escribir un enunciado**: idioma `es`, el texto en Markdown
   (fórmulas con `$...$`) y **Guardar**. La vista previa se ve a la derecha.
4. Ventana **Casos de prueba**, **Un caso**: escribe la entrada (`1 2`), la
   salida (`3`), marca **Público** si quieres que los concursantes vean su
   resultado, y **Agregar caso**. Repite para cada caso; el nombre se
   propone solo (1, 2, 3...).
5. Si tienes una solución correcta, envíala en **Envíos de prueba** (en la
   misma ventana). Con ella puedes escribir solo las entradas y elegir
   *Salida: escrita por la solución de referencia*; o generar muchos casos
   con un programa (**Generar con un programa**). La lista de casos muestra
   el veredicto de cada envío de prueba en cada caso.
6. **Configuración → Opciones**: límites, checker y **Puntuación y
   subtareas**. Por defecto cada caso vale un punto (*Sum*). Para
   subtareas, elige *GroupMin* y define cada subtarea con sus puntos y sus
   casos. **Guardar**.
7. La línea de lo que falta solo debe pedir un concurso (o decir *Listo*).
   Envía de nuevo la solución para comprobar que obtiene el puntaje
   completo.

## 5. Inscribir concursantes

- Uno por uno: **Usuarios → Nuevo usuario**, y luego en el concurso
  **Participantes → Agregar usuarios** con su nombre de usuario.
- Muchos a la vez: prepara un CSV con la primera fila
  `username,password,first_name,last_name` y súbelo en **Participantes →
  Importar usuarios de un CSV a este concurso**. Revisa la vista previa (cada
  fila con sus errores) y confirma. Si dejas vacías las contraseñas, marca
  *Generar las contraseñas faltantes*.
- **Generar e imprimir credenciales** crea un PDF con usuario, contraseña y
  la dirección del concurso para repartir.

## 6. Probar como concursante

1. Pon el concurso en *publicado* y abre `https://<dominio>/<concurso>/`
   en otra ventana (o en modo incógnito).
2. Entra con un usuario de prueba. La página **Problemas** lista los
   problemas con tu puntaje y tus envíos; al lado derecho del menú está
   cada problema (`A. suma`).
3. Abre el problema: pestaña **Enunciado**; la pestaña **Envíos** tiene el
   formulario de envío y la lista de tus envíos.
4. Envía la solución (archivo o escribiéndola en *O escribe el código
   aquí*). Aparece un aviso "Envío realizado" y la fila del envío se
   actualiza sola: *Compilando…*, *Evaluando* con una barra de progreso, y
   el veredicto final con otro aviso.
5. Abre **detalles**: cada subtarea es un bloque de color (verde AC,
   amarillo PA parcial, rojo WA u otro error) con su puntaje; debajo, el
   resultado de los casos públicos uno por uno. Los casos secretos no se
   muestran uno por uno, pero sí cuentan en los bloques.
6. En **Clarificaciones** haz una pregunta. En la administración aparece el
   número rojo en **Preguntas**; respóndela (marca *Mostrar la pregunta y la respuesta a todos en el concurso* si
   sirve a todos). El concursante recibe un aviso sin recargar la página.

## 7. El día del concurso

Sigue el [manual del día del concurso](dia-del-concurso.md): verificar la
máquina de evaluación, un respaldo, un ensayo corto con un par de
concursantes, y durante el concurso vigilar el panel (se actualiza solo) y
las preguntas.
