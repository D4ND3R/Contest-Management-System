Agrega a SPEC_AUDIT.md (sección 5) y luego implementa, en modo autónomo:

* K18 Paquete propio de problema: un zip con `problem.yaml` (nombre, tipo, límites,
nombres de I/O, score type, subtareas, feedback, lenguajes permitidos), `statement/`
(PDF o HTML por idioma), `tests/` (pares .in/.out), `checker`/`interactor`/`manager`
opcional, `graders/` y `attachments/`. Documenta el formato en docs/ con un ejemplo
por cada tipo de problema.
* K19 Subida desde el admin: arrastrar y soltar el zip, vista previa de lo detectado,
errores claros por archivo antes de crear nada, y opción de crear la tarea o
actualizar una existente como nuevo dataset.
* K20 Validación automática al importar: compila checker/interactor/manager, corre
las soluciones de `solutions/` (si vienen, con su veredicto esperado en el nombre,
p. ej. `ac_main.cpp`, `tle_brute.py`) con el probador de tarea, y muestra un
reporte antes de publicar.
* K21 Exportar cualquier tarea en este mismo formato.
* Completa también K5 (editor visual de subtareas) y K10 (lenguajes por tarea).
Tests para cada punto y actualiza AUDIT.md.


# PROMPT DE CIERRE: todo lo que falta para un CMS listo para concursos reales

## 0. Reglas
- SKILLS: NO uses `immersive-web-design` ni `master skill`. Sáltalas aunque parezcan
  aplicar. Confirma en una línea: "Skipping skills: immersive-web-design, master skill".
- Lee SPEC.md, SPEC_AUDIT.md, AUDIT.md, PROGRESS.md y DECISIONS.md antes de empezar.
  Guarda este mensaje como SPEC_CLOSE.md.
- MODO AUTÓNOMO: no pidas aprobación. Solo detente por credenciales faltantes,
  acciones destructivas fuera del repo o 3 fallos seguidos en el mismo problema.
  Registra decisiones en DECISIONS.md.
- Si K5, K10 o K18–K21 (paquete de problemas) están en curso, termínalos primero y
  no los dupliques.
- Hardware objetivo: VPS KVM de 2 vCPU (1 núcleo para web + DB, 1 para un sandbox).
  Ningún cambio puede romper los objetivos de rendimiento de SPEC.md: la web nunca
  se degrada por la cola de evaluación.
- Cada punto necesita tests automatizados. Actualiza AUDIT.md (agrega las filas
  nuevas con su ID), PROGRESS.md y haz commit por bloque.
- Toda pantalla nueva o modificada: i18n es/en, sin JS pesado, htmx + SSE.

## BLOQUE A — Bloqueantes para un concurso real (hacer primero, en este orden)
A1. Comunicación (C8 + parte de F8):
    - Preguntas/aclaraciones: el concursante pregunta por tarea o en general; el staff
      responde en privado o hace pública la respuesta para todos. Respuestas rápidas
      predefinidas ("Sí", "No", "Lee el enunciado", "Sin comentarios", "Respondido
      en el enunciado").
    - Anuncios globales y mensajes privados a un usuario o equipo.
    - Notificación en vivo por SSE con indicador de no leídos; aviso sonoro opcional.
    - Vista del staff: bandeja de preguntas pendientes ordenada por antigüedad,
      filtro por tarea, contador en el menú del admin.
    - Límite de preguntas por minuto por usuario (antispam).
A2. Ranking (C4 + F7): servicio RWS completo según SPEC.md, con configuración por
    concurso en el admin:
    - Visibilidad: público / solo concursantes / solo admins / oculto.
    - Qué ven los concursantes: ranking completo, solo su posición, o nada.
    - Freeze de los últimos X minutos, descongelado manual por el admin.
    - Mostrar durante el concurso y/o solo al final.
    - Toggles: desglose por subtarea, banderas, instituciones, usuarios hidden,
      ranking anonimizado.
    - Snapshot JSON cacheado + deltas por SSE; historial de puntaje por usuario.
    - Ranking por equipos cuando el concurso es por equipos.
A3. Invalidar envíos (X6): excluir un envío del puntaje (con motivo obligatorio),
    revertirlo, recalcular puntajes y ranking al instante, registro en audit log.
    El concursante ve el envío marcado como invalidado.
A4. Backups (X14):
    - `cmsctl dump` y `cmsctl restore`: Postgres + blobs, en un solo archivo,
      verificando integridad (hashes).
    - Backup programado configurable (p. ej. cada 15 min durante un concurso),
      rotación de copias, destino local y opcional S3.
    - Botón en el admin para backup inmediato y lista de backups.
    - Test: dump → base vacía → restore → datos idénticos.
A5. Verificación del juez en hardware real:
    - Script `scripts/verify-host.sh` que compruebe: kernel, cgroups v2 activo,
      isolate instalado con permisos correctos, `isolate --cg` funcional, núcleos
      disponibles, estado de hyperthreading/turbo, y luego corra la batería de
      programas maliciosos y la suite de soluciones de ejemplo DOS veces,
      exigiendo veredictos idénticos. Salida: reporte claro de OK/FALLA con
      instrucciones para corregir cada falla.
    - Documentar que el concurso NO debe iniciar si este script falla.
A6. Despliegue (F11):
    - Guía paso a paso en docs/es y docs/en para instalar en un VPS Ubuntu/Debian
      limpio: dependencias, isolate, cgroups v2, Postgres y Valkey afinados para
      2 vCPU, usuarios del sistema, systemd, HTTPS con Caddy o nginx + Let's Encrypt,
      firewall (solo 80/443 y SSH).
    - Script `scripts/install.sh` idempotente que haga lo anterior.
    - Units de systemd para cada servicio con reinicio automático y límites de CPU
      (pinning del sandbox al núcleo reservado).
    - Cómo agregar un worker externo en otra máquina.
    - Runbook del día del concurso: checklist antes (verify-host, backup, simulacro
      de envío), durante (qué vigilar, cómo extender tiempo, cómo reevaluar, qué hacer
      si un worker cae o el VPS se reinicia) y después (freeze, exportar resultados,
      backup final).

## BLOQUE B — Funcionalidad de concurso (F8 y configuración pendiente)
B1. Configuración general (C1): estados borrador/publicado/archivado, clonar
    concurso (con tareas, sin envíos), lenguajes e idiomas de interfaz, zona horaria.
B2. Horario (C2): ventana por usuario con botón "Empezar", modo análisis, práctica/
    upsolving tras el concurso, reloj de cuenta regresiva visible, extensión global
    de tiempo en caliente.
B3. Modalidad (C3): individual o por equipos (tamaño máximo, envíos compartidos);
    IOI o ICPC con penalización configurable; score mode y precisión por defecto.
B4. Resultados y feedback (C5): mostrar puntaje sí/no/al final, nivel de feedback,
    detalle por testcase, salida del compilador, tokens.
B5. Envíos (C6): máximos por concurso y tarea, intervalo mínimo, tamaño máximo de
    archivo, user tests activables con sus propios límites.
B6. Acceso (C7): registro solo por admin / autoregistro con aprobación / código de
    invitación; restricción por IP/subred; autologin por IP; bloqueo de sesiones
    simultáneas; política de contraseñas; duración de sesión.
B7. Modo ICPC completo (F8): veredicto binario, penalización, freeze y globos (X11):
    lista en vivo para el staff de primeros AC por equipo y tarea, marcando entregados.
B8. Impresión (C9): activable, páginas máximas por trabajo y por concursante, cola
    visible y marcable como entregada por el staff.

## BLOQUE C — Gestión de usuarios pendiente
C1. Campos completos (U1): institución, país, estado/región, foto, idioma, zona horaria.
C2. CSV (U2, U3): importación con vista previa y errores por fila; exportación.
C3. Contraseñas (U4): generación segura, reseteo individual y masivo.
C4. Hoja de credenciales imprimible en PDF (U5): una o varias por página, con
    usuario, contraseña, nombre, sede y URL del concurso.
C5. Cuenta (U6): deshabilitar/habilitar, eliminar con confirmación, forzar cierre
    de sesión, ver sesiones activas e IPs.
C6. Ver como concursante (U7): solo lectura y registrado en el audit log.
C7. Equipos (U9): CRUD completo con miembros, bandera/logo e institución.
C8. Sedes (U10): grupos con horario de inicio propio; filtrar ranking por sede.
C9. Admins (U11): roles all/messaging/read-only y 2FA TOTP opcional.

## BLOQUE D — Herramientas del admin pendientes
D1. Envíos (X1, X2, X4): filtros por veredicto y fecha, resaltado de sintaxis,
    descargar todos los envíos en zip (por concurso, tarea o usuario).
D2. Ajuste manual de puntaje (X7) con justificación obligatoria y audit log.
D3. Panel de sistema (X9): trabajos atascados con botón de reencolar, errores del
    sistema, uso de CPU/memoria/disco, espacio del blob store.
D4. Estadísticas por tarea (X10): distribución de puntajes, envíos por veredicto,
    primer AC.
D5. Exportación de resultados (X12): CSV, JSON y PDF imprimible.
D6. Audit log (X15): todas las acciones del admin, con filtros por admin, acción y
    fecha.
D7. Importar problemas en formato italy_yaml y paquetes Polygon (T8, F9), además
    del paquete propio. Export/import completo de un concurso para archivo.

## BLOQUE E — Extras (al final, solo si todo lo anterior está completo)
E1. Detección de plagio (X8): reporte de similitud entre envíos por tarea
    (normalizando espacios, comentarios e identificadores), con vista lado a lado.
E2. Certificados/constancias en PDF (X13) con plantilla configurable.

## BLOQUE F — Cierre (F10)
F1. Pruebas de carga con k6 en la configuración de 2 vCPU: 500 concursantes
    concurrentes, ráfaga de envíos en los últimos minutos, 1,000 espectadores del
    ranking. Reporta p95/p99, envíos por minuto evaluados y cuántos concursantes
    soporta la máquina.
F2. Endurecimiento web: CSRF, CSP, rate limiting, cookies seguras, tamaño de
    archivos; pruebas automáticas de cada uno.
F3. `make test`, batería de seguridad y suite de ejemplo en verde.
F4. Documentación del admin completa en es/en: crear cada tipo de problema,
    configurar un concurso, operar el día del concurso.
F5. Guía de simulacro: un concurso corto con 3 problemas (Normal I/O, Interactive,
    Output only), preguntas, un anuncio, un envío invalidado, freeze y exportación
    de resultados, con checklist para comprobar cada punto.
F6. Resumen final: qué quedó completo, qué queda pendiente de verificar en el VPS
    real y las decisiones importantes de DECISIONS.md. AUDIT.md sin filas en
    "missing" ni "partial" (excepto las marcadas "hw").
