package i18n

// esRanking is the Spanish catalog of the rankings (ranking web server,
// contestants' ranking page and the admin settings).
var esRanking = map[string]string{
	"Back to the ranking":                           "Volver al ranking",
	"Frozen since %s: later submissions show as ?.": "Congelado desde las %s: los envíos posteriores se muestran como ?.",
	"No history.":                                   "Sin historial.",
	"No public rankings.":                           "No hay rankings públicos.",
	"Participant":                                   "Participante",
	"Score history":                                 "Historial de puntaje",
	"Solved":                                        "Resueltos",
	"The ranking is frozen: results of later submissions show as ?.": "El ranking está congelado: los resultados de envíos posteriores se muestran como ?.",
	"You are not in the ranking.":                                    "No apareces en el ranking.",
	"Your position: %d of %d.":                                       "Tu posición: %d de %d.",
	// Admin settings
	"Who sees it":                             "Quién lo ve",
	"everybody (public scoreboard)":           "todos (marcador público)",
	"only contestants":                        "solo los concursantes",
	"only administrators (and a secret link)": "solo administradores (y un enlace secreto)",
	"nobody (admin panel only)":               "nadie (solo el panel de administración)",
	"What contestants see":                    "Qué ven los concursantes",
	"the whole ranking":                       "el ranking completo",
	"only their position":                     "solo su posición",
	"nothing":                                 "nada",
	"When":                                    "Cuándo",
	"When the ranking is shown":               "Cuándo se muestra el ranking",
	"during and after the contest":            "durante y después del concurso",
	"only after the end":                      "solo al terminar",
	"Freeze the last (minutes)":               "Congelar los últimos (minutos)",
	"Freeze minutes":                          "Minutos de congelamiento",
	"or freeze at (%s)":                       "o congelar a las (%s)",
	"subtask scores":                          "puntajes por subtarea",
	"flags":                                   "banderas",
	"institutions":                            "instituciones",
	"hidden users":                            "usuarios ocultos",
	"anonymous (no names)":                    "anónimo (sin nombres)",
	"Ranking visibility":                      "Visibilidad del ranking",
	"Team contests are ranked by team. The public scoreboard is served by the ranking web server; the freeze hides later results until you unfreeze it from the ranking page.": "Los concursos por equipos se clasifican por equipo. El marcador público lo sirve el servidor de ranking; el congelamiento oculta los resultados posteriores hasta que lo descongeles desde la página del ranking.",
	"Public ranking:":                  "Ranking público:",
	"open the scoreboard":              "abrir el marcador",
	"frozen since %s UTC":              "congelado desde %s UTC",
	"freezes at %s UTC":                "se congela a las %s UTC",
	"unfrozen":                         "descongelado",
	"Freeze the public ranking again?": "¿Congelar de nuevo el ranking público?",
	"Unfreeze the public ranking? Every result becomes visible.": "¿Descongelar el ranking público? Todos los resultados se harán visibles.",
	"Freeze again": "Congelar de nuevo",
	"Unfreeze now": "Descongelar ahora",
	"This page always shows the complete, unfrozen ranking.": "Esta página siempre muestra el ranking completo, sin congelar.",
	"The public ranking is frozen again.":                    "El ranking público está congelado de nuevo.",
	"The public ranking is unfrozen: every result is shown.": "El ranking público está descongelado: se muestran todos los resultados.",
	"public":      "público",
	"contestants": "concursantes",
	"admins":      "administradores",
	"hidden":      "oculto",
}

func init() {
	for k, v := range esRanking {
		es[k] = v
	}
}
