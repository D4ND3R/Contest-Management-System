# Rankings

Hay tres lugares que muestran un ranking:

- **Panel de administración** (concurso → Ranking): siempre completo y sin
  congelar, con exportación CSV/JSON y filtro por sede.
- **Servidor de concursantes** (menú del concursante → Ranking): lo que los
  concursantes pueden ver según la configuración del concurso.
- **Servidor de ranking** (`cms ranking-web`, RWS): el marcador público en
  vivo, con historial de puntaje por participante, banderas y
  actualizaciones en vivo.

## Configuración (página del concurso → Ranking)

| opción | valores |
|--------|---------|
| Quién lo ve | todos (marcador público) · solo los concursantes · solo administradores (y un enlace secreto, p. ej. para un proyector) · nadie (solo el panel) |
| Qué ven los concursantes | el ranking completo · solo su posición · nada |
| Cuándo | durante y después del concurso · solo al terminar |
| Congelamiento | los últimos N minutos (o a una hora dada); los envíos posteriores se muestran como `?` |
| Mostrar | puntajes por subtarea · banderas · instituciones · usuarios ocultos · anónimo (sin nombres) |

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
