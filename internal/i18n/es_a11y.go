package i18n

// Spanish for accessibility and localization (SPEC_IOI H7).
func init() {
	for k, v := range map[string]string{
		"Theme":                  "Tema",
		"Text size":              "Tamaño del texto",
		"Dark":                   "Oscuro",
		"Light":                  "Claro",
		"High contrast":          "Alto contraste",
		"As the system":          "Como el sistema",
		"Normal":                 "Normal",
		"Large":                  "Grande",
		"Larger":                 "Más grande",
		"Largest":                "El más grande",
		"Apply":                  "Aplicar",
		"Language and display":   "Idioma y visualización",
		"Or write the code here": "O escribe el código aquí",
		"Source code":            "Código fuente",
		"Tab indents. To leave the editor with the keyboard, press Esc and then Tab. Ctrl+Enter submits. A draft is kept in this browser.": "Tab indenta. Para salir del editor con el teclado, presiona Esc y luego Tab. Ctrl+Enter envía. Se guarda un borrador en este navegador.",
	} {
		es[k] = v
	}
}
