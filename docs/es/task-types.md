# Tipos de tarea y checkers

`task_type` y `task_type_params` (JSON) del dataset determinan cómo se
compilan y evalúan los envíos.

## Batch
El programa lee un caso de prueba y escribe la respuesta.

| parámetro | por defecto | significado |
|---|---|---|
| `compilation` | `alone` | `grader`: compila el manager `grader.<ext>` (y cabeceras) con el envío |
| `input_file` / `output_file` | stdin / stdout | nombres de archivo para E/S por archivos |
| `checker` | `white_diff` | `exact`, `white_diff`, `float`, `custom`, `testlib` |
| `float_abs_tol`, `float_rel_tol` | 0 | tolerancias del comparador `float` |
| `checker_time_limit_ms`, `checker_memory_bytes` | 10 s, 1 GiB | límites del checker propio |

Los graders se proveen por lenguaje como managers (`grader.c` + `task.h`,
`grader.cpp`, `grader.java`, `grader.py`, …); el concursante implementa las
funciones. Los graders funcionan con E/S estándar o con `input_file` /
`output_file`.

## OutputOnly
El concursante envía `output_<codename>.txt` por caso (`output_pattern`
cambia el nombre), uno por uno o todos juntos en un zip (cada archivo del
zip debe ser uno de los nombres esperados). Se permiten envíos parciales:
las salidas faltantes valen cero, salvo que se active `merge_previous`, en
cuyo caso cada salida faltante se toma del envío anterior del concursante
con mejor resultado en ese caso. Si el formato de envío de la tarea está
vacío, los nombres esperados salen de los casos del dataset activo. Mismos
checkers que Batch.

## TwoSteps
El envío se compila con el manager `manager.<ext>` en un solo ejecutable
que corre dos veces: `argv[1]="0"` lee el caso y escribe un mensaje;
`argv[1]="1"` lee sólo el mensaje (en otra sandbox) y escribe la respuesta,
que se verifica.

## Communication
Un manager (binario `manager` o `manager.cpp`, en sandbox) conversa con
`num_processes` procesos del concursante (cada uno en su sandbox) mediante
un par de FIFOs por proceso. Manager: `argv = u2m_0 m2u_0 [u2m_1 m2u_1 ...]`,
stdin = entrada del caso, stdout = puntaje en [0,1], stderr = mensaje.
Proceso del concursante: `argv = m2u u2m [índice]` (`user_io: fifos`) o
stdin/stdout sobre las FIFOs (`user_io: std_io`). `compilation: stub`
compila el `stub.<ext>` del manager junto con el envío.

Límites: cada proceso del concursante recibe los límites de tiempo y
memoria de la tarea (`limits_mode: per_process`, por defecto); con
`limits_mode: total` además la suma del tiempo de CPU y de la memoria
máxima de los procesos debe caber en los límites. Hay stubs por lenguaje
(`stub.c`, `stub.cpp`, `stub.py`, `stub.java`, …).
`manager_time_limit_ms` / `manager_memory_bytes` limitan al manager.

## Interactive
Estilo ICPC / Codeforces: el manager `interactor` (binario, `.c` o `.cpp`;
se permite testlib subiendo `testlib.h` como otro manager) conversa con UN
proceso del concursante. La salida estándar del interactor es la entrada
estándar del concursante y viceversa (pipes anónimos entre dos sandboxes).
Se ejecuta como `interactor <entrada> <salida> <respuesta>` en su propia
sandbox con sus propios límites (`interactor_time_limit_ms`, por defecto
10 s + TL; `interactor_memory_bytes`, por defecto 1 GiB); su tiempo de CPU
nunca se le cobra al concursante. Decide el veredicto con los códigos de
salida de testlib (0 correcto, 1/2/4 incorrecto, 3 fallo del juez, 7 /
16+n parcial) y la primera línea de su stderr es el mensaje.

Veredictos: si el concursante excede un límite recibe ese veredicto (un
programa que no hace flush bloquea a ambos lados y termina por el límite de
tiempo real: TLE); un fallo del programa es error de ejecución; en otro
caso decide el interactor — también cuando el concursante termina antes de
tiempo (el interactor ve fin de archivo) o muere por SIGPIPE porque el
interactor ya terminó. Si el interactor falla o reporta un fallo, es error
del sistema.

## Checkers
- `white_diff` (por defecto): línea por línea, ignorando la cantidad de
  espacios entre tokens y líneas en blanco al final (semántica de CMS).
- `exact`: byte por byte.
- `float`: token por token; los números pueden diferir en `float_abs_tol` o
  `float_rel_tol × |esperado|`.
- `custom` (protocolo CMS): manager `checker` (binario), `checker.cpp` o `checker.c`
  (se compila una vez por worker), se ejecuta `checker entrada correcta
  concursante`; la primera línea de stdout es el puntaje en [0,1] y la de
  stderr el mensaje (`translate:success|wrong|partial` son mensajes estándar).
- `testlib`: mismas convenciones, se ejecuta `checker entrada concursante
  correcta`; código de salida 0 = correcto, 1/2/4 = incorrecto, 3 = fallo del
  checker (error del sistema), 7 = parcial (`points X`: fracción si ≤ 1,
  porcentaje si no), 16+n = n por ciento.
