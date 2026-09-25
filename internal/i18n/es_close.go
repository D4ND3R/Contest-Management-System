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

	// A4: backups.
	"Backups":                     "Respaldos",
	"Backups are not configured.": "Los respaldos no están configurados.",
	"Each backup holds the whole database and every file (statements, testcases, submissions) in a single file whose hashes are checked when it is restored.": "Cada respaldo contiene toda la base de datos y todos los archivos (enunciados, casos de prueba, envíos) en un único archivo cuyos hashes se comprueban al restaurarlo.",
	"Folder":               "Carpeta",
	"During contests":      "Durante los concursos",
	"Otherwise":            "El resto del tiempo",
	"every %s":             "cada %s",
	"no scheduled backups": "sin respaldos programados",
	"Rotation":             "Rotación",
	"the newest %d scheduled backups are kept; manual ones stay until deleted": "se conservan los %d respaldos programados más recientes; los manuales quedan hasta que se eliminen",
	"Off-site copy":              "Copia externa",
	"not configured (backup.s3)": "no configurada (backup.s3)",
	"Back up now":                "Respaldar ahora",
	"To restore one, stop every CMS service and run: cmsctl restore <file> (the operations guide has the details).": "Para restaurar uno, detén todos los servicios del CMS y ejecuta: cmsctl restore <archivo> (la guía de operación tiene los detalles).",
	"Backup in progress (%s, started %s ago): %s read.":                                                             "Respaldo en curso (%s, iniciado hace %s): %s leídos.",
	"Time (UTC)":       "Hora (UTC)",
	"Kind":             "Tipo",
	"Size":             "Tamaño",
	"Took":             "Duración",
	"scheduled":        "programado",
	"manual":           "manual",
	"cli":              "línea de comandos",
	"external":         "externo",
	"done":             "listo",
	"failed":           "falló",
	"%d files missing": "faltan %d archivos",
	"registered but missing from the store when the backup was taken": "registrados pero ausentes del almacén cuando se tomó el respaldo",
	"download":                     "descargar",
	"delete":                       "eliminar",
	"Delete this backup for good?": "¿Eliminar este respaldo definitivamente?",
	"No backups yet.":              "Todavía no hay respaldos.",
	"Another backup is running.":   "Hay otro respaldo en curso.",
	"Backup started.":              "Respaldo iniciado.",
	"Backup deleted.":              "Respaldo eliminado.",
}

func init() {
	for k, v := range esClose {
		es[k] = v
	}
}
