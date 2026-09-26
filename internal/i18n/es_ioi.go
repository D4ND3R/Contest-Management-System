package i18n

// esIOI holds the Spanish strings of the SPEC_IOI blocks (statements,
// results, testing, the redesign and the features that followed).
var esIOI = map[string]string{
	// H1: statements, results, testing.
	"Latest result":                "Último resultado",
	"Full details":                 "Ver el detalle",
	"Statement language":           "Idioma del enunciado",
	"PDF":                          "PDF",
	"Examples":                     "Ejemplos",
	"Example %d":                   "Ejemplo %d",
	"Explanation":                  "Explicación",
	"standard input":               "entrada estándar",
	"standard output":              "salida estándar",
	"interactive":                  "interactivo",
	"output only":                  "solo salida",
	"Memory limit":                 "Límite de memoria",
	"Tests are not available now.": "Las pruebas no están disponibles ahora.",
	// olymp.sty section titles.
	"Note": "Nota", "Notes": "Notas", "Scoring": "Puntuación", "Interaction": "Interacción",
	"Explanations": "Explicaciones", "Specification": "Especificación", "Constraints": "Restricciones",
	"Subtasks": "Subtareas", "Example": "Ejemplo",
	"A statement is a PDF, Markdown (.md), LaTeX (.tex), HTML or text file.": "Un enunciado es un archivo PDF, Markdown (.md), LaTeX (.tex), HTML o de texto.",
	"An example needs an input and an output.":                               "Un ejemplo necesita una entrada y una salida.",
	"Example added.":    "Ejemplo agregado.",
	"Examples updated.": "Ejemplos actualizados.",
	"Show it in the statement, on the page and in the PDF": "Mostrarlo en el enunciado, en la página y en el PDF",
	"use as example": "usar como ejemplo",
	"New statement":  "Nuevo enunciado",
	"This statement is an uploaded PDF: it is shown as it is. Write a Markdown or LaTeX statement instead to get the page, the PDF and the examples from one source.": "Este enunciado es un PDF subido: se muestra tal cual. Escribe un enunciado en Markdown o LaTeX para obtener la página, el PDF y los ejemplos a partir de un mismo texto.",
	"Format":         "Formato",
	"PDF preview":    "Vista previa en PDF",
	"Syntax":         "Sintaxis",
	"Formulas (TeX)": "Fórmulas (TeX)",
	"where the examples go (by default, at the end)": "dónde van los ejemplos (por defecto, al final)",
	"Preview":                            "Vista previa",
	"with %d examples":                   "con %d ejemplos",
	"Not understood (shown as written):": "No se entendió (se muestra tal como está escrito):",
	"Write statements in Markdown or LaTeX (formulas with $...$): contestants read them on the task page and download them as PDF, with the limits and the examples typeset in. An uploaded PDF is shown as it is.": "Escribe los enunciados en Markdown o LaTeX (fórmulas con $...$): los concursantes los leen en la página del problema y los descargan en PDF, con los límites y los ejemplos incluidos. Un PDF subido se muestra tal cual.",
	"edit":                                "editar",
	"view":                                "ver",
	"file":                                "archivo",
	"Write a statement":                   "Escribir un enunciado",
	"File (PDF, Markdown, LaTeX or HTML)": "Archivo (PDF, Markdown, LaTeX o HTML)",
	"Shown in every statement of the task, on the page and in the PDF (not as downloads). A testcase can be added from its dataset page.": "Se muestran en todos los enunciados del problema, en la página y en el PDF (no como descargas). Un caso de prueba se puede agregar desde la página de su dataset.",
	"Long example: only the beginning is shown here.": "Ejemplo largo: aquí solo se muestra el comienzo.",
	"Explanation (Markdown, optional)":                "Explicación (Markdown, opcional)",
	"up":                                              "subir",
	"down":                                            "bajar",
	"Delete this example?":                            "¿Borrar este ejemplo?",
	"Add an example":                                  "Agregar un ejemplo",
	"or an input file":                                "o un archivo de entrada",
	"or an output file":                               "o un archivo de salida",
	"Add example":                                     "Agregar ejemplo",
	"Statement (%s) saved.":                           "Enunciado (%s) guardado.",
	"Text":                                            "Texto",
	"Back to the task":                                "Volver al problema",
	"Choose a format.":                                "Elige un formato.",
	"The statement is too long (at most 1 MiB).": "El enunciado es demasiado largo (a lo más 1 MiB).",
	"The statement must be UTF-8 text.":          "El enunciado debe ser texto UTF-8.",
}

func init() {
	for k, v := range esIOI {
		es[k] = v
	}
}
