# Cómo se evalúan los envíos rápido y con justicia

Esta página explica qué hace el juez para que los resultados lleguen
rápido durante un concurso y cómo comprobar que todos los núcleos de
evaluación miden el tiempo igual.

## Colas y prioridades

Cada trabajo espera en una de cinco colas, que se atienden en este orden:

1. **evaluación** — correr los casos de un envío en vivo (así un envío ya
   compilado termina primero);
2. **compilación** — compilar un envío en vivo;
3. **diferidas** — los trabajos pendientes de un envío cuyo concursante ya
   mandó otro más nuevo al mismo problema (ver abajo);
4. **pruebas de usuario** — la página *Pruebas*;
5. **segundo plano** — reevaluaciones y datasets que no están en vivo.

Un worker siempre toma el primer trabajo de la cola de mayor prioridad que
no esté vacía. La página **Jueces** (*Workers y colas*) muestra el largo de
cada cola, los trabajos en curso y los slots de cada worker.

### Primero los envíos más recientes

Cuando un concursante envía a un problema mientras un envío anterior suyo
a ese problema todavía se está evaluando, el anterior queda marcado como
*superado*. Sus trabajos pendientes se pasan, cuando un worker llega a
ellos, a la cola **diferidas**: se evalúan después de los últimos envíos
de todos. Así, un concursante que manda diez envíos seguidos no retrasa a
los demás, y cada envío se evalúa igual por completo (el puntaje del
problema toma el mejor, como siempre).

## Posición en la cola y tiempo de espera

Mientras un envío espera un worker, la tarjeta de resultado de la página
del problema dice cuántos envíos hay antes y cuánto tardan ahora los
resultados (la mediana del tiempo entre envío y puntaje de los últimos 200
envíos evaluados). La tarjeta se actualiza sola cada 10 segundos mientras
espera y deja de hacerlo cuando el envío empieza a evaluarse.

## Evaluación en cortocircuito

En una subtarea *GroupMin* (y *GroupMul*), un caso con 0 deja toda la
subtarea en 0: los demás casos no pueden cambiarlo. Con **Cortocircuito**
marcado en la página del dataset (sección de puntaje), el dispatcher marca
esos casos como *omitidos* en cuanto llega el cero, y los workers no corren
los que aún esperan. El puntaje es el mismo que con la evaluación completa;
el tiempo ahorrado queda para los demás concursantes.

Los casos omitidos aparecen en el detalle como *Omitido: otro caso de la
subtarea ya falló*. Un caso que pertenece a varias subtareas se omite solo
cuando todas ya tienen un cero. La opción viene desactivada; déjala así
cuando los concursantes deban ver el resultado de cada caso (por ejemplo,
con retroalimentación completa si el concurso lo quiere).

## Caché de compilación

Una compilación exitosa se recuerda bajo un hash de todo lo que la
determina: el lenguaje y sus comandos, el tipo de tarea y sus parámetros,
los archivos enviados y los graders o headers del dataset. Un envío
idéntico más adelante (el mismo código enviado de nuevo, una reevaluación,
otro dataset con los mismos graders) toma los ejecutables directamente,
sin trabajo de compilación. El recolector de blobs borra las entradas que
no se usan hace una semana.

Una reevaluación con **recompilar** vacía la caché y compila de verdad:
úsala después de actualizar un compilador en los workers.

## Límites de tiempo por lenguaje

Un lenguaje puede escalar los límites de tiempo de los problemas con
`time_multiplier` en su archivo (ninguno por defecto); ver
[lenguajes](languages.md).

## Los workers descargan los casos por adelantado

Cada minuto el dispatcher publica los casos, checkers y graders de los
datasets en vivo de los concursos en curso o que empiezan dentro de tres
horas. Cada worker descarga los que le faltan en su caché, de a uno, en
segundo plano (hasta el 80% de `worker.cache_max_bytes`), así los primeros
envíos de un concurso no esperan los archivos. La página **Jueces**
muestra el avance de cada worker (*caché 120/130*). Si la caché de un
worker no alcanza para los casos de un concurso, aumenta
`worker.cache_max_bytes`.

## Calibrar las máquinas de evaluación

El mismo programa debe tardar lo mismo en cada núcleo de evaluación; si
no, el veredicto de un concursante depende del núcleo que lo corrió.
Ejecuta en cada máquina de evaluación, con su worker detenido:

```sh
sudo systemctl stop cms-worker
sudo cms ctl calibrate -config /etc/cms/cms.yaml
sudo systemctl start cms-worker
```

Compila una vez un programa que solo usa CPU y lo corre varias veces
(`-runs`, 5 por defecto) en cada núcleo de evaluación a través del
sandbox; luego muestra la mediana del tiempo de CPU de cada núcleo, cuánto
se aleja de la mediana de la máquina y cuánto variaron sus corridas. Los
núcleos a más de un 3% (`-tolerance`) se marcan SLOW o FAST; un núcleo
cuyas corridas varían mucho se marca como *noisy*. Las causas habituales
son el turbo, el governor de frecuencia de la CPU y los hyperthreads que
comparten un núcleo físico: `sudo cms-host-tuning enable` los configura, o
quita el núcleo de `worker.cores`.

El resultado además se guarda (30 días) y aparece en la página **Jueces**,
donde se comparan las máquinas entre sí: se marca la máquina que se aleja
de la mediana de todas más que la tolerancia, y un aviso dice cuánto más
lenta es la máquina más lenta que la más rápida. Usa `-publish=false` para
solo imprimir el resultado, y `-box-offset` si las cajas de isolate por
defecto (desde la 500) se superponen con las de un worker en marcha.
