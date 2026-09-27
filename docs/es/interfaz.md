# Idiomas de la interfaz y accesibilidad

## Idiomas de la interfaz

Los sitios de concursantes y de ranking hablan 18 idiomas: inglés, español,
francés, portugués, alemán, italiano, ruso, ucraniano, polaco, turco, chino
(simplificado), japonés, coreano, vietnamita, indonesio y tres que se
escriben de derecha a izquierda: árabe, persa y hebreo. El sitio de
administración está en inglés y español.

Cada visitante elige un idioma en el menú **Idioma y visualización** (en el
menú de usuario, o al pie de la página de inicio de sesión); la elección se
recuerda en el navegador. Sin elección, deciden los idiomas preferidos del
navegador. Un concurso puede restringir los idiomas ofrecidos
(las casillas **Interfaz** de la configuración del concurso, o
`allowed_localizations` en [contest.yaml](configuracion-en-git.md)).

Los idiomas de derecha a izquierda invierten todo el diseño (el menú pasa a
la derecha, las flechas apuntan al otro lado). El código fuente, las
entradas y las salidas siempre se leen de izquierda a derecha. Un
enunciado se muestra en la dirección de su propio idioma, sea cual sea el
de la interfaz, así que un enunciado en persa se lee bien en una página en
inglés y viceversa. Las preguntas, respuestas, anuncios y nombres toman la
dirección de lo escrito.

Las traducciones incluidas se escribieron para este proyecto y se
verificaron automáticamente (todos los mensajes presentes, todos los
marcadores en su lugar), pero todavía no las revisaron hablantes nativos.
Antes de un concurso oficial, pide a un hablante de cada idioma que ofrezcas
que lea las páginas de concursantes, y corrige lo necesario como se explica
abajo.

### Agregar o corregir un idioma

No hace falta código: un idioma es un archivo YAML con el texto en inglés de
cada mensaje y su traducción.

```sh
cmsctl locale-template -name "Kiswahili" sw > sw.yaml       # todos los mensajes, el español como pista
cmsctl locale-template -from /etc/cms/locales/fr.yaml fr > fr.yaml   # continuar un archivo
cmsctl locale-check sw.yaml                                  # marcadores y cobertura
```

Pon los archivos en un directorio y apunta `locales_dir` (o
`CMS_LOCALES_DIR`) hacia él:

```yaml
locales_dir: /etc/cms/locales
```

Un archivo con el nombre de un idioma incluido (`fr.yaml`) lo corrige: sus
traducciones reemplazan a las incluidas y el resto se conserva. Un código
nuevo agrega un idioma (`dir: rtl` para escrituras de derecha a izquierda).
Los mensajes vacíos se muestran en inglés. Conserva todos los marcadores
(`%d`, `%s`) del texto en inglés; los índices del estilo `%[2]s` los
reordenan cuando la gramática lo pide. Una traducción con otros marcadores
se ignora (se muestra el inglés) y se registra al iniciar los servicios.
Reinicia los servicios de concurso, administración y ranking después de
cambiar el directorio.

Las contribuciones son bienvenidas: para incluir un idioma nuevo basta con
un `cmsctl locale-check` completo y la revisión de un hablante nativo.

## Accesibilidad

Los tres sitios están hechos para usarse solo con teclado, con lector de
pantalla y con texto grande.

- **Preferencias de visualización** (menú **Idioma y visualización**):
  oscuro (por defecto), claro, alto contraste (blanco y negro con acentos
  amarillos, enlaces subrayados) o como prefiera el sistema operativo;
  tamaño de texto normal, grande, más grande o el más grande. El tamaño base
  sigue la configuración de letra del propio navegador y cada página se
  reacomoda con cualquier zoom. Se recuerdan en el navegador, valen antes de
  iniciar sesión y nunca cambian lo que ven los organizadores.
- **Teclado**: cada página empieza con un enlace "Ir al contenido"; todos
  los controles se alcanzan con Tab y muestran dónde está el foco; los
  menús se abren con Enter y se cierran con Escape; el botón de menú de las
  pantallas angostas también se alcanza. El editor de código reserva Tab
  para indentar: presiona Esc y luego Tab para salir (sin trampa de
  teclado).
- **Lectores de pantalla**: las páginas declaran su idioma y dirección, cada
  campo de formulario tiene etiqueta, los íconos se ocultan a la tecnología
  asistiva o tienen nombre, los resultados aparecen en una región viva, las
  notificaciones se anuncian y las marcas del ranking (medallas,
  participantes no oficiales) tienen palabras.
- **Contraste**: todos los colores de texto cumplen WCAG AA (4,5:1) sobre su
  fondo en los temas oscuro y claro; el tema de alto contraste va mucho más
  allá.
- **Movimiento**: con la opción "reducir movimiento" del sistema operativo
  nada se anima.

La suite de pruebas revisa cada página de concursantes, de ranking y de
administración con estas reglas (etiquetas, nombres, ids únicos, regiones,
idioma y dirección) y maneja el sitio del concurso en un navegador real
(las teclas del editor, el diseño de derecha a izquierda, los temas y los
tamaños de texto).

## El editor de código

Los problemas cuya entrega es un solo archivo fuente muestran **O escribe el
código aquí** debajo del campo de archivo: un área de texto simple donde Tab
indenta (Shift+Tab quita la indentación, también sobre una selección),
Ctrl+Enter envía, y se guarda un borrador en el navegador hasta que se
reemplace. No necesita complementos y funciona con el deshacer, la búsqueda
y el zoom del propio navegador. Un archivo elegido en el campo de archivo
tiene prioridad sobre el texto del editor. Antes de enviar, la página
comprueba el tamaño del archivo y que su extensión corresponda al lenguaje
elegido; el servidor vuelve a comprobarlo todo.
