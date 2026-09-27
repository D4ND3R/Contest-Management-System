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
      // The result card follows the newest submission of its task.
      var card = document.getElementById("latest");
      if (card && String(d.task_id) === card.dataset.task && Number(d.submission_id) >= Number(card.dataset.sub || 0)) {
        card.dataset.src = card.dataset.base + "submissions/" + d.submission_id + "/card";
        refresh(card);
      }
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

  // The user menu closes when clicking elsewhere, or with Escape.
  document.addEventListener("click", function (e) {
    document.querySelectorAll("details.userbox[open]").forEach(function (d) {
      if (!d.contains(e.target)) d.removeAttribute("open");
    });
  });
  document.addEventListener("keydown", function (e) {
    if (e.key !== "Escape") return;
    document.querySelectorAll("details.userbox[open]").forEach(function (d) {
      d.removeAttribute("open");
      d.querySelector("summary").focus();
    });
  });

  // Checks of the submission form before sending: file sizes, each
  // source's extension against the chosen language, something to send.
  // The server checks all of it again; this only saves a round trip.
  function check(f) {
    var max = Number(f.dataset.max || 0), sel = f.querySelector('select[name="language"]');
    var opt = sel && sel.options[sel.selectedIndex];
    var exts = opt ? (opt.dataset.exts || "").split(" ").filter(Boolean) : [];
    var any = false, inputs = f.querySelectorAll('input[type="file"]');
    for (var i = 0; i < inputs.length; i++) {
      var files = inputs[i].files || [];
      for (var j = 0; j < files.length; j++) {
        var name = files[j].name, source = /\.%l$/.test(inputs[i].name);
        any = true;
        if (max && inputs[i].name !== "zip" && files[j].size > max) return f.dataset.msgSize;
        if (source && exts.length && name.lastIndexOf(".") > 0 &&
            !exts.some(function (x) { return name.slice(-x.length) === x; })) return f.dataset.msgExt;
      }
    }
    var src = f.querySelector('textarea[name="source"]');
    if (src && src.value.trim()) {
      any = true;
      if (max && new Blob([src.value]).size > max) return f.dataset.msgSize;
    }
    return any ? "" : f.dataset.msgEmpty;
  }
  document.addEventListener("submit", function (e) {
    var f = e.target;
    if (!f.classList || !f.classList.contains("checked")) return;
    var msg = check(f);
    if (!msg) return;
    e.preventDefault();
    e.stopPropagation(); // before htmx sends it
    var out = f.querySelector('[role="status"]');
    if (out) out.textContent = msg;
  }, true);

  // The code editor: a textarea where Tab indents (Shift+Tab unindents),
  // Esc then Tab leaves it (no keyboard trap), Ctrl+Enter submits, and a
  // draft survives reloads in this browser.
  var INDENT = "    ";
  function insert(t, text) {
    t.focus();
    if (!document.execCommand || !document.execCommand("insertText", false, text)) {
      t.setRangeText(text, t.selectionStart, t.selectionEnd, "end"); // no undo history, still correct
    }
  }
  function eachLine(t, fn) {
    var v = t.value, start = v.lastIndexOf("\n", t.selectionStart - 1) + 1, end = t.selectionEnd;
    if (end > start && v.charAt(end - 1) === "\n") end--;
    var out = v.slice(start, end).split("\n").map(fn).join("\n");
    t.setSelectionRange(start, end);
    insert(t, out);
    t.setSelectionRange(start, start + out.length);
  }
  function editor(t) {
    var key = "cms-draft:" + t.dataset.draft, leaving = false, timer;
    var saved = store(key);
    if (saved && !t.value) {
      t.value = saved;
      var d = t.closest("details");
      if (d) d.open = true;
    }
    t.addEventListener("input", function () {
      clearTimeout(timer);
      timer = setTimeout(function () { store(key, t.value); }, 400);
    });
    t.addEventListener("keydown", function (e) {
      if (e.key === "Escape") { leaving = true; return; }
      if (e.key === "Enter" && (e.ctrlKey || e.metaKey)) {
        e.preventDefault();
        if (t.form.requestSubmit) t.form.requestSubmit(); else t.form.submit();
        return;
      }
      if (e.key !== "Tab" || leaving || e.ctrlKey || e.altKey || e.metaKey) { leaving = false; return; }
      e.preventDefault();
      if (e.shiftKey) {
        eachLine(t, function (l) { return l.replace(/^( {1,4}|\t)/, ""); });
      } else if (t.selectionStart === t.selectionEnd) {
        insert(t, INDENT);
      } else {
        eachLine(t, function (l) { return INDENT + l; });
      }
    });
  }

  document.addEventListener("DOMContentLoaded", function () {
    connect();
    countdowns();
    soundToggle();
    document.querySelectorAll("textarea[data-draft]").forEach(editor);
    var ask = document.getElementById("enable-notifications");
    if (ask && window.Notification) {
      ask.hidden = Notification.permission !== "default";
      ask.addEventListener("click", function () { Notification.requestPermission(); ask.hidden = true; });
    }
  });
})();
