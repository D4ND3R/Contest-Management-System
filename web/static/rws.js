// Ranking web server: applies live row updates (ready-made HTML sent over
// Server-Sent Events) and keeps the table sorted. No framework.
(function () {
  "use strict";
  var meta = document.querySelector('meta[name="rws-events"]');
  var body = document.getElementById("rows");
  if (!meta || !body || !window.EventSource) return;
  var live = document.getElementById("live");

  function place(tr) {
    var rank = Number(tr.dataset.rank), name = tr.dataset.name;
    var rows = body.children;
    for (var i = 0; i < rows.length; i++) {
      var r = rows[i];
      if (r === tr) continue;
      var rr = Number(r.dataset.rank);
      if (rr > rank || (rr === rank && r.dataset.name > name)) { body.insertBefore(tr, r); return; }
    }
    body.appendChild(tr);
  }

  var es = new EventSource(meta.content);
  es.addEventListener("open", function () { if (live) live.classList.remove("off"); });
  es.addEventListener("error", function () { if (live) live.classList.add("off"); });
  es.addEventListener("reload", function () { setTimeout(function () { location.reload(); }, Math.random() * 3000); });
  es.addEventListener("rows", function (e) {
    var d = JSON.parse(e.data);
    (d.removed || []).forEach(function (k) { var r = document.getElementById("r-" + k); if (r) r.remove(); });
    (d.rows || []).forEach(function (u) {
      var t = document.createElement("template");
      t.innerHTML = u.html.trim();
      var tr = t.content.firstElementChild;
      var old = document.getElementById("r-" + u.key);
      if (old) {
        if (old.dataset.rank !== tr.dataset.rank) tr.classList.add("moved");
        old.remove();
      } else {
        tr.classList.add("moved");
      }
      place(tr);
      setTimeout(function () { tr.classList.remove("moved"); }, 2500);
    });
  });
})();
