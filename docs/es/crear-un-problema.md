# Crear un problema

Cada problema tiene **dos ventanas**, como pestañas arriba de su página:

- **Configuración**: importar un zip que lo rellena todo, las **opciones**
  del problema (tipo, límites, checker, puntuación... es decir, su
  `problem.yaml`, editado como un formulario) y los **archivos** uno por uno.
- **Casos de prueba**: agregar casos a mano o importarlos, verlos en una
  lista y hacer **envíos de prueba** contra ellos.

Puedes hacerlo todo de una vez con un zip, o por partes, en el orden que
quieras y en días distintos.

## Vocabulario

| Palabra | Qué es |
|---------|--------|
| **Problema** (tarea) | Lo que resuelven los concursantes: nombre, título, enunciado, reglas de envío. |
| **Dataset** | La forma de evaluar un problema: opciones, archivos de evaluación y casos. Un problema puede tener varios; el que evalúa los envíos es el **dataset en vivo**. Las dos ventanas muestran el dataset en vivo salvo que elijas otro. |
| **Caso de prueba** | Una entrada y su salida esperada, con un nombre (`1`, `2`, `g07`...). Un caso **público** muestra su resultado al concursante. |
| **Subtarea** | Un grupo de casos con puntos propios. |
| **Checker** | Programa o regla que decide si una salida es correcta. |
| **Archivo de evaluación** (manager) | Checker propio, grader, stub, interactor, manager o cabecera. |
| **Envío de prueba** | Una solución enviada por un organizador: se evalúa como un envío pero nunca cuenta. La **solución de referencia** es un envío de prueba correcto. |

Más términos en el [glosario](glosario.md).

## 1. Crear el problema

- Desde un concurso: **Problemas → Nuevo problema**, o desde el **Banco de
  problemas** (sin concurso, para agregarlo después).
- **Nombre**: corto y sin espacios (`suma`, `caminos`); se usa en las
  direcciones y en el nombre del archivo que envían los concursantes.
- **Título**: el nombre que se lee ("Suma de dos números").

Al crearlo se abre su ventana **Configuración**, con un dataset *Default*
en vivo: tipo *Batch* (entrada y salida estándar), 1 segundo, 256 MiB y un
punto por caso.

Arriba de las dos ventanas, una línea dice **qué falta**: enunciado, casos,
archivos que piden las opciones, puntuación, una solución de referencia con
el puntaje completo, estar en un concurso. Cada punto es un enlace a donde
se hace; cuando no falta nada dice *Listo*.

## 2. Ventana Configuración

### Importar un paquete (zip)

**Importar un paquete**: elige el zip y **Revisar el paquete**. Un paquete
tiene `problem.yaml`, `statement/`, `tests/`, el checker, `graders/`,
`attachments/` y `solutions/` ([formato](paquete-de-problema.md)); también
sirven los paquetes de CMS (`task.yaml`) y de Polygon.

La vista previa muestra lo que trae (tipo, límites, subtareas, casos,
enunciados, archivos, soluciones) y los problemas que tenga, **sin cambiar
nada**. **Rellenar el problema** reemplaza sus opciones, enunciados,
archivos, ejemplos y casos por los del paquete. Si marcas *Ejecutar las
soluciones de referencia*, las soluciones de `solutions/` se envían como
envíos de prueba y sus resultados aparecen en la ventana Casos de prueba.

Si el paquete llama al problema con un nombre que ya usa otro problema, este
conserva el suyo (la vista previa lo avisa).

### Opciones (problem.yaml)

Un solo formulario con todo lo que describe `problem.yaml`, en grupos:

- **Problema**: nombre, título y **tipo de problema**: *Batch*
  (entrada/salida estándar o archivos, con o sin grader), *OutputOnly*
  (solo salida), *TwoSteps* (dos pasos), *Communication* (comunicación),
  *Interactive* (interactivo). Al elegir el tipo aparecen sus opciones.
- **Límites**: tiempo (segundos de CPU), tiempo real (vacío: el doble, o el
  límite más 1 s), memoria (MiB), salida (MiB), procesos, tamaño del
  código.
- **Checker** (Batch, OutputOnly, TwoSteps): *ignorar espacios* (lo
  normal), *exacto*, *reales con tolerancia* (absoluta y relativa), o un
  **checker propio** (protocolo CMS o testlib) que se sube en Archivos.
- **Puntuación y subtareas** (con el puntaje máximo calculado):
  - *Sum*: puntos por caso (10 casos × 10 puntos = 100);
  - *GroupMin*: cada subtarea vale sus puntos solo si **todos** sus casos
    son correctos (la regla de la IOI);
  - *GroupMul*: los puntos de la subtarea se multiplican por el peor
    resultado de sus casos (checkers con puntaje parcial);
  - *GroupThreshold*: la subtarea puntúa si cada caso supera un umbral.

  El editor de subtareas tiene una fila por subtarea: **puntos** y **qué
  casos** (nombres que cumplen una expresión regular como `^g.*`, una lista
  marcada a mano o "los siguientes N casos"); la última fila vacía agrega
  una. Debajo dice el total y qué casos no están en ninguna subtarea. Con
  GroupMin o GroupMul, **cortocircuito** deja de evaluar una subtarea en
  cuanto un caso da 0.
- **Envíos y resultados**: archivos del envío (`suma.%l`: el `%l` se cambia
  por la extensión del lenguaje), enunciados principales, modo de
  puntuación (mejor por subtarea, mejor envío, el último...),
  retroalimentación, mensajes del checker, precisión y lenguajes
  permitidos.
- **Reglas del concurso** (plegado): tokens y límites de envíos del
  problema.

**Guardar** guarda todo junto. Los envíos ya evaluados conservan su
resultado hasta que los reevalúes (sección *Reevaluar*, al final).

**Editar problem.yaml como texto** (plegado) muestra las mismas opciones en
el formato de los paquetes; puedes editarlas ahí y **Guardar problem.yaml**.
Se revisan igual que en un paquete: un error (una clave desconocida, un
límite fuera de rango, una subtarea que no encuentra casos) no cambia nada
y se muestra arriba del texto. `public_tests` dice qué casos son públicos.
**Descargar problem.yaml** baja el archivo.

### Archivos

Una tabla con todos los archivos del problema y qué es cada uno:
enunciados (con **editar**, **PDF**, **eliminar**), archivos de evaluación
(checker, grader, stub, interactor, manager, cabeceras) y adjuntos para los
concursantes. Si las opciones piden un archivo que falta (por ejemplo
`checker` con un checker propio), aparece en amarillo.

Para subir, elige uno o varios archivos y **Subir**. *Es* dice qué son:

- **según su nombre** (lo normal): `checker.cpp`, `grader.cpp`, `stub.py`,
  `interactor.cpp`, `manager.c` y las cabeceras (`.h`) son archivos de
  evaluación; `es.md`, `en.pdf`... son enunciados en ese idioma; lo demás
  es un adjunto;
- **un enunciado** (con su idioma en *Idioma de un enunciado*), **un
  archivo de evaluación** o **un adjunto**, para decidirlo tú.

Un archivo con el mismo nombre se reemplaza. **Escribir un enunciado** abre
el editor de Markdown/LaTeX con vista previa ([enunciados](enunciados.md)).
Si cambias un enunciado durante el concurso, los concursantes reciben un
aviso y la página se recarga sola.

### Datasets (plegado)

La lista de datasets del problema: cuál está **en vivo**, **comparar con el
vivo**, **poner en vivo**, **eliminar**; **renombrar** el que se muestra,
**Nuevo dataset** (vacío o copiando otro) e **importar un paquete como
dataset nuevo**. Ver la sección 5.

## 3. Ventana Casos de prueba

### Agregar casos

**Un caso**:

1. **Nombre**: se propone el siguiente número; puedes poner otro (letras,
   dígitos, `_`, `.`, `-`). Un nombre que ya existe reemplaza ese caso.
2. **Entrada**: escríbela en el recuadro o elige un archivo.
3. **Salida**, una de tres:
   - *escrita o subida*;
   - *escrita por la solución de referencia*: el sistema ejecuta un envío
     de prueba (se elige en la lista; primero los que tienen el puntaje
     completo) con esa entrada y guarda lo que imprime. Solo en *Batch*;
   - *vacía*: para problemas interactivos o checkers que solo leen la
     entrada.
4. **Público**: el concursante verá el resultado de este caso.
5. **Agregar caso**.

Los textos escritos se guardan con saltos de línea de Unix y un salto al
final.

**Generar con un programa** crea muchos casos a la vez:

1. Un **generador**: un programa (en cualquier lenguaje configurado) que lee
   **una línea de parámetros** por la entrada estándar y escribe **una
   entrada completa** por la salida estándar.
2. **Parámetros, un caso por línea**: el generador se ejecuta una vez por
   línea no vacía (hasta 500).
3. **Prefijo** y **primer número**: prefijo `g`, primer número 8 y 3 líneas
   crean `g08`, `g09`, `g10`.
4. **Salida**: *escrita por la solución de referencia* o *vacía*.

Ejemplo en Python (`n` y una semilla por línea, por ejemplo `1000 7`):

```python
import random
n, seed = map(int, input().split())
random.seed(seed)
print(n)
print(*[random.randint(1, 10**9) for _ in range(n)])
```

Ejemplo en C++ (una línea con `n`):

```cpp
#include <cstdio>
#include <random>
int main() {
    long long n; std::scanf("%lld", &n);
    std::mt19937_64 rng(n);
    std::printf("%lld %lld\n", (long long)(rng() % 2000000001) - 1000000000,
                               (long long)(rng() % 2000000001) - 1000000000);
}
```

Todo corre en los **jueces** (en el mismo sandbox que los envíos), nunca en
el servidor de administración. El generador tiene al menos 10 s, 1 GiB de
memoria y 256 MiB de salida; la solución de referencia usa los límites del
dataset. Mientras tanto la lista muestra cada caso pendiente ("el generador
está escribiendo la entrada…", "la solución de referencia está escribiendo
la salida…") y se actualiza sola; si algo falla, el caso queda como
*falló* con el motivo y **olvidar los fallidos** lo quita.

**Importar casos desde un zip**: sube un zip e indica cómo se llaman las
entradas y las salidas (`*.in`/`*.out`, `input*.txt`/`output*.txt`...).
Los pares se forman por la parte que reemplaza al `*`. *Sobrescribir
existentes* reemplaza los casos con el mismo nombre.

### La lista de casos

Una tabla con cada caso: número, **nombre** (enlace a su página, con la
entrada y la salida a la vista y **anterior**/**siguiente**), tamaño de la
entrada y de la salida (enlaces para descargarlas), **público** (clic para
cambiarlo), **ejemplo** (*usar como ejemplo* lo agrega al enunciado) y
**eliminar**. **Borrar todos los casos** vacía la lista (por ejemplo, antes
de importar otro juego).

A la derecha, una columna por cada uno de los **últimos cuatro envíos de
prueba**, con el veredicto de cada caso (AC, PA, WA, TLE, MLE, RE...) y su
tiempo; al pasar el ratón se ve el mensaje del checker. Las soluciones de un
paquete se ven con su nombre (`wa_resta.c`).

### Envíos de prueba

Elige el lenguaje, sube el archivo y **Probar**. El envío se evalúa en
todos los datasets del problema como un envío más, pero nunca aparece en
rankings ni estadísticas. La página vuelve a la
lista, que se actualiza sola hasta que el envío termina. La tabla de envíos
de prueba muestra el puntaje de cada uno (verde completo, amarillo parcial,
rojo cero); el número abre el detalle completo.

Sirven para comprobar que el problema está bien (la línea de lo que falta
pide un envío con el puntaje completo), para escribir las salidas de casos
nuevos y para ver que las soluciones incorrectas o lentas fallan como se
espera. Si cambias los casos, **vuelve a enviar**: el resultado anterior
se calculó con los casos de entonces.

### Ejemplos

Al final de la ventana: los ejemplos que muestran todos los enunciados del
problema (en la página y en el PDF). Se agregan desde un caso (**usar como
ejemplo**) o escribiéndolos; cada uno puede llevar una explicación en
Markdown, y **subir**/**bajar** los ordena.

## 4. Ponerlo en el concurso

Si lo creaste desde el concurso ya está en él. Si no, en el concurso:
**Problemas → Agregar un problema existente**. El orden de la lista es el
de las letras (A, B, C...); las flechas lo cambian. Durante el concurso,
**cerrar envíos** detiene los envíos a un solo problema.

## 5. Cambiar un problema ya usado

Cambiar las opciones o los casos del dataset en vivo afecta a los envíos
nuevos; los ya evaluados conservan su resultado hasta que los reevalúes.
Para preparar un cambio sin tocar el resultado actual:

1. En **Datasets**, **Nuevo dataset** copiando el actual.
2. El selector **Dataset**, arriba de las ventanas, muestra la copia (un
   aviso recuerda que no es el que está en vivo); cambia ahí opciones,
   archivos o casos. Marca *Evaluar los envíos nuevos en este dataset en
   segundo plano* si quieres compararlos.
3. **Comparar con el vivo** muestra qué puntajes cambiarían.
4. **Poner en vivo** cuando estés seguro: los puntajes se recalculan.
