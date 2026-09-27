# Rankings

Three places show a ranking:

- **Admin panel** (contest → Ranking): always complete and unfrozen, with
  CSV/JSON export and a site filter.
- **Contest web server** (contest → Ranking in the contestant menu): what
  contestants may see, per contest settings.
- **Ranking web server** (`cms ranking-web`, RWS): the public live
  scoreboard, with per-participant score history, flags and live updates.

## Settings (contest Settings → Ranking)

| setting | values |
|---------|--------|
| Who sees it | everybody (public scoreboard) · only contestants · only administrators (and a secret link, e.g. for a projector) · nobody (admin panel only) |
| What contestants see | the whole ranking · only their position · nothing |
| When | during and after the contest · only after the end |
| Freeze | the last N minutes (or at a given time); later submissions show as `?` |
| Show | subtask scores · flags · photos · institutions · hidden users · anonymous (no names) |
| Ties | share the place (the IOI rule, default) · broken by time |

**Photos** are the participants' photos (user page → Photo), off by
default: contestants are often minors, so turn them on only with consent.
The scoreboard shows a small square thumbnail next to each name and a
larger one on the participant's page, never the original file; team rows
show the team's flag, and anonymous boards show no photos. Thumbnails are
made in the background by the ranking pusher (JPEG, PNG or GIF photos), so
they appear a few seconds after the contest is published.

**Ties.** By default equal totals share a place (in ICPC mode, equal
problems and penalty). *Broken by time* ranks first whoever got there
first: in IOI mode, the moment the participant's total was reached (the
submission that last changed each task's score to its current value, the
latest among the tasks); in ICPC mode, the last problem solved. Times count
from each participant's own start, so a delayed start is no disadvantage;
manual score adjustments do not change them. The JSON export shows the time
as `reached_s` (seconds from the start). With time tie-breaks, medals and
certificate awards follow the resulting places.

Team contests are ranked by team: per task the best member score (with
"best per subtask" scoring, the best member score of every subtask); in
ICPC mode the member who solved first.

The freeze stays until an administrator presses **Unfreeze now** on the
ranking page (**Freeze again** undoes it). The admin ranking is never
frozen. Open public scoreboards do not reload when unfreezing: the rows
that changed are revealed one by one from the bottom up (half a minute at
most), each highlighted as it moves.

## Unofficial participants and medals

- **Unofficial** (participant page): a guest or an extra contestant is
  judged and shown like everybody else, marked *unofficial* (a * on the
  public scoreboard), but takes no place and no medal: the official
  places skip them. Hidden participants take no place either.
- **Medals** (Settings → Ranking): *none*, *cutoffs for the
  administrators* or *also on the public scoreboards*. The IOI rule: at most
  a twelfth of the official participants get gold, a quarter gold or
  silver, half a medal; a tie is never split (a tie group that does not
  fit gets the next medal) and a zero score wins nothing. The ranking shows
  the cutoffs (lowest total and number for each medal) and every
  medallist; the CSV export has `official` and `medal` columns.

## Ranking web server

The RWS never touches the database: it keeps the boards in memory (saved in
`ranking_web.data_dir`) and receives them from the **ranking pusher**, which
runs inside the dispatcher. The pusher recomputes a contest's board when
scores change (at most every 250 ms, every 2 s while frozen), sends only the
rows that changed and a full board when a server is new or behind; the
server sends spectators the changed rows as ready-made HTML over
Server-Sent Events, and the rows that only moved as rank shifts (a climb
past hundreds of rows is a few bytes). A page that missed an update (a
dropped connection, a restart) notices and reloads itself. A score reaches
the scoreboard well under a second after it is computed.

Configuration:

```yaml
ranking_web:
  listen: ":8890"
  data_dir: /var/lib/cms/ranking
  push_token: "<long random secret>"   # shared by the pusher and the RWS
  public_url: https://ranking.example.org  # links in the admin panel
  max_clients: 20000
dispatcher:
  ranking_urls: ["http://127.0.0.1:8890"]  # every RWS instance to feed
```

The RWS may run on another machine (it only needs to be reachable by the
dispatcher); several instances can be fed at once. `/{contest}/ranking.json`
is the cached snapshot (gzip, ETag), `/{contest}/events` the live stream
and `/{contest}/u/<key>` a participant's score history.
