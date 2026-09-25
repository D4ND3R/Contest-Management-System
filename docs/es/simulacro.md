# Simulacro: un concurso de ensayo

Haz este ensayo en el servidor real unos días antes del concurso, con el
equipo que lo va a operar. Toma unos 45 minutos y ejercita lo que un
concurso necesita: tres tipos de problema, preguntas, un anuncio, un envío
invalidado, el ranking congelado y los resultados finales. Los mismos pasos
están automatizados en `internal/e2e/drill_test.go` (`TestDrill`, parte de
`make test`), así que se sabe que funcionan; el simulacro comprueba tu
servidor, tu red y tu equipo.

Marca cada casilla; todo lo que no se comporte como se describe es un
hallazgo a resolver antes del concurso.

## Preparación (10 minutos)

- [ ] `sudo cms-verify-host` pasa ([verificar la máquina](verificar-host.md)).
- [ ] **Concursos → Nuevo concurso** `drill`: empieza ahora, dura 30
      minutos, lenguajes C, C++ y Python, visibilidad del ranking
      *concursantes*, congelar los últimos 10 minutos.
- [ ] **Problemas → Importar un paquete**, tres veces, en `drill` (comprime
      cada carpeta de [`docs/examples/packages/`](../examples/packages/)):
      `batch-suma` (entrada/salida normal), `interactive-adivina`
      (interactivo) y `output-only-cuadrados` (solo salida). Cada reporte de
      validación dice *Todas las soluciones se comportan como se esperaba*.
- [ ] **Usuarios → Importar CSV** dos concursantes, `ana` y `beto`,
      agregados a `drill`; **Participaciones → Generar e imprimir** sus
      credenciales.
- [ ] Dos personas entran como `ana` y `beto` desde la red del concurso
      (`https://<dominio>/drill/`), en las computadoras que usarán los
      concursantes.

## Durante el concurso (20 minutos)

- [ ] `ana` envía una solución correcta a cada problema (para `suma` y
      `adivina` una de `solutions/ac_*` del paquete; para `cuadrados` los
      cuatro `tests/*.out` como `output_01.txt` … `output_04.txt`): cada una
      obtiene 100 en segundos, visible en sus páginas sin recargar.
- [ ] `beto` envía las soluciones incorrectas (`solutions/wa_*`) y solo
      `output_01.txt`: 0 puntos en `suma`, 25 en `adivina` (`wa_uno` siempre
      responde 1, correcto en un caso) y 25 en `cuadrados`; las páginas de
      los envíos muestran el resultado por caso que permite la
      retroalimentación del problema.
- [ ] `beto` hace una pregunta sobre `cuadrados`; el equipo la ve en
      **Preguntas** (se enciende el contador del menú) y la responde en
      público: ambos concursantes reciben el aviso y leen la respuesta.
- [ ] El equipo publica un anuncio desde la página **Comunicación** del
      concurso: ambos concursantes reciben el aviso.
- [ ] El equipo invalida el envío de `ana` en `suma` con un motivo (página
      del envío → *Invalidar*): su puntaje baja 100 y ella ve el motivo en su
      envío.
- [ ] **Workers y colas** no muestra trabajos en espera ni errores del
      sistema.
- [ ] En los últimos 10 minutos `ana` vuelve a enviar: el ranking de los
      concursantes (`/drill/ranking`) dice que está congelado y no lo
      muestra; el ranking del admin sí.

## Después del concurso (15 minutos)

- [ ] El concurso termina a tiempo para ambos; los envíos tardíos se
      rechazan.
- [ ] **Ranking → descongelar**: el ranking de los concursantes muestra los
      puntajes finales.
- [ ] **Ranking → CSV** y **PDF para imprimir**: `ana` primera (200 más lo
      que haya reenviado), `beto` segundo (50).
- [ ] **Respaldos → Respaldar ahora**, descarga el archivo y restáuralo en
      una base de prueba ([respaldos](respaldos.md#simulacro)).
- [ ] Página del concurso → **Archivo** con envíos; después borra el
      concurso de ensayo si no quieres conservarlo.

Anota cuánto tomó cada paso y cada duda del equipo: es la lista a resolver
antes del concurso real.
