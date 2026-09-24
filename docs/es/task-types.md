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

## OutputOnly
El concursante envía `output_<codename>.txt` por caso (`output_pattern`
cambia el nombre). Mismos checkers que Batch.

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

## Checkers
- `white_diff` (por defecto): línea por línea, ignorando la cantidad de
  espacios entre tokens y líneas en blanco al final (semántica de CMS).
- `exact`: byte por byte.
- `float`: token por token; los números pueden diferir en `float_abs_tol` o
  `float_rel_tol × |esperado|`.
- `custom` (protocolo CMS): manager `checker` (binario) o `checker.cpp`
  (se compila una vez por worker), se ejecuta `checker entrada correcta
  concursante`; la primera línea de stdout es el puntaje en [0,1] y la de
  stderr el mensaje (`translate:success|wrong|partial` son mensajes estándar).
- `testlib`: mismas convenciones, se ejecuta `checker entrada concursante
  correcta`; código de salida 0 = correcto, 1/2/4 = incorrecto, 3 = fallo del
  checker (error del sistema), 7 = parcial (`points X`: fracción si ≤ 1,
  porcentaje si no), 16+n = n por ciento.
