package i18n

// esComm is the Spanish catalog of communication (questions, answers,
// announcements and messages) for both web servers.
var esComm = map[string]string{
	// Contestants
	"Ask a question":               "Hacer una pregunta",
	"About":                        "Sobre",
	"General":                      "General",
	"Question":                     "Pregunta",
	"Send question":                "Enviar pregunta",
	"Announcements":                "Avisos",
	"Private messages":             "Mensajes privados",
	"Your questions":               "Tus preguntas",
	"Answers for everyone":         "Respuestas para todos",
	"answered for everyone":        "respondida para todos",
	"Waiting for an answer.":       "Esperando respuesta.",
	"You have not asked anything.": "No has hecho preguntas.",
	"Sound: on":                    "Sonido: activado",
	"Sound: off":                   "Sonido: desactivado",
	"unread":                       "sin leer",
	"Questions are closed.":        "Las preguntas están cerradas.",
	"Write a question (at most 4000 characters).": "Escribe una pregunta (a lo más 4000 caracteres).",
	"Unknown task.": "Problema desconocido.",
	"You are asking too many questions; wait a minute.": "Estás haciendo demasiadas preguntas; espera un minuto.",
	"Your question was sent.":                           "Tu pregunta fue enviada.",
	// Quick answers (stored in English)
	"Yes":                       "Sí",
	"No":                        "No",
	"Read the statement":        "Lee el enunciado",
	"No comment":                "Sin comentarios",
	"Answered in the statement": "Respondido en el enunciado",

	// Staff
	"Questions of this contest":  "Preguntas de este concurso",
	"Communication in %s":        "Comunicación en %s",
	"Subject":                    "Asunto",
	"Text":                       "Texto",
	"Publish to everyone":        "Publicar para todos",
	"Delete the announcement?":   "¿Eliminar el aviso?",
	"No announcements.":          "No hay avisos.",
	"To (username or team code)": "Para (usuario o código de equipo)",
	"Send":                       "Enviar",
	"To":                         "Para",
	"No messages.":               "No hay mensajes.",
	"No questions to answer.":    "No hay preguntas por responder.",
	"Pending questions come first, the oldest at the top. New questions appear here and in the menu counter as they arrive.": "Las preguntas pendientes van primero, la más antigua arriba. Las nuevas aparecen aquí y en el contador del menú al llegar.",
	"include answered and ignored":          "incluir respondidas e ignoradas",
	"Answer (optional with a quick answer)": "Respuesta (opcional con una respuesta rápida)",
	"Answer":                                "Responder",
	"Show the question and the answer to everyone in the contest": "Mostrar la pregunta y la respuesta a todos en el concurso",
	"answered":             "respondida",
	"ignored":              "ignorada",
	"ignore":               "ignorar",
	"back to pending":      "volver a pendiente",
	"unanswered questions": "preguntas sin responder",
	"at most":              "a lo más",
	"per minute and contestant (0: no limit)":   "por minuto y concursante (0: sin límite)",
	"Questions per minute":                      "Preguntas por minuto",
	"Recipient":                                 "Destinatario",
	"Announcement published.":                   "Aviso publicado.",
	"Announcement deleted.":                     "Aviso eliminado.",
	"Answer sent.":                              "Respuesta enviada.",
	"Message sent to %d participants.":          "Mensaje enviado a %d participantes.",
	"Unknown quick answer.":                     "Respuesta rápida desconocida.",
	"Write an answer or choose a quick answer.": "Escribe una respuesta o elige una respuesta rápida.",
	"The answer is too long.":                   "La respuesta es demasiado larga.",
	"%q is neither a participant nor a team with members in this contest": "%q no es un participante ni un equipo con integrantes en este concurso",
}

func init() {
	for k, v := range esComm {
		es[k] = v
	}
}
