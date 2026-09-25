# PROMPT SECUNDARIO: Auditoría de compatibilidad y completitud del CMS

## 0. Reglas
- SKILLS: NO uses `immersive-web-design` ni `master skill`. Sáltalas aunque parezcan
  aplicar. Confirma en una línea: "Skipping skills: immersive-web-design, master skill".
- Lee SPEC.md, PROGRESS.md y DECISIONS.md antes de empezar.
- Mismo MODO AUTÓNOMO del SPEC: no pidas aprobación. Solo detente por credenciales
  faltantes, acciones destructivas fuera del repo o 3 fallos seguidos en el mismo
  problema. Registra decisiones en DECISIONS.md.
- Guarda este mensaje como SPEC_AUDIT.md.
- Mantén los objetivos de rendimiento de SPEC.md: nada de lo que agregues puede
  romperlos. Repite las pruebas de carga de F10 al final.

## 1. Procedimiento
1. AUDITAR: recorre el código y crea AUDIT.md con una matriz de cada requisito de
   este documento: estado (completo / parcial / falta), archivos involucrados y
   test que lo cubre.
2. IMPLEMENTAR: resuelve todo lo "parcial" o "falta", en el orden de las secciones.
3. VERIFICAR: cada requisito necesita al menos un test automatizado. Actualiza
   AUDIT.md hasta que todo esté "completo" (o "pendiente de verificar en hardware
   real" si el entorno no permite probarlo).
4. Commit por sección. Al final, resumen: qué ya existía, qué agregaste y qué quedó
   pendiente.

## 2. Tipos de problema (todos obligatorios)
Cada tipo debe poder crearse y configurarse completo desde el admin panel, importarse
y juzgarse, con soluciones de ejemplo AC/WA/TLE/MLE/RE en los tests.

1. Normal I/O: el programa lee de stdin y escribe a stdout. Comparación con
   checker (diff exacto, ignorar espacios, reales con tolerancia, custom).
2. Batch: variante con archivos. Soportar:
   - I/O por archivos con nombres configurables (p. ej. input.txt / output.txt).
   - Grader: el concursante implementa funciones; se compila junto con un grader y
     headers/stubs por lenguaje provistos por el admin (C, C++, Java, Python mínimo).
   - Combinaciones: grader + stdin/stdout y grader + archivos.
3. Interactive (estilo ICPC/Codeforces): un interactor del admin se comunica con UN
   proceso del concursante por pipes bidireccionales (stdin/stdout cruzados). El
   interactor decide el veredicto. Manejar: el concursante no hace flush (TLE por
   wall time), el interactor termina primero, el concursante termina primero,
   salida inválida (WA), consumo de tiempo del interactor sin cobrárselo al
   concursante. Interactor en su propia caja de isolate.
4. Communication (estilo IOI/CMS): un manager del admin + N procesos del concursante
   (N configurable), comunicados por FIFOs, cada uno en caja separada. Límites de
   tiempo y memoria aplicados por proceso o sumados (configurable). Soportar stubs
   por lenguaje. El manager escribe score y mensaje.
5. Output only: el concursante sube un archivo de salida por testcase (o un zip).
   Validar nombres de archivos, envíos parciales (solo algunos testcases) y
   combinación con el mejor resultado previo por testcase si el admin lo activa.
6. Mantener TwoSteps como en SPEC.md.

Para todos: checkers custom (protocolo CMS y testlib), subtareas, score types,
feedback full/restricted, testcases públicos, varios datasets por tarea y
reevaluación. Agrega al admin un "probador de tarea": subir una solución de
referencia y ver el veredicto por testcase sin que cuente como envío.

## 3. Gestión de usuarios desde el admin panel
- Crear usuario individual con: username, nombre, apellidos, email, institución/
  escuela, país, estado/región, foto opcional, idioma preferido, zona horaria.
- Carga masiva por CSV (con vista previa y reporte de errores por fila) y
  exportación a CSV.
- Generación automática de contraseñas seguras, reseteo de contraseña, y hoja de
  credenciales imprimible (PDF, una o varias por página, para repartir en sede).
- Editar, deshabilitar/habilitar, eliminar (con confirmación), forzar cierre de
  sesión, ver sesiones activas e IP de acceso.
- "Ver como concursante" (modo lectura, registrado en el audit log).
- Inscribir usuarios en concursos (participation) individualmente o en lote, con
  overrides por participación: contraseña propia, IPs permitidas, extra_time,
  delay_time, hidden, unrestricted.
- Equipos: CRUD, asignar miembros, nombre, bandera/logo, institución.
- Grupos o sedes: agrupar participantes con su propio horario de inicio (concursos
  multisede) y filtrar el ranking por sede.
- Administradores: crear/editar admins con roles (all / messaging / read-only) y
  2FA opcional (TOTP).

## 4. Configuración del concurso desde el admin panel
Todo editable en la UI, con valores por defecto sensatos y validación.

### General
Nombre, slug, descripción, estado (borrador / publicado / archivado), clonar
concurso, lenguajes de programación permitidos, idiomas de la interfaz, zona
horaria, enunciados y adjuntos descargables.

### Horario
Inicio y fin; ventana por usuario (per_user_time) con botón "Empezar"; modo análisis
(inicio/fin) con envíos que no cuentan; práctica/upsolving después del concurso;
reloj de cuenta regresiva visible para el concursante; extensión global de tiempo
en caliente.

### Modalidad
- Individual o por equipos (tamaño máximo de equipo; envíos compartidos por equipo).
- Formato de puntuación: IOI (puntaje parcial, subtareas) o ICPC (binario con
  penalización; minutos de penalización por intento configurables).
- Score mode por defecto (max, max_subtask, max_tokened_last) y precisión.

### Leaderboard / ranking
- Visibilidad: público, solo concursantes, solo admins, oculto.
- Qué ven los concursantes: ranking completo, solo su posición, o nada.
- Congelar (freeze) los últimos X minutos y descongelar manualmente.
- Mostrar durante el concurso y/o solo al final.
- Mostrar u ocultar desglose por subtarea, banderas, instituciones y usuarios hidden.
- Ranking anonimizado opcional.

### Resultados y feedback
- Mostrar puntaje al concursante: sí / no / solo al final.
- Nivel de feedback por defecto (full / restricted) y detalle por testcase.
- Mostrar salida del compilador.
- Tokens (disabled / finite / infinite con todos sus parámetros).

### Envíos
Número máximo de envíos por concurso y por tarea, intervalo mínimo entre envíos,
tamaño máximo de archivo, lenguajes permitidos por tarea, user tests
(activar/desactivar y sus propios límites).

### Acceso y seguridad
Registro: solo por admin / autoregistro con aprobación / código de invitación.
Restricción por IP o subred, autologin por IP, bloquear sesiones simultáneas,
política de contraseñas, duración de sesión.

### Comunicación
Activar preguntas/aclaraciones, respuestas rápidas predefinidas, anuncios,
mensajes privados, notificaciones en vivo.

### Impresión
Activar/desactivar, páginas máximas por trabajo y por concursante, cola visible
para el staff.

## 5. Configuración de cada tarea desde el admin panel
Tipo de problema (sección 2) con sus opciones; nombres de archivos de I/O; límites
de tiempo, wall time, memoria y salida; puntaje máximo; subtareas con editor
visual (testcases por regex o selección); score type; feedback; score mode;
tokens y límites de envío propios; lenguajes permitidos; enunciados por idioma;
adjuntos; checker/interactor/manager; graders y stubs por lenguaje; carga de
testcases en zip con detección de pares input/output; datasets y cambio de
dataset live; orden de las tareas en el concurso.

## 6. Resto de funcionalidades de un CMS completo (verificar y completar)
- Envíos: búsqueda y filtros (usuario, tarea, veredicto, lenguaje, fecha), ver
  fuente con resaltado, diff entre envíos, descargar todos los envíos en zip.
- Rejuzgar por envío, usuario, tarea o concurso; invalidar; ajuste manual de
  puntaje con justificación obligatoria (registrado en el audit log).
- Detección de plagio: reporte de similitud entre envíos por tarea.
- Panel de sistema en vivo: workers, colas, trabajos atascados, errores del
  sistema, uso de CPU/memoria.
- Estadísticas por tarea: distribución de puntajes, envíos por veredicto,
  primer AC.
- Modo ICPC: globos (lista de primeros AC por equipo y tarea para el staff).
- Exportar resultados finales (CSV, JSON, PDF imprimible) y certificados/
  constancias opcionales en PDF.
- Backups: dump programado y restauración desde el admin o la CLI.
- Audit log de toda acción de admin con filtros.
- i18n es/en en todas las pantallas nuevas.

## 7. Cierre
Corre la suite completa, las pruebas de carga de F10 y la batería de seguridad.
Actualiza AUDIT.md, PROGRESS.md y la documentación de admin (cómo crear cada tipo
de problema, paso a paso). Entrega el resumen final.

Este es un propmt secundario, debes de acoplar esto al primario y terminar
