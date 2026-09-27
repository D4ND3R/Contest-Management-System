# Enunciados

El enunciado de un problema se escribe una vez, en **Markdown** o **LaTeX**,
y el CMS lo muestra de dos formas:

- en la página del problema, como página web con las fórmulas dibujadas por
  el navegador (MathML, sin JavaScript), en el idioma del concursante
  cuando existe;
- como **PDF** (botón "PDF" junto al enunciado) con el título del problema,
  los límites (tiempo, memoria, archivos de entrada y salida) y los
  **ejemplos** incluidos, listo para imprimir.

También se acepta un PDF subido, que se muestra tal cual (incrustado en la
página, con los ejemplos del problema debajo). Los enunciados HTML (los de
Polygon, por ejemplo) se convierten al mismo modelo: se quitan los scripts
y todo lo activo, y sus fórmulas `$...$` se dibujan.

## Escribir un enunciado

Página del problema → **Enunciados → Escribir un enunciado** (o **editar**
junto a uno existente). El editor muestra el resultado mientras escribes,
lista lo que no entendió y **Vista previa en PDF** abre el PDF sin guardar.
Marca **Principal** para el idioma oficial (puede haber varios).

Los idiomas son códigos como `es`, `en`, `pt_BR`. Los concursantes ven el
de su idioma de interfaz, o el oficial, y pueden cambiarlo.

### Markdown

```markdown
# Título (opcional; de todos modos se usa el título del problema)

Dados $N$ enteros $a_1, \ldots, a_N$ ($1 \le N \le 10^5$), calcula

$$\sum_{i=1}^{N} \left\lfloor \frac{a_i}{2} \right\rfloor$$

## Entrada

- La primera línea tiene **N**.
- Luego `N` números.

| Subtarea | Puntos | Restricciones |
|:-:|:-:|---|
| 1 | 30 | $N \le 1000$ |

![La pirámide](piramide.png)

{{examples}}

## Notas

Usa `long long`.
```

Se admiten títulos, párrafos, **negritas**, *cursivas*, `código`, listas,
tablas (con alineación), bloques de código, citas, enlaces e imágenes
(archivos adjuntos, por nombre). Una línea `{{examples}}` ubica los
ejemplos; sin ella van al final. Un signo de pesos seguido de un espacio o
un dígito (`$5 y $10`) no es una fórmula.

### LaTeX

Documentos completos o fragmentos: `\section`, `\subsection`, `\textbf`,
`\emph`, `\texttt`, `\underline`, `itemize`/`enumerate`/`description`,
`tabular`, `verbatim`/`lstlisting`, `quote`, `center`, `\includegraphics`
(imágenes adjuntas), `\url`/`\href`, acentos (`\'a`, `\~n`), comillas y
guiones. Los problemas con olymp.sty (Polygon y muchas olimpiadas)
funcionan tal cual: `\begin{problem}{Título}{...}`, `\InputFile`,
`\OutputFile`, `\Examples` con `\exmp{entrada}{salida}`, `\Note`,
`\Scoring`, `\Interaction` (los títulos de sección siguen el idioma del
enunciado).

### Fórmulas

Matemáticas de TeX como en LaTeX: `$...$` o `\(...\)` en línea, `$$...$$`
o `\[...\]` en su propia línea (también el `$$$...$$$` de Polygon). Se
admiten fracciones, raíces, subíndices y superíndices, sumas, productos e
integrales con límites, delimitadores `\left`/`\right` y `\big`, pisos y
techos, `\binom`, `\pmod`/`\bmod`, `cases`, matrices (`pmatrix`,
`bmatrix`...), `aligned`, `\text`, `\mathbb`/`\mathcal`/`\mathbf`/`\mathrm`,
acentos (`\hat`, `\bar`, `\vec`, `\overline`), letras griegas y las
relaciones y operadores habituales. Lo demás se muestra tal como está
escrito (en rojo) y el editor lo lista.

## Ejemplos

Los ejemplos son del problema, no de un dataset, y aparecen en **todos los
enunciados**, en la página y en el PDF (no como descargas):

- página del problema → **Ejemplos → Agregar un ejemplo**: escribe la
  entrada y la salida, o sube los dos archivos, con una explicación
  opcional (Markdown);
- página del dataset → **usar como ejemplo** junto a un caso de prueba;
- en un paquete de problema, `statement/examples/NOMBRE.in`, `NOMBRE.out` y
  una explicación opcional `NOMBRE.md`.

## Cambios

Un enunciado, ejemplo o límite nuevo se ve de inmediato: la página y el PDF
se regeneran a partir de lo que dependen, y sus direcciones llevan una
versión, así que ningún navegador guarda una copia vieja (una dirección
vieja se vuelve a validar).

## Fuentes del PDF

Los PDF usan las fuentes estándar que tiene cualquier lector de PDF (Times,
Helvetica, Courier y Symbol para las matemáticas), así que son pequeños y
no incrustan nada. Cubren los idiomas de Europa occidental; para
enunciados en otros alfabetos (cirílico, texto en griego, chino...) la
página web muestra todo, y para el PDF sube uno hecho en otro lado.
