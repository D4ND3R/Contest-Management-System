# Paquetes de problema

Un paquete de problema es un zip con todo lo que necesita un problema. El
panel de administración lo importa (**Problemas → Importar un paquete**),
muestra lo que encontró y cada error por archivo antes de crear nada, ejecuta
las soluciones de referencia e informa si cada una obtiene el veredicto que
anuncia su nombre. Cualquier problema se exporta en el mismo formato
(**Exportar como paquete** en la página del problema o del dataset), así que
un problema pasa de una instalación a otra sin cambios.

Hay ejemplos completos, uno por tipo de problema, en
[`docs/examples/packages/`](../examples/packages/): comprime una de las
carpetas e impórtala.

## Estructura

```
problem.yaml             configuración (obligatoria)
statement/es.md          un enunciado por idioma: <idioma>.md|.tex|.pdf|.html|.txt
statement/en.tex           (Markdown y LaTeX los dibuja y compone el CMS)
statement/examples/01.in ejemplos que se muestran en los enunciados: NOMBRE.in,
statement/examples/01.out  NOMBRE.out y una explicación opcional NOMBRE.md
tests/01.in              casos: <nombre>.in + <nombre>.out (o .ans)
tests/01.out
checker.cpp              opcionales: checker, interactor, manager
interactor.cpp             (binario, .c o .cpp; testlib.h puede ir junto a ellos)
manager.cpp
graders/grader.cpp       graders, stubs y encabezados por lenguaje
graders/stub.py
attachments/ejemplo.zip  archivos que los concursantes pueden descargar
solutions/ac_main.cpp    soluciones de referencia: <veredicto esperado>_<nombre>.<ext>
solutions/tle_bruta.py
```

Los archivos también pueden estar dentro de una carpeta (`suma/problem.yaml`,
…), como al comprimir una carpeta. Se ignoran los archivos que agregan los
compresores (`__MACOSX`, `.DS_Store`); cualquier otro archivo desconocido se
informa como advertencia y se ignora.

Los nombres de los casos pueden tener letras, dígitos, `_`, `.` y `-`; se
ordenan por nombre (las subtareas por cantidad siguen este orden).

## problem.yaml

Las claves desconocidas son errores, así que un error de dedo nunca pasa
inadvertido. Los tiempos van en segundos y los tamaños en MiB (el tamaño del
código en KiB).

| clave | predeterminado | significado |
|-------|----------------|-------------|
| `format` | 1 | versión del formato |
| `name` | — | nombre del problema, se usa en las URL (letras, dígitos, `_ . -`) |
| `title` | name | lo que ven los concursantes |
| `type` | `batch` | `batch`, `output_only`, `interactive`, `communication`, `two_steps` |
| `time_limit` | — | segundos de CPU (obligatorio salvo en `output_only`) |
| `memory_limit` | — | MiB (obligatorio salvo en `output_only`) |
| `wall_time_limit` | máx(2×TL, TL+1 s) | segundos de reloj |
| `output_limit` | 64 | MiB |
| `process_limit` | 1 | procesos/hilos |
| `source_size_limit` | ninguno | KiB |
| `languages` | los del concurso | lenguajes permitidos (ids de `config/languages/`) |
| `submission_format` | `<name>.%l` | archivos del envío; `%l` = extensión del lenguaje |
| `primary_statements` | ninguno | idiomas de enunciado marcados como oficiales |
| `feedback` | `full` | `full` o `restricted` (primer fallo por subtarea) |
| `score_mode` | `max_subtask` | `max_subtask`, `max`, `max_tokened_last` |
| `score_precision` | 0 | decimales |
| `dataset` | `Default` | descripción del dataset |

Por tipo:

| clave | tipos | significado |
|-------|-------|-------------|
| `input_file`, `output_file` | batch | E/S por archivos en lugar de stdin/stdout |
| `compilation` | batch, communication | batch: `alone` o `grader` (graders/grader.<ext>); communication: `stub` (predeterminado) o `alone` |
| `checker` | batch, output_only, two_steps | `white_diff` (predeterminado), `exact`, `float`, `custom` (protocolo CMS), `testlib` |
| `float_tolerance` | igual | `{absolute: 1e-6, relative: 1e-6}` |
| `checker_time_limit`, `checker_memory_limit` | igual | límites del checker propio (10 s, 1024 MiB) |
| `output_pattern`, `merge_previous` | output_only | nombres de archivo (`output_%s.txt`), conservar la mejor salida anterior de los archivos faltantes |
| `manager` | two_steps | nombre del código del manager (`manager`) |
| `processes`, `user_io`, `limits_mode` | communication | de 1 a 4 procesos; `fifos` o `std_io`; `per_process` o `total` |
| `manager_time_limit`, `manager_memory_limit` | communication | límites del manager |
| `interactor_time_limit`, `interactor_memory_limit` | interactive | límites del interactor (nunca se cobran al concursante) |

Puntuación:

```yaml
scoring: sum             # cada caso vale points_per_test
points_per_test: 10      # predeterminado: 100 repartidos entre los casos

scoring: group_min       # group_min, group_mul o group_threshold
subtasks:
  - points: 30
    tests: "1_.*"        # una regex sobre los nombres de los casos,
  - points: 30
    tests: [2_01, 2_07]  # una lista de nombres,
  - points: 40
    tests: 5             # o los siguientes N casos en orden de nombre
    threshold: 0.5       # solo group_threshold
public_tests: ["1_01"]   # regex de los casos cuyo resultado ven los concursantes
```

Un caso que no está en ninguna subtarea se informa como advertencia (nunca
cuenta).

## Checkers, interactores y managers

- `checker` (binario, `checker.c` o `checker.cpp`) se usa con
  `checker: custom` (protocolo CMS: `checker entrada salida_correcta
  salida_concursante`, puntaje en [0, 1] en stdout y mensaje en stderr) o
  `checker: testlib` (orden de argumentos y códigos de salida de testlib).
- `interactor` (problemas interactivos) se ejecuta como `interactor input
  output answer`, se comunica con el concursante por su entrada/salida
  estándar y decide el veredicto con los códigos de salida de testlib.
- `manager` (problemas de comunicación) habla con los procesos del
  concursante por FIFOs y escribe el puntaje en stdout; `manager.<ext>`
  (problemas de dos pasos) se compila junto con el envío.
- Los graders, stubs y encabezados van en `graders/` (`grader.cpp`,
  `stub.py`, `task.h`, …); `testlib.h` puede ir en la raíz o en `graders/`.

Los archivos faltantes se informan antes de importar (por ejemplo
`checker: testlib` sin checker).

## Soluciones de referencia y validación

Cada archivo de `solutions/` empieza con el veredicto que debe obtener:

| prefijo | la solución debe |
|---------|------------------|
| `ac_` | obtener el puntaje completo |
| `pa_` | obtener más de 0 y menos del máximo |
| `wa_`, `tle_`, `mle_`, `re_` | no obtener el puntaje completo, con respuesta incorrecta / tiempo excedido / memoria excedida / error de ejecución en algún caso |
| `ce_` | no compilar |
| `any_` | nada (se ejecuta y se muestra) |

El lenguaje sale de la extensión: el primer lenguaje del problema (o, sin
`languages`, de la configuración) que la usa. En problemas de solo salida una
solución es una carpeta (`solutions/ac_todas/output_01.txt`, …) o un zip.

Después de importar, las soluciones se ejecutan con el probador de problemas
en todos los datasets (nunca son envíos) y el **Reporte de validación**
muestra, por solución y dataset, ✓ o ✗ con lo que ocurrió (`ac`,
`pa 30/100 wa`, `tle`, …). Un checker, interactor o manager que no compila
aparece como error del sistema. El problema queda fuera de todo concurso
salvo que se haya elegido uno, y un paquete importado como dataset nuevo no
queda en vivo: publícalo cuando el reporte esté todo en ✓. **Ejecutarlas de
nuevo** vuelve a evaluar las soluciones (por ejemplo, tras cambiar límites).

## Opciones de importación

- **Un problema nuevo**, opcionalmente al final de un concurso. Si ya existe
  un problema con el mismo nombre se informa; impórtalo como dataset de ese
  problema o renómbralo.
- **Un dataset nuevo (no en vivo) de un problema existente**: límites, tipo,
  puntuación, casos y managers; se conservan los enunciados, adjuntos y la
  configuración del problema.

No se escribe nada hasta confirmar la vista previa; la importación es una
sola transacción.

## Paquetes de otros sistemas

Otros dos formatos se convierten al importar, tanto desde el panel de
administración como con `cmsctl task-import`; la vista previa indica
**convertido desde el formato …** y avisa de lo que no se pudo convertir.
Hay ejemplos en [`docs/examples/other-formats/`](../examples/other-formats/).

**CMS italy_yaml** (una carpeta con `task.yaml`):

| italy_yaml | se convierte en |
|------------|-----------------|
| `task.yaml`: `name`, `title`, `time_limit` (s), `memory_limit` (MiB), `infile`/`outfile` (por defecto `input.txt`/`output.txt`; vacío = entrada/salida estándar), `output_only`, `public_testcases` (`all` o una lista de índices), `n_input` | la misma configuración |
| `input/inputN.txt`, `output/outputN.txt` | testcases `000`, `001`, … |
| líneas `# ST: puntos` de `gen/GEN` (o los pares de `score_type_parameters`) | subtareas (GroupMin; GroupMul si `score_type` lo indica) |
| sin subtareas | Sum con `total_value` / `n_input` puntos por testcase (100 en total por defecto) |
| `check/checker` o `cor/correttore` (binario o `.c`/`.cpp`) | checker propio (protocolo de CMS: resultado en stdout, mensaje en stderr) |
| `check/manager` | tarea Communication con ese manager |
| `sol/grader.*`, `sol/stub.*`, headers | graders / stubs por lenguaje |
| `sol/soluzione.*` (o `solution`, `sol`) | solución de referencia que debe ser aceptada; las demás fuentes de `sol/` se ejecutan sin veredicto esperado |
| `statement/statement.pdf` o `testo/testo.pdf` | enunciado en `primary_language` (italiano por defecto) |
| `att/*` | adjuntos; los pares de ejemplo (`input0.txt`/`output0.txt`, `NOMBRE.in`/`NOMBRE.out`) pasan a ser los ejemplos del enunciado |

**Polygon** (el paquete completo con `problem.xml`, descargado con los tests
generados, o después de correr `doall.sh`):

| Polygon | se convierte en |
|---------|-----------------|
| testset `tests`: límite de tiempo (ms), de memoria (bytes), archivos de entrada/salida | la misma configuración, en segundos y MiB |
| `tests/01`, `tests/01.a`, … | testcases `01`, `02`, …; los tests marcados como ejemplo son públicos |
| grupos de tests con puntos | subtareas (GroupMin) con los tests del grupo |
| puntos por test sin grupos | puntos por testcase (una subtarea por test si difieren) |
| fuente del checker (testlib) | checker `testlib`; los recursos `.h` (`testlib.h`) van con él |
| interactor | tarea Interactive |
| soluciones con etiqueta `main`/`accepted`, `wrong-answer`, `time-limit-exceeded`, `memory-limit-exceeded`, … | soluciones de referencia con el veredicto esperado correspondiente (el resto se ejecuta sin veredicto esperado) |
| `statement-sections/<idioma>/` (`legend.tex`, `input.tex`, `output.tex`, `notes.tex`, ...) | un enunciado LaTeX por idioma, que el CMS dibuja y compone; `example.NN`/`example.NN.a` (si no, los tests de ejemplo) pasan a ser los ejemplos |
| otros enunciados | el PDF si lo hay; si no, el HTML (sin sus imágenes) |

Un paquete sin sus tests generados se rechaza con un mensaje que lo
explica: descarga el paquete **completo** desde Polygon.

## Exportación

La exportación escribe `problem.yaml` a partir del problema y del dataset
elegido (el que está en vivo, por omisión), los enunciados con sus ejemplos, casos, managers
(checker, interactor y manager en la raíz, el resto en `graders/`), adjuntos
y la versión más reciente de cada solución de referencia importada con un
paquete. Al importarla de nuevo se obtiene un problema idéntico.
