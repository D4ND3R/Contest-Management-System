# Manual del día del concurso

Una lista de verificación para quienes operan el concurso. "Admin" es el
sitio web de administración; los comandos se ejecutan en el servidor
principal salvo que se indique otra cosa.

## El día anterior

- [ ] Actualiza si hace falta (`git pull && make build && sudo scripts/install.sh …`
      con las mismas opciones); nunca el mismo día del concurso.
- [ ] Cada problema validado: el informe de **Validación** de su página
      muestra cada solución de referencia con el veredicto esperado.
- [ ] Configuración del concurso: hora de inicio y fin (en UTC en el
      servidor; revisa la zona horaria que ven los concursantes), lenguajes,
      límites de envíos, tokens, ranking (visibilidad, minutos de
      congelamiento).
- [ ] Usuarios importados, contraseñas impresas o enviadas (**Usuarios →
      PDF de credenciales**), equipos y sedes asignados.
- [ ] Toma un respaldo (**Respaldos → Respaldar ahora**) y restáuralo en una
      base de prueba ([simulacro](respaldos.md#simulacro)).

## Una hora antes

- [ ] `sudo systemctl stop cms-worker && sudo cms-verify-host --config /etc/cms/cms.yaml --languages <los lenguajes del concurso>; sudo systemctl start cms-worker`
      en cada máquina de evaluación. **Si informa `RESULT: FAIL`, no inicies
      el concurso** en esa máquina: corrígela o detén su worker.
- [ ] **Workers y colas**: todos los workers activos, colas vacías.
- [ ] Ensayo: entra como un concursante de prueba (o **ver como
      concursante** en una participación), envía una solución correcta y una
      incorrecta a cada problema, revisa veredictos, puntajes y ranking; luego
      borra la participación de prueba (o déjala oculta).
- [ ] **Respaldos**: la programación dice "cada 15m durante los concursos";
      apareció un respaldo después del ensayo.
- [ ] Avisos preparados; la bandeja de preguntas abierta en una pestaña (el
      contador del menú se actualiza en vivo).

## Durante el concurso

**Qué vigilar**

- **Workers y colas**: todos los workers activos; los trabajos en espera
  deberían vaciarse en segundos. Una cola que no deja de crecer significa
  falta de capacidad de evaluación (agrega un worker, ver [workers
  externos](worker-externo.md)) o un worker trabado.
- **Preguntas** (contador del menú): responde en privado o para todos; las
  respuestas rápidas ahorran tiempo.
- Alertas del sistema (avisos rojos en el admin): errores de evaluación
  después de los reintentos, respaldos fallidos.
- `journalctl -u 'cms-*' -p warning -f` en el servidor principal.
- Concursos ICPC: la página de **Globos** (en la página del concurso)
  abierta para quienes los reparten; se actualiza sola.
- Con impresión: la cola de **Impresión** (en la página del concurso)
  abierta para el staff que entrega las hojas.

**Extender el tiempo**

- Para todos: edita el concurso y mueve la hora de fin; los relojes de los
  concursantes la siguen en segundos.
- Para un participante (p. ej. una computadora que falló): **Participaciones
  → el participante → tiempo extra** (segundos); con ventanas de tiempo por
  usuario, **retraso** mueve su inicio.

**Reevaluar**

- Caso de prueba o checker equivocado: corrige el dataset (o crea uno nuevo
  y hazlo activo; los puntajes se recalculan solos) y usa **Reevaluar** en el
  problema: *recalcular puntaje*, *reevaluar* (ejecutar de nuevo),
  *recompilar* (compilar y ejecutar de nuevo). Los mismos botones existen
  por envío, por usuario y para todo el concurso.
- Un puntaje que hay que cambiar a mano (p. ej. una decisión del jurado):
  **Participaciones → el participante → Ajustar un puntaje**: puntos a sumar
  (negativos para quitar) y un motivo obligatorio. Los ajustes quedan para
  siempre (una corrección es otro ajuste), el concursante los ve con el
  motivo, se registran en la auditoría y sobreviven a cualquier
  reevaluación; los rankings se actualizan al instante.
- Un envío que no debe contar (trampa, error de los organizadores):
  **Invalídalo** en su página indicando el motivo; el concursante ve el
  motivo y el puntaje se actualiza al instante. Se puede restaurar.

**Cuando algo falla**

| Síntoma | Qué pasa | Qué hacer |
|---------|----------|-----------|
| Un worker se cae o su máquina muere | Tras 10 s sin latido sus trabajos vuelven a la cola y otros workers los toman. | Reinícialo (`systemctl restart cms-worker`); no hay nada que rehacer. |
| Un trabajo falla 3 veces | El envío muestra "la evaluación falló"; aparece una alerta. | Lee la alerta (suele ser un checker o un compilador faltante), corrige y **Reevalúa** ese envío. |
| Un servicio web se cae | systemd lo reinicia en 2 s; las sesiones sobreviven. | Revisa `journalctl -u cms-contest-web`. |
| El VPS se reinicia | Todo arranca solo; Valkey conserva las colas (archivo append-only) y PostgreSQL los datos; el dispatcher reenvía lo que estaba en curso. | Revisa **Workers y colas** y un envío. El reloj del concurso no se detiene: extiende el tiempo si el corte fue largo. |
| Se pierde la base de datos | — | Restaura el último respaldo (de hace 15 minutos como máximo) en una máquina nueva: [respaldos](respaldos.md). Los envíos posteriores se pierden: anúncialo y extiende el tiempo. |
| El servidor de ranking no responde | Los concursantes no se ven afectados; el dispatcher envía el tablero completo cuando vuelve. | `systemctl restart cms-ranking-web`. |

## Al terminar

- [ ] Congelamiento: con "congelar los últimos N minutos" el ranking público
      dejó de actualizarse antes del final; después de la ceremonia,
      **Ranking → descongelar**.
- [ ] Espera a que **Workers y colas** no tenga trabajos en espera ni envíos
      pendientes.
- [ ] Responde o cierra las preguntas que queden.
- [ ] Exporta los resultados: **Ranking → CSV / JSON** (y guarda una copia de
      la página del ranking).
- [ ] Respaldo final (**Respaldar ahora**), descárgalo y guárdalo fuera del
      servidor.
- [ ] Opcional: activa el modo análisis o permite que los concursantes
      descarguen sus envíos.
