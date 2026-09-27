# Rankings

Hay tres lugares que muestran un ranking:

- **Panel de administración** (concurso → Clasificación): siempre completo y sin
  congelar, con exportación CSV/JSON y filtro por sede.
- **Servidor de concursantes** (menú del concursante → Clasificación): lo que los
  concursantes pueden ver según la configuración del concurso.
- **Servidor de ranking** (`cms ranking-web`, RWS): el marcador público en
  vivo, con historial de puntaje por participante, banderas y
  actualizaciones en vivo.

## Configuración (Configuración del concurso → Clasificación)

El formulario hace cuatro preguntas; lo demás está en **Más opciones de la
clasificación**.

1. **Quién ve la clasificación**:

   | Respuesta | Qué pasa |
   |-----------|----------|
   | todos: concursantes y la clasificación pública | el servidor de ranking la publica y los concursantes la ven completa |
   | solo los concursantes | la ven completa en el sitio del concurso; no hay marcador público |
   | cada concursante ve solo su posición | el sitio del concurso muestra "Tu posición: 3 de 40" |
   | solo los organizadores (y quien tenga el enlace secreto) | para un proyector: el marcador público pide una clave |
   | nadie (solo en este panel) | solo la administración la ve |

   Un concurso antiguo con una combinación que no es ninguna de estas
   muestra *mantener el ajuste actual*.
2. **Se muestra**: durante y después del concurso, o solo al terminar.
3. **Congelarla durante los últimos N minutos** (0: nunca): los envíos
   posteriores se muestran como `?` hasta que la descongelas.
4. **Columnas**: puntajes por subtarea, instituciones, banderas, fotos.

En **Más opciones de la clasificación**: congelar a una hora exacta,
empates, medallas, penalización ICPC, decimales, mostrar usuarios ocultos y
clasificación anónima (sin nombres).

**Fotos**: las fotos de los participantes (página del usuario → Foto),
desactivadas por defecto: los concursantes suelen ser menores de edad, así
que actívalas solo con consentimiento. El marcador muestra una miniatura
cuadrada junto a cada nombre y una más grande en la página del
participante, nunca el archivo original; las filas de equipo muestran la
bandera del equipo y los marcadores anónimos no muestran fotos. El
publicador de ranking hace las miniaturas en segundo plano (fotos JPEG, PNG
o GIF), así que aparecen unos segundos después de publicar el concurso.

**Empates.** Por defecto, los totales iguales comparten el puesto (en modo
ICPC, igual cantidad de problemas y penalización). *Se desempatan por
tiempo* pone primero a quien llegó antes: en modo IOI, el momento en que el
participante alcanzó su total (el envío que dejó por última vez el puntaje
de cada problema en su valor actual, el más tardío entre los problemas); en
modo ICPC, el último problema resuelto. Los tiempos se cuentan desde el
inicio de cada participante, así que empezar con retraso no perjudica; los
ajustes manuales de puntaje no los cambian. La exportación JSON muestra el
tiempo como `reached_s` (segundos desde el inicio). Con desempate por
tiempo, las medallas y los premios de los certificados siguen los puestos
resultantes.

Los concursos por equipos se clasifican por equipo: por problema, el mejor
puntaje de sus integrantes (con puntuación "mejor por subtarea", el mejor
puntaje de cada subtarea); en modo ICPC, el integrante que lo resolvió
primero.

El congelamiento se mantiene hasta que un administrador pulsa
**Descongelar ahora** en la página del ranking (**Congelar de nuevo** lo
revierte). El ranking del panel nunca se congela. Los marcadores públicos
abiertos no se recargan al descongelar: las filas que cambiaron se revelan
una por una de abajo hacia arriba (medio minuto como máximo), resaltadas al
moverse.

## Participantes no oficiales y medallas

- **No oficial** (página del participante): un invitado o un concursante
  extra se evalúa y se muestra como todos, marcado *no oficial* (un * en la
  tabla pública), pero no ocupa puesto ni recibe medalla: los puestos
  oficiales lo saltan. Los participantes ocultos tampoco ocupan puesto.
- **Medallas** (Configuración → Ranking): *ninguna*, *cortes para los
  administradores* o *también en las tablas públicas*. La regla de la IOI:
  como máximo una doceava parte de los participantes oficiales recibe oro,
  una cuarta parte oro o plata y la mitad una medalla; un empate nunca se
  divide (un grupo empatado que no entra recibe la medalla siguiente) y un
  puntaje de cero no gana nada. La clasificación muestra los cortes (total
  mínimo y cantidad de cada medalla) y a cada medallista; el CSV exportado
  tiene las columnas `official` y `medal`.

## Servidor de ranking

El RWS nunca toca la base de datos: guarda los marcadores en memoria (y en
`ranking_web.data_dir`) y los recibe del **publicador de ranking**, que corre
dentro del dispatcher. El publicador recalcula el marcador de un concurso
cuando cambian los puntajes (a lo más cada 250 ms, cada 2 s mientras está
congelado), envía solo las filas que cambiaron y un marcador completo cuando
un servidor es nuevo o quedó atrás; el servidor envía a los espectadores las
filas cambiadas como HTML listo por Server-Sent Events, y las filas que solo
se movieron como desplazamientos de lugar (un ascenso que pasa a cientos de
filas ocupa unos pocos bytes). Una página que se perdió una actualización
(una conexión caída, un reinicio) lo nota y se recarga sola. Un puntaje
llega al marcador muy por debajo de un segundo después de calcularse.

Configuración:

```yaml
ranking_web:
  listen: ":8890"
  data_dir: /var/lib/cms/ranking
  push_token: "<secreto largo y aleatorio>"  # compartido por el publicador y el RWS
  public_url: https://ranking.ejemplo.org   # enlaces en el panel
  max_clients: 20000
dispatcher:
  ranking_urls: ["http://127.0.0.1:8890"]   # cada instancia de RWS a alimentar
```

El RWS puede correr en otra máquina (solo necesita que el dispatcher lo
alcance); se pueden alimentar varias instancias a la vez.
`/{concurso}/ranking.json` es la instantánea en caché (gzip, ETag),
`/{concurso}/events` el flujo en vivo y `/{concurso}/u/<clave>` el
historial de puntaje de un participante.
