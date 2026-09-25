package i18n

// esClose holds the Spanish strings of the SPEC_CLOSE blocks (invalidation,
// backups and the rest of the closing features).
var esClose = map[string]string{
	// A3: invalidated submissions.
	"invalidated": "invalidado",
	"Invalidated on %s UTC: it does not count.": "Invalidado el %s UTC: no cuenta.",
	"Reason:":                          "Motivo:",
	"Make the submission count again?": "¿Hacer que el envío vuelva a contar?",
	"Restore (count it again)":         "Restaurar (que vuelva a contar)",
	"Invalidate this submission":       "Invalidar este envío",
	"Reason (shown to the contestant)": "Motivo (lo ve el concursante)",
	"The submission stays visible but stops counting for the score, the ranking and ICPC penalties; it can be restored.": "El envío sigue visible pero deja de contar para el puntaje, el ranking y las penalizaciones ICPC; se puede restaurar.",
	"Invalidate": "Invalidar",
	"Write the reason (at most 1000 characters): the contestant sees it.":                "Escribe el motivo (a lo más 1000 caracteres): el concursante lo ve.",
	"Submission invalidated: it no longer counts.":                                       "Envío invalidado: ya no cuenta.",
	"Submission restored: it counts again.":                                              "Envío restaurado: vuelve a contar.",
	"The organizers invalidated this submission on %s: it does not count for the score.": "Los organizadores invalidaron este envío el %s: no cuenta para el puntaje.",
}

func init() {
	for k, v := range esClose {
		es[k] = v
	}
}
