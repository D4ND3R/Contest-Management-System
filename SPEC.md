# PROYECTO: Contest Management System (CMS) de alto rendimiento

## 0. Reglas de sesión (léelas primero)
- SKILLS: NO uses la skill `immersive-web-design` ni la skill `master skill`. Sáltalas
  aunque su descripción parezca aplicar (landing, animaciones, liquid glass, scroll,
  hero, cursor, etc.). Este proyecto NO lleva diseño inmersivo: la UI es funcional,
  sobria y ultraligera. Al iniciar, confirma en una línea: "Skipping skills:
  immersive-web-design, master skill". Repite esta regla en cada fase.
- Referencia: https://github.com/cms-dev/cms (y su documentación en cms.readthedocs.io).
  Úsalo SOLO como referencia de arquitectura, modelo de datos y comportamiento esperado.
  NO copies código (es AGPL-3.0). Reimplementa desde cero.
- MODO AUTÓNOMO: no me pidas aprobación entre fases ni para planes. Planea
  internamente, ejecuta y continúa con la siguiente fase en cuanto se cumplan los
  criterios de salida. Solo detente si: (a) necesitas credenciales o acceso que no
  tienes, (b) una decisión es irreversible o destructiva fuera del repo, o (c) llevas
  3 intentos fallidos seguidos en el mismo problema. En cualquier otro caso, toma la
  decisión razonable, regístrala en DECISIONS.md y sigue.
- Lo primero que harás: guardar este mensaje completo como SPEC.md en la raíz del repo
  y crear PROGRESS.md y DECISIONS.md.
- Al terminar cada fase: `make test` en verde, actualiza PROGRESS.md (qué se hizo,
  pendientes), haz commit con el nombre de la fase y pasa a la siguiente. Al empezar
  cada fase relee SPEC.md y PROGRESS.md (el contexto puede haberse compactado).
- Si un criterio de salida no se puede verificar en este entorno (por ejemplo,
  isolate sin cgroups v2 o sin root), crea el test igualmente, márcalo como
  "pendiente de verificar en hardware real" en PROGRESS.md y continúa.
- Código, identificadores y commits en inglés; documentación de usuario en español
  e inglés (i18n).

## 1. Objetivo
Un sistema completo para organizar y juzgar concursos de programación estilo IOI/OMI
(con modo ICPC opcional). Debe tener paridad funcional con CMS y ser MUCHO más rápido.
La prioridad #1 es el rendimiento. La #2 es la exactitud y la seguridad del juez.

## 2. Objetivos de rendimiento (no negociables, medirlos en la Fase 10)
- Servidor del concursante: p95 < 15 ms y p99 < 40 ms en el servidor, con 3,000
  concursantes concurrentes en una máquina de 4 vCPU y 8 GB.
- Recepción de un envío (validar, guardar y encolar): p99 < 50 ms. Throughput > 200
  envíos/s en ráfaga.
- Overhead del sistema por testcase (sin contar ejecución): < 10 ms. Overhead de
  compilación, fuera del compilador mismo: < 50 ms.
- Actualización del ranking visible en los clientes: < 1 s después del scoring.
- Frontend del concursante: < 30 KB de JS total (gzip), sin frameworks SPA, First
  Contentful Paint < 300 ms en LAN.
- Arranque de cualquier servicio: < 1 s.

## 3. Stack
- Go (última versión estable). Monorepo con un binario por servicio (o un binario
  con subcomandos).
- PostgreSQL 16+ como fuente de verdad (pgx, SQL explícito con sqlc; sin ORM pesado).
  Migraciones versionadas.
- Redis 7 / Valkey para colas de trabajo (Streams con consumer groups), pub/sub de
  eventos, rate limiting y caché.
- Sandbox: `isolate` (github.com/ioi/isolate) con cgroups v2. Una caja por núcleo
  físico, con CPU pinning.
- Almacenamiento de archivos content-addressed (SHA-256): disco local o S3/MinIO,
  con caché local en cada worker (tmpfs/LRU). Nunca guardes blobs grandes en
  PostgreSQL.
- UI: Go `html/template` (o templ) + htmx + CSS plano minificado + SSE para eventos
  en vivo. Sin React, sin Tailwind runtime, sin animaciones decorativas.
- Observabilidad: logs estructurados (slog, JSON), métricas Prometheus, /healthz en
  cada servicio.
- Dev: docker compose (postgres, redis, minio). Producción: systemd sobre Linux
  (bare metal o VM) para los workers. Los workers NO pueden correr en serverless.

## 4. Servicios (equivalentes a los de CMS)
1. contest-web (CWS): portal del concursante.
2. admin-web (AWS): administración.
3. ranking-web (RWS): scoreboard público en vivo, desacoplado; recibe datos por push
   y puede correr en otra máquina.
4. dispatcher (EvaluationService + ScoringService): orquesta compilación y
   evaluación, reintentos, prioridades, scoring y reevaluación.
5. worker: ejecuta trabajos en isolate. Stateless y escalable horizontalmente.
6. checker/monitor: heartbeats de workers, detección de trabajos colgados y
   reencolado.
7. printing (opcional): cola de impresión con límites por usuario (PDF/texto → CUPS).
8. cmsctl (CLI): crear contest/usuarios, importar/exportar, reevaluar, dump/restore,
   bootstrap.

Todo el estado vive en Postgres + blob store. Cualquier servicio puede reiniciarse
sin perder trabajo (colas idempotentes, ack explícito, reintentos con backoff).

## 5. Modelo de datos (mínimo)
Contest, Task, Statement (PDF/HTML por idioma), Attachment, Dataset (varios por tarea,
uno "live"), Testcase (input/output digest, público/privado), Manager (checker,
grader, stubs, manager de comunicación), Language, User, Team, Participation (password
propio, IP permitidas, hidden, unrestricted, delay_time, extra_time, starting_time),
Submission, SubmissionFile, SubmissionResult (por dataset), Executable, Evaluation (por
testcase: outcome, texto, tiempo, wall time, memoria, exit status), UserTest +
UserTestResult, Token, Question, Announcement, Message, PrintJob, Admin (roles: all /
messaging / read-only), AuditLog.

## 6. Funcionalidad requerida (paridad con CMS + extras)
### Concursos
- Ventana global start/stop; modo per_user_time (cada usuario tiene su propia ventana
  al pulsar "Empezar"); extra_time y delay_time por participación; modo análisis tras
  el concurso; timezone por contest y por usuario.
- Restricción por IP/subred, autologin por IP opcional, bloqueo de login simultáneo
  opcional, contraseñas con argon2id.
- Idiomas de interfaz (es/en mínimo) y enunciados multi-idioma.
- Equipos (teams) visibles en el ranking.
### Tareas
- Tipos: Batch (stdin/stdout o archivos; con o sin grader/stub por lenguaje),
  OutputOnly, Communication (manager + N procesos del usuario vía FIFOs, en cajas
  separadas), TwoSteps.
- Checkers: diff exacto, diff ignorando espacios, comparador de reales con tolerancia,
  checker custom (protocolo CMS: stdout = score 0..1, stderr = mensaje; y
  compatibilidad testlib).
- Límites por dataset: tiempo CPU, wall time, memoria, tamaño de salida, número de
  procesos, tamaño del código fuente.
- Score types: Sum, GroupMin, GroupMul, GroupThreshold. Subtareas por regex/lista de
  testcases. Precisión de puntaje configurable.
- Score modes: max, max_subtask (mejor puntaje por subtarea entre envíos),
  max_tokened_last.
- Feedback: full o restricted (solo el primer testcase fallido de cada subtarea).
  Separación de puntaje público y privado.
- Varios datasets por tarea: cambiar el dataset live dispara reevaluación. Los datasets
  no-live pueden autojuzgarse en segundo plano para compararlos.
### Lenguajes (configurables, con flags y versiones fijas)
C11, C++17, C++20, Java, Python 3, PyPy 3, Pascal, Rust, Go, Kotlin, C#, Haskell.
Añadir un lenguaje = un archivo de configuración, sin tocar código.
### Envíos y evaluación
- Límite de número de envíos e intervalo mínimo entre envíos (por contest y por tarea),
  igual para user tests.
- User tests: ejecutar el código con input propio.
- Tokens: modos disabled/finite/infinite con initial, gen_number, gen_interval,
  gen_max, max y min_interval, a nivel contest y tarea. Usar un token revela el
  resultado completo.
- Estados en vivo por SSE: compilando → compilación fallida/ok → evaluando (x/N) →
  puntuado.
- Veredictos: AC, WA, puntaje parcial, TLE (CPU y wall), MLE, RE (con señal), OLE,
  CE (salida del compilador truncada), SE (error del sistema, alerta al admin).
- Reevaluación en tres niveles: recompile, reevaluate y rescore. Alcance por contest,
  tarea, dataset, usuario o envío. Sin downtime.
- Prioridades de cola: compilación > evaluación de envíos > user tests > datasets en
  segundo plano. Testcases de un mismo envío en paralelo entre workers.
### Comunicación
Preguntas/aclaraciones (con respuestas rápidas predefinidas), anuncios globales y
mensajes privados, con notificaciones en vivo por SSE.
### Admin (AWS)
CRUD de todo lo anterior; carga masiva de usuarios (CSV); vista de envíos con filtros,
descarga del fuente y diff entre envíos; estado de workers y colas en vivo;
reevaluaciones; exportar ranking (CSV/JSON); estadísticas por tarea; audit log de toda
acción de admin.
### Ranking (RWS)
Scoreboard en vivo con desglose por tarea y subtarea, historial de puntaje por usuario,
banderas y fotos de equipo opcionales, freeze configurable, ocultar usuarios hidden.
Snapshot JSON cacheado + deltas por SSE. Soportar 10,000 espectadores con una
instancia.
### Modo ICPC (opcional, por contest)
Veredicto binario, penalización por tiempo e intentos, freeze de la última hora y
descongelado con animación mínima en el ranking.
### Import/Export
Importar formato italy_yaml (task.yaml) y paquetes Polygon. Dump/restore completo
(JSON + blobs). Export del contest para archivo.

## 7. Seguridad del juez
- isolate con cgroups v2: sin red, filesystem de solo lectura salvo /box, límites de
  procesos, archivos y stack, usuario sin privilegios, entorno limpio.
- Compilación también dentro de la sandbox, con límites propios.
- Batería de programas maliciosos en tests: fork bomb, lectura de /etc/passwd, intento
  de red, escritura fuera de la caja, sleep infinito, consumo de memoria, salida
  gigante, `#include </dev/random>`, abuso de threads.
- Web: CSRF, cookies HttpOnly/SameSite/Secure, rate limiting por usuario/IP, validación
  de tamaño de archivos, CSP estricta, sin inline JS.
- Tiempo: CPU time vía cgroup, wall limit = max(2×TL, TL+1s). Documentar la
  recomendación de desactivar hyperthreading/turbo en los workers.

## 8. Reglas de calidad
- Tests unitarios por paquete, tests de integración con docker compose y suite
  end-to-end de soluciones de ejemplo (AC/WA/TLE/MLE/RE/CE por lenguaje) con veredicto
  esperado.
- Benchmarks de Go en rutas calientes; pruebas de carga con k6 en F10.
- Nada de N+1 queries; índices justificados; perfilar con pprof antes de optimizar.
- Cada fase termina con `make test` en verde y PROGRESS.md actualizado.

## 9. Plan de creación (ejecutar en orden, una fase por sesión)
F0  Fundaciones: monorepo, Makefile, docker compose, config, logging, CI, CLAUDE.md.
    Salida: `make dev` levanta todo; CI verde.
F1  Modelo de datos y blob store: esquema SQL, migraciones, sqlc, blobs por SHA-256.
    Salida: tests de repositorios y de deduplicación de blobs.
F2  Sandbox + worker: isolate, caché de testcases, pinning de núcleos.
    Salida: la batería de programas maliciosos da el veredicto correcto.
F3  Tipos de tarea, checkers y lenguajes.
    Salida: la suite de soluciones de ejemplo pasa en todos los lenguajes.
F4  Dispatcher: colas, prioridades, reintentos, score types/modes, reevaluación.
    Salida: matar un worker a mitad de evaluación no pierde trabajo.
F5  CWS (portal del concursante) con SSE.
    Salida: p95 < 15 ms en carga ligera; flujo completo de envío funcionando.
F6  AWS (admin): roles, audit log, vista de workers y colas.
    Salida: montar un concurso completo solo desde la UI.
F7  RWS (ranking): snapshot + deltas, freeze.
    Salida: 10k clientes SSE simulados sin degradación.
F8  Tokens, límites, user tests, Q&A, printing, modo análisis, modo ICPC.
    Salida: tests de reglas de tokens y límites en casos borde.
F9  Import/export: italy_yaml, Polygon, dump/restore.
    Salida: importar un problema real y restaurar un dump idéntico.
F10 Rendimiento y seguridad: k6, pprof, endurecimiento web.
    Salida: se cumplen todos los objetivos de la sección 2.
F11 Despliegue: systemd, guía de instalación, backups, runbook del día del concurso,
    guía de simulacro con usuarios reales.
    Salida: documentación completa y un simulacro ejecutable de punta a punta.

F2 y F3 son críticas: antes de salir de ellas, ejecuta la batería completa de programas
maliciosos y la suite de soluciones de ejemplo DOS veces seguidas; ambas corridas deben
dar veredictos idénticos. Escribe un resumen de resultados en PROGRESS.md.

## 10. Empieza ahora
1. Confirma "Skipping skills: immersive-web-design, master skill".
2. Guarda este mensaje como SPEC.md y crea PROGRESS.md y DECISIONS.md.
3. Ejecuta F0 a F11 en orden, sin detenerte entre fases salvo por las excepciones
   de la sección 0. Al terminar F11, dame un resumen final: qué quedó listo, qué
   quedó pendiente de verificar en hardware real y las decisiones más importantes
   de DECISIONS.md.
