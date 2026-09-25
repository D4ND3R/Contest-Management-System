// Ranking web server: applies live row updates sent over Server-Sent
// Events (ready-made HTML for rows whose content changed; for rows that
// only moved, runs of old ranks and how far they move) and keeps the table
// sorted. No framework.
(function () {
  "use strict";
  var meta = document.querySelector('meta[name="rws-events"]');
  var body = document.getElementById("rows");
  if (!meta || !body || !window.EventSource) return;
  var live = document.getElementById("live");
  // The sequence number of what the table shows: an update that starts
  // anywhere else was meant for another state (one was missed), and the
  // page starts over.
  var seq = meta.dataset.seq ? Number(meta.dataset.seq) : -1;
  var reloading = false;

  // Rows are sorted by rank, then name: binary search for the place.
  function after(r, rank, name) {
    var rr = Number(r.dataset.rank);
    return rr > rank || (rr === rank && r.dataset.name > name);
  }
  function place(tr) {
    var rows = body.children, rank = Number(tr.dataset.rank), name = tr.dataset.name;
    var lo = 0, hi = rows.length;
    while (lo < hi) {
      var mid = (lo + hi) >> 1;
      if (after(rows[mid], rank, name)) hi = mid; else lo = mid + 1;
    }
    body.insertBefore(tr, rows[lo] || null);
  }
  function sorted() {
    var rows = Array.prototype.slice.call(body.children);
    rows.sort(function (a, b) {
      return Number(a.dataset.rank) - Number(b.dataset.rank) || (a.dataset.name < b.dataset.name ? -1 : a.dataset.name > b.dataset.name ? 1 : 0);
    });
    rows.forEach(function (r, i) { if (body.children[i] !== r) body.insertBefore(r, body.children[i]); });
  }
  function highlight(tr) {
    tr.classList.add("moved");
    setTimeout(function () { tr.classList.remove("moved"); }, 2500);
  }
  // row takes a row out of the table and returns its new version.
  function row(u) {
    var t = document.createElement("template");
    t.innerHTML = u.html.trim();
    var tr = t.content.firstElementChild;
    var old = document.getElementById("r-" + u.key);
    if (!old || old.dataset.rank !== tr.dataset.rank) highlight(tr);
    if (old) old.remove();
    return tr;
  }
  // rerank takes a row that only moved out of the table, with its new rank.
  function rerank(tr, rank) {
    tr.dataset.rank = rank;
    var c = tr.querySelector(".rank");
    if (c) c.textContent = rank;
    tr.remove();
    return tr;
  }
  function reload() {
    if (reloading) return;
    reloading = true;
    es.close();
    setTimeout(function () { location.reload(); }, Math.random() * 3000);
  }

  var es = new EventSource(meta.content);
  es.addEventListener("open", function () { if (live) live.classList.remove("off"); });
  es.addEventListener("error", function () { if (live) live.classList.add("off"); });
  es.addEventListener("reload", reload);
  es.addEventListener("rows", function (e) {
    if (reloading) return;
    var d = JSON.parse(e.data);
    if (seq >= 0 && d.base !== seq) { reload(); return; }
    seq = d.seq;
    (d.removed || []).forEach(function (k) { var r = document.getElementById("r-" + k); if (r) r.remove(); });
    var rows = d.rows || [];
    if (!d.unfrozen) {
      // Take out every row that changes, then put each back in its place;
      // the rows left keep their order.
      var out = rows.map(row), shift = d.shift || [];
      if (shift.length) {
        Array.prototype.slice.call(body.children).forEach(function (tr) {
          var r = Number(tr.dataset.rank);
          for (var i = 0; i < shift.length; i++) {
            if (r >= shift[i][0] && r <= shift[i][1]) { out.push(rerank(tr, r + shift[i][2])); return; }
          }
        });
      }
      Object.keys(d.ranks || {}).forEach(function (k) {
        var tr = document.getElementById("r-" + k);
        if (tr) out.push(rerank(tr, d.ranks[k]));
      });
      out.forEach(place);
      return;
    }
    // Unfreeze: the notice goes and the rows are revealed bottom-up, at
    // most half a minute in all whatever the size of the board.
    document.querySelectorAll(".frozen").forEach(function (el) { el.remove(); });
    var step = Math.min(700, 30000 / Math.max(rows.length, 1));
    rows.forEach(function (u, i) {
      setTimeout(function () { place(row(u)); if (i === rows.length - 1) sorted(); }, i * step);
    });
  });
})();
