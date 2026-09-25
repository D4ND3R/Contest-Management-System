// CMS front-end glue (no framework): CSRF header for htmx requests, live
// updates over Server-Sent Events, countdowns and notifications.
(function () {
  "use strict";
  var meta = function (n) { var m = document.querySelector('meta[name="' + n + '"]'); return m ? m.content : ""; };

  document.addEventListener("htmx:configRequest", function (e) {
    e.detail.headers["X-CSRF-Token"] = meta("csrf-token");
  });

  // Refresh an element that declares where its fresh HTML lives.
  function refresh(el) {
    if (el && el.dataset.src && window.htmx) {
      htmx.ajax("GET", el.dataset.src, { target: el, swap: "outerHTML" });
    }
  }

  function notify(text) {
    var box = document.getElementById("notifications");
    if (!box) return;
    var n = document.createElement("div");
    n.className = "notice";
    n.textContent = text;
    n.addEventListener("click", function () { n.remove(); });
    box.appendChild(n);
    if (window.Notification && Notification.permission === "granted") {
      try { new Notification(document.title, { body: text }); } catch (err) { /* ignore */ }
    }
  }

  // Optional sound on announcements, messages and answers (per browser).
  function store(k, v) {
    try { if (v === undefined) return localStorage.getItem(k); localStorage.setItem(k, v); } catch (err) { return null; }
    return null;
  }
  function beep() {
    if (store("cms-sound") !== "1") return;
    try {
      var Ctx = window.AudioContext || window.webkitAudioContext, c = new Ctx(), o = c.createOscillator(), g = c.createGain();
      o.frequency.value = 880; g.gain.value = 0.08; o.connect(g); g.connect(c.destination);
      o.start(); o.stop(c.currentTime + 0.25); o.onended = function () { c.close(); };
    } catch (err) { /* no audio */ }
  }
  function soundToggle() {
    var b = document.getElementById("sound-toggle");
    if (!b) return;
    var show = function () { b.textContent = store("cms-sound") === "1" ? b.dataset.on : b.dataset.off; };
    b.addEventListener("click", function () { store("cms-sound", store("cms-sound") === "1" ? "0" : "1"); show(); beep(); });
    show();
  }

  function connect() {
    var url = meta("cms-events");
    if (!url || !window.EventSource) return;
    var es = new EventSource(url);
    es.addEventListener("submission", function (e) {
      var d = JSON.parse(e.data);
      var row = document.getElementById("sub-" + d.submission_id);
      if (row) { refresh(row); return; }
      var list = document.getElementById("submissions");
      if (list && String(d.task_id) === list.dataset.task) refresh(list);
    });
    es.addEventListener("user_test", function (e) {
      var d = JSON.parse(e.data);
      refresh(document.getElementById("test-" + d.user_test_id));
    });
    ["announcement", "message", "question"].forEach(function (t) {
      es.addEventListener(t, function (e) {
        var d = JSON.parse(e.data);
        notify(d.text || t);
        beep();
        var page = document.getElementById("communication");
        if (page) { refresh(page); return; } // the list marks it read
        var badge = document.getElementById("unread");
        if (badge) { badge.textContent = String(Number(badge.textContent || 0) + 1); badge.hidden = false; }
      });
    });
    es.addEventListener("print", function () { refresh(document.getElementById("print-jobs")); });
    es.addEventListener("reload", function () { location.reload(); });
    // The organizers changed the times: fetch this contestant's window
    // (spread over two seconds so thousands of pages do not ask at once).
    es.addEventListener("clock", function () {
      setTimeout(function () {
        fetch(meta("cms-clock"), { credentials: "same-origin" }).then(function (r) { return r.json(); }).then(function (c) {
          if (c.phase !== meta("cms-phase")) { location.reload(); return; }
          document.querySelectorAll("[data-countdown]").forEach(function (el) {
            el.dataset.countdown = String(c.end);
            delete el.dataset.done;
          });
        }).catch(function () { /* next event */ });
      }, Math.random() * 2000);
    });
  }

  function countdowns() {
    var els = document.querySelectorAll("[data-countdown]");
    if (!els.length) return;
    var offset = Date.now() - Number(meta("server-time") || Date.now());
    function tick() {
      var now = Date.now() - offset;
      els.forEach(function (el) {
        var left = Math.max(0, Math.floor((Number(el.dataset.countdown) - now) / 1000));
        var h = Math.floor(left / 3600), m = Math.floor(left / 60) % 60, s = left % 60;
        el.textContent = h + ":" + (m < 10 ? "0" : "") + m + ":" + (s < 10 ? "0" : "") + s;
        if (left === 0 && !el.dataset.done) { el.dataset.done = "1"; setTimeout(function () { location.reload(); }, 1500); }
      });
    }
    tick();
    setInterval(tick, 1000);
  }

  document.addEventListener("DOMContentLoaded", function () {
    connect();
    countdowns();
    soundToggle();
    var ask = document.getElementById("enable-notifications");
    if (ask && window.Notification) {
      ask.hidden = Notification.permission !== "default";
      ask.addEventListener("click", function () { Notification.requestPermission(); ask.hidden = true; });
    }
  });
})();
