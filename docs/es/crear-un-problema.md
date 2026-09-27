# Crear un problema

Un problema se arma **por partes, en el orden que quieras y en días
distintos**: primero el nombre, otro día el enunciado, luego los casos uno
por uno, la solución de referencia, las subtareas... No hace falta un zip ni
un paquete (aunque también se puede, ver
[paquetes de problema](paquete-de-problema.md)).

## Vocabulario

| Palabra | Qué es |
|---------|--------|
| **Problema** (tarea) | Lo que resuelven los concursantes: nombre, título, enunciado, reglas de envío. |
| **Dataset** | La forma de evaluar un problema: tipo, límites, casos, checker, puntuación. Un problema puede tener varios; el que evalúa los envíos es el **dataset en vivo**. |
| **Caso de prueba** | Una entrada y su salida esperada. Tiene un nombre (`1`, `2`, `g07`...). Un caso **público** muestra su resultado al concursante. |
| **Subtarea** | Un grupo de casos con puntos propios. |
| **Checker** | Programa o regla que decide si una salida es correcta. |
| **Manager** | Archivo auxiliar del dataset: checker propio, grader, stub, interactor, headers. |
| **Solución de referencia** | Una solución del autor enviada con el **probador de problemas**: se evalúa como un envío pero nunca cuenta. |

Más términos en el [glosario](glosario.md).

## 1. Crear el problema

- Desde un concurso: **Problemas → Nuevo problema**, o desde el **Banco de
  problemas** (sin concurso, para agregarlo después).
- **Nombre**: corto y sin espacios (`suma`, `caminos`); se usa en las
  direcciones y en el nombre del archivo que envían los concursantes.
- **Título**: el nombre que se lee ("Suma de dos números"). Si lo dejas
  vacío se usa el nombre.

Al crearlo se crea también su dataset *Default* (en vivo), con tipo *Batch*
(entrada y salida estándar), 1 segundo, 256 MiB y un punto por caso.

## 2. La lista de preparación

La página del problema empieza con **Preparación**: una lista numerada de lo
que un problema necesita, cada punto con un enlace a donde se hace:

1. **Nombre y título**.
2. **Enunciado** (cuántos idiomas).
3. **Casos de prueba** (cuántos hay en el dataset en vivo).
4. **Tipo, límites y checker** (falta si la configuración necesita un
   manager que no se ha subido, por ejemplo un checker propio).
5. **Puntuación y subtareas** (el puntaje máximo).
6. **Solución de referencia con el puntaje completo**: al menos una
   ejecución del probador obtuvo el máximo en el dataset en vivo.
7. **En un concurso**.

Los puntos hechos se marcan en verde y los pendientes en amarillo. Debajo,
pestañas llevan a cada sección de la página: Enunciados, Datasets y casos,
Ejemplos, Adjuntos, Probador de problemas, Configuración.

## 3. El enunciado

**Enunciados → Escribir un enunciado**: elige el idioma (`es`, `en`...),
escribe en **Markdown** o **LaTeX** y guarda; la vista previa está al lado.

- Fórmulas: `$a+b$` en línea, `$$\sum_{i=1}^n a_i$$` en su propio renglón.
- Secciones típicas: `## Entrada`, `## Salida`, `## Subtareas`,
  `## Ejemplos` (los ejemplos se insertan solos, ver abajo).
- Los concursantes leen el enunciado en la página y lo descargan en PDF; el
  PDF lleva los límites y los ejemplos.
- También puedes **subir** un PDF, Markdown, LaTeX o HTML ya hecho.
- Si cambias un enunciado durante el concurso, los concursantes reciben un
  aviso y la página se recarga sola.

Detalles en [enunciados](enunciados.md).

## 4. Los casos de prueba

En la página del problema, **Datasets y casos → Default** (o el dataset que
quieras). La página del dataset tiene pestañas: *Tipo, límites y checker*,
*Puntuación y subtareas*, *Managers*, *Agregar casos*, *Casos de prueba*.

### Uno por uno

**Agregar casos → Un caso**:

1. **Nombre**: se propone el siguiente número; puedes poner otro (letras,
   dígitos, `_`, `.`, `-`). Usar un nombre que ya existe reemplaza ese caso.
2. **Entrada**: escríbela en el recuadro o elige un archivo.
3. **Salida**, una de tres:
   - *escrita o subida*: escríbela o elige un archivo;
   - *escrita por la solución de referencia*: el sistema ejecuta tu
     solución (una ejecución del probador, se elige en la lista) con esa
     entrada y guarda lo que imprime. Solo en problemas *Batch*;
   - *vacía*: para problemas interactivos o checkers que solo leen la
     entrada.
4. **Público**: el concursante verá el resultado de este caso uno por uno.
   Los casos no públicos solo cuentan en los bloques de subtarea.
5. **Agregar caso**.

Los textos escritos en el recuadro se guardan con saltos de línea de Unix y
un salto al final.

### Con un generador

**Agregar casos → Generar con un programa** crea muchos casos a la vez:

1. Un **generador**: un programa (en cualquier lenguaje configurado) que lee
   **una línea de parámetros** por la entrada estándar y escribe **una
   entrada completa** por la salida estándar.
2. **Parámetros, un caso por línea**: cada línea no vacía es un caso. El
   generador se ejecuta una vez por línea (hasta 500 líneas).
3. **Prefijo** y **primer número** del nombre: prefijo `g` y primer número
   8 con 3 líneas crean `g08`, `g09`, `g10`.
4. **Salida**: *escrita por la solución de referencia* (lo normal) o
   *vacía*.

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

Mientras se generan, la sección **Casos de prueba** muestra cada caso
pendiente ("el generador está escribiendo la entrada…", "la solución de
referencia está escribiendo la salida…") y se actualiza sola. Si algo falla
(el generador no compila, se pasa del tiempo, la solución falla), el caso
queda como *falló* con el motivo; **olvidar los fallidos** limpia la lista.

Todo corre en los **jueces** (en el mismo sandbox que los envíos), nunca en
el servidor de administración. El generador tiene al menos 10 s, 1 GiB de
memoria y 256 MiB de salida; la solución de referencia usa los límites del
dataset.

### Muchos casos desde un zip

**Muchos casos desde un archivo zip** (plegado): sube un zip e indica cómo
se llaman las entradas y las salidas (`*.in`/`*.out`,
`input*.txt`/`output*.txt`...). Los pares se forman por la parte que
reemplaza al `*`.

### En la tabla de casos

Cada caso muestra si es público (clic para cambiarlo), enlaces para
descargar la entrada y la salida, **usar como ejemplo** (aparece en el
enunciado, en la página y en el PDF) y **borrar**.

## 5. Tipo, límites y checker

En la pestaña **Tipo, límites y checker** del dataset:

- **Límite de tiempo** (segundos de CPU), **límite de tiempo real** (vacío:
  el doble, o el límite más 1 s), **memoria** (MiB), **salida** (MiB),
  **procesos**, **tamaño del código**.
- **Tipo de problema**: *Batch* (entrada/salida estándar o archivos, con o
  sin grader), *Solo salida*, *Dos pasos*, *Comunicación*, *Interactiva*.
  Cada tipo muestra sus opciones; qué subir y cómo funcionan está en
  [tipos de problema](task-types.md) y en la tabla de la
  [guía del administrador](guia-del-admin.md#cada-tipo-de-problema-paso-a-paso).
- **Checker** (Batch, Solo salida, Dos pasos): *ignorar espacios* (lo
  normal), *exacto*, *reales con tolerancia* (absoluta y relativa), o un
  **checker propio** (protocolo CMS o testlib) que se sube en **Managers**.
- **Managers**: sube los archivos que la configuración pide (la página dice
  cuáles faltan).

**Guardar**. Los envíos ya evaluados conservan su resultado hasta que los
reevalúes (botón al principio de la página del dataset).

## 6. Puntuación y subtareas

En la pestaña **Puntuación y subtareas**:

- *Sum*: puntos por caso (por ejemplo 10 casos × 10 puntos = 100).
- *GroupMin*: cada subtarea vale sus puntos solo si **todos** sus casos son
  correctos (la regla de la IOI).
- *GroupMul*: los puntos de la subtarea se multiplican por el peor resultado
  de sus casos (útil con checkers que dan puntaje parcial).
- *GroupThreshold*: la subtarea puntúa si cada caso supera un umbral.

El editor de subtareas tiene una fila por subtarea: **puntos** y **qué
casos** (nombres que cumplen una expresión regular como `^g.*`, una lista
marcada a mano, o "los siguientes N casos"); la última fila vacía agrega
una subtarea. Debajo dice el total y qué casos no están en ninguna subtarea.

Con GroupMin o GroupMul, **cortocircuito** deja de evaluar una subtarea en
cuanto un caso da 0 (mismo puntaje, menos tiempo de jueces; los casos
omitidos se muestran como *omitidos*).

Lo que el concursante ve: un **bloque por subtarea** con sus puntos y su
veredicto (verde, amarillo o rojo) y el resultado de los **casos públicos**
uno por uno.

## 7. La solución de referencia

**Probador de problemas** (página del problema): elige el lenguaje, sube el
archivo y **Probar**. Se evalúa en todos los datasets del problema como un
envío, pero nunca aparece en rankings ni estadísticas. La tabla muestra el
resultado en cada dataset.

Sirve para:

- comprobar que el problema está bien (la lista de preparación pide una
  ejecución con el puntaje completo);
- escribir las salidas de los casos (sección 4);
- probar soluciones incorrectas o lentas y ver que fallan como se espera.

Si cambias los casos después, **vuelve a enviarla**: su resultado anterior
se calculó con los casos de entonces.

## 8. Ejemplos, adjuntos y reglas de envío

- **Ejemplos**: se muestran en todos los enunciados del problema. Agrégalos
  desde un caso (**usar como ejemplo**) o escribiéndolos; cada uno puede
  llevar una explicación en Markdown.
- **Adjuntos**: archivos que los concursantes descargan (un grader de
  ejemplo, un header, datos).
- **Configuración** (al final de la página del problema): archivos del envío
  (`suma.%l`: el `%l` se cambia por la extensión del lenguaje), lenguajes
  permitidos, modo de puntuación (mejor por subtarea, mejor envío, el
  último...), retroalimentación, tokens, límites de envíos del problema, y
  si los concursantes ven los mensajes propios del checker.

## 9. Ponerlo en el concurso

Si lo creaste desde el concurso ya está en él. Si no, en el concurso:
**Problemas → Agregar un problema existente**. El orden de la lista es el
de las letras (A, B, C...); las flechas lo cambian. Durante el concurso,
**cerrar envíos** detiene los envíos a un solo problema.

## 10. Cambiar un problema ya usado

Para cambiar casos o puntajes de un problema que ya tiene envíos sin
afectar el resultado actual:

1. En la página del problema, **Nuevo dataset** copiando el actual.
2. Cambia lo necesario en la copia; marca *Evaluar los envíos nuevos en
   este dataset en segundo plano* si quieres compararlos.
3. **Comparar con el vivo** muestra qué puntajes cambiarían.
4. **Poner en vivo** cuando estés seguro: los puntajes se recalculan.
