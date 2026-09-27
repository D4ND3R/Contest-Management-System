// CMS contest site (no framework): live updates over Server-Sent Events
// with a polling fallback, notifications, display preferences applied at
// once, countdowns, the submission form's checks and the code editor.
(function () {
  "use strict";
  var meta = function (n) { var m = document.querySelector('meta[name="' + n + '"]'); return m ? m.content : ""; };
  var $ = function (s, r) { return (r || document).querySelector(s); };
  var $$ = function (s, r) { return Array.prototype.slice.call((r || document).querySelectorAll(s)); };
  document.documentElement.classList.add("js");

  document.addEventListener("htmx:configRequest", function (e) {
    e.detail.headers["X-CSRF-Token"] = meta("csrf-token");
  });

  function store(k, v) {
    try { if (v === undefined) return localStorage.getItem(k); localStorage.setItem(k, v); } catch (err) { return null; }
    return null;
  }

  // ---- notifications ---------------------------------------------------
  function beep() {
    if (store("cms-sound") !== "1") return;
    try {
      var Ctx = window.AudioContext || window.webkitAudioContext, c = new Ctx(), o = c.createOscillator(), g = c.createGain();
      o.frequency.value = 880; g.gain.value = 0.08; o.connect(g); g.connect(c.destination);
      o.start(); o.stop(c.currentTime + 0.25); o.onended = function () { c.close(); };
    } catch (err) { /* no audio */ }
  }
  function notify(title, text, kind, sound) {
    var box = document.getElementById("notifications");
    if (!box) return;
    var n = document.createElement("div");
    n.className = "notice" + (kind ? " " + kind : "");
    n.setAttribute("role", "status");
    if (title) { var b = document.createElement("b"); b.textContent = title; n.appendChild(b); }
    if (text) { var t = document.createElement("span"); t.textContent = text; n.appendChild(t); }
    n.addEventListener("click", function () { n.remove(); });
    box.appendChild(n);
    setTimeout(function () { n.remove(); }, 12000);
    if (sound) beep();
    if (document.hidden && window.Notification && Notification.permission === "granted") {
      try { new Notification(title || document.title, { body: text || "" }); } catch (err) { /* ignore */ }
    }
  }
  function soundToggle() {
    var b = document.getElementById("sound-toggle");
    if (!b) return;
    var show = function () { b.textContent = store("cms-sound") === "1" ? b.dataset.on : b.dataset.off; };
    b.addEventListener("click", function () { store("cms-sound", store("cms-sound") === "1" ? "0" : "1"); show(); beep(); });
    show();
  }

  // ---- refreshing parts of the page --------------------------------------
  // An element with data-src is replaced by the fresh HTML found there.
  function refresh(el, then) {
    if (!el || !el.dataset.src) return;
    fetch(el.dataset.src, { credentials: "same-origin", headers: { "HX-Request": "true" } })
      .then(function (r) { return r.ok ? r.text() : Promise.reject(r.status); })
      .then(function (html) {
        var t = document.createElement("template");
        t.innerHTML = html.trim();
        var n = t.content.firstElementChild;
        if (!n || !el.parentNode) return;
        el.replaceWith(n);
        if (window.htmx) htmx.process(n);
        if (then) then(n);
      }).catch(function () { /* the next event or poll retries */ });
  }
  // A whole region of the current page (#sub-detail, #statement).
  function reloadRegion(id, then) {
    var el = document.getElementById(id);
    if (!el) return;
    fetch(location.href, { credentials: "same-origin" }).then(function (r) { return r.text(); }).then(function (html) {
      var doc = new DOMParser().parseFromString(html, "text/html"), n = doc.getElementById(id);
      if (n) { el.replaceWith(n); if (window.htmx) htmx.process(n); if (then) then(n); }
    }).catch(function () { /* retried later */ });
  }
  // What a judged row says: the verdict and the score.
  function verdictText(row) {
    var cell = function (i) { return row.cells[i] ? row.cells[i].textContent.replace(/\s+/g, " ").trim() : ""; };
    var score = row.cells.length > 5 ? cell(4) : "";
    return cell(3) + (score ? " · " + score : "");
  }
  function verdictKind(row) {
    var v = row.querySelector(".v");
    return v ? v.className.replace(/^v\s*/, "") : "";
  }
  function submissionChanged(id, taskID, final) {
    var title = (meta("cms-msg-judged") || "%s").replace("%s", "#" + id);
    var row = document.getElementById("sub-" + id);
    if (row) {
      refresh(row, function (n) { if (final) notify(title, verdictText(n), verdictKind(n), true); });
    } else {
      var list = document.getElementById("submissions");
      if (list && String(taskID) === list.dataset.task) refresh(list);
      else if (final) notify(title, "", "", true);
    }
    var det = document.getElementById("sub-detail");
    if (det && det.dataset.sub === String(id)) reloadRegion("sub-detail");
  }
  function bumpUnread(n) {
    var badge = document.getElementById("unread");
    if (!badge) return;
    var v = n === undefined ? Number((badge.lastChild && badge.lastChild.textContent) || 0) + 1 : n;
    if (badge.lastChild) badge.lastChild.textContent = String(v);
    badge.hidden = v <= 0;
  }

  // ---- live updates: SSE, with polling when it is not working ------------
  var lastSeen = 0, polling = null;
  function connect() {
    var url = meta("cms-events");
    if (!url) return;
    if (!window.EventSource) { startPolling(); return; }
    var es = new EventSource(url);
    var seen = function () { lastSeen = Date.now(); if (polling) { clearInterval(polling); polling = null; } };
    es.addEventListener("open", function () { /* pings prove it works */ });
    es.addEventListener("ping", seen);
    es.addEventListener("submission", function (e) {
      seen();
      var d = JSON.parse(e.data), st = d.status || "";
      submissionChanged(d.submission_id, d.task_id, st === "scored" || st === "compilation_failed" || st === "error");
    });
    es.addEventListener("user_test", function (e) {
      seen();
      var d = JSON.parse(e.data);
      refresh(document.getElementById("test-" + d.user_test_id));
    });
    ["announcement", "message", "question"].forEach(function (t) {
      es.addEventListener(t, function (e) {
        seen();
        var d = JSON.parse(e.data);
        notify(meta("cms-msg-" + t), d.text || "", "", true);
        var page = document.getElementById("communication");
        if (page) refresh(page); else bumpUnread();
      });
    });
    es.addEventListener("statement", function (e) {
      seen();
      var d = JSON.parse(e.data), st = document.getElementById("statement");
      notify((meta("cms-msg-statement") || "%s").replace("%s", d.text || ""), "", "", true);
      if (st && st.dataset.task === String(d.task_id)) reloadRegion("statement");
    });
    es.addEventListener("print", function () { seen(); refresh(document.getElementById("print-jobs")); });
    es.addEventListener("reload", function () { location.reload(); });
    // The organizers changed the times: fetch this contestant's window
    // (spread over two seconds so thousands of pages do not ask at once).
    es.addEventListener("clock", function () { seen(); setTimeout(syncClock, Math.random() * 2000); });
    // No ping for a minute: a proxy holds the stream back, or it is down.
    setInterval(function () {
      if (Date.now() - lastSeen > 60000 && !polling) startPolling();
    }, 15000);
    lastSeen = Date.now();
  }
  function syncClock() {
    fetch(meta("cms-clock"), { credentials: "same-origin" }).then(function (r) { return r.json(); }).then(function (c) {
      if (c.phase !== meta("cms-phase")) { location.reload(); return; }
      $$("[data-countdown]").forEach(function (el) { el.dataset.countdown = String(c.end); delete el.dataset.done; });
      if (typeof c.unread === "number") {
        var badge = document.getElementById("unread"), before = badge ? Number(badge.lastChild.textContent || 0) : 0;
        if (c.unread > before) {
          notify(meta("cms-msg-clar"), "", "", true);
          var page = document.getElementById("communication");
          if (page) refresh(page);
        }
        if (!document.getElementById("communication")) bumpUnread(c.unread);
      }
    }).catch(function () { /* next poll */ });
  }
  // Polling: what is being judged, the clock and new clarifications.
  function startPolling() {
    if (polling) return;
    var tick = function () {
      $$("tr[data-pending]").forEach(function (row) {
        var id = row.id.replace("sub-", "");
        refresh(row, function (n) {
          if (!n.dataset.pending && n.id.indexOf("sub-") === 0) {
            notify((meta("cms-msg-judged") || "%s").replace("%s", "#" + id), verdictText(n), verdictKind(n), true);
          }
        });
      });
      var det = document.getElementById("sub-detail");
      if (det && det.dataset.pending) reloadRegion("sub-detail");
      if (meta("cms-clock")) syncClock();
    };
    polling = setInterval(tick, 8000);
    tick();
  }

  // ---- countdowns ------------------------------------------------------
  var offset = 0;
  function fmt(left) {
    var h = Math.floor(left / 3600), m = Math.floor(left / 60) % 60, s = left % 60;
    return (h ? h + ":" + (m < 10 ? "0" : "") : "") + m + ":" + (s < 10 ? "0" : "") + s;
  }
  function countdowns() {
    offset = Date.now() - Number(meta("server-time") || Date.now());
    setInterval(tickAll, 1000);
    tickAll();
  }
  function tickAll() {
    var now = Date.now() - offset;
    $$("[data-countdown]").forEach(function (el) {
      var left = Math.max(0, Math.floor((Number(el.dataset.countdown) - now) / 1000));
      var h = Math.floor(left / 3600), m = Math.floor(left / 60) % 60, s = left % 60;
      el.textContent = h + ":" + (m < 10 ? "0" : "") + m + ":" + (s < 10 ? "0" : "") + s;
      if (left === 0 && !el.dataset.done) { el.dataset.done = "1"; setTimeout(function () { location.reload(); }, 1500); }
    });
    // The minimum interval between submissions: the button waits.
    $$("button[data-ready-at]").forEach(function (b) {
      if (!b.dataset.label) b.dataset.label = b.textContent;
      var left = Math.ceil((Number(b.dataset.readyAt) - now) / 1000);
      if (left > 0) { b.disabled = true; b.textContent = (b.dataset.wait || "%s").replace("%s", fmt(left)); }
      else { b.disabled = false; b.textContent = b.dataset.label; b.removeAttribute("data-ready-at"); }
    });
  }

  // ---- display preferences: applied at once --------------------------------
  function prefs() {
    var f = document.getElementById("prefs");
    if (!f) return;
    f.addEventListener("change", function (e) {
      var sel = e.target, root = document.documentElement;
      if (sel.dataset.attr) {
        if (sel.value) root.setAttribute("data-" + sel.dataset.attr, sel.value); else root.removeAttribute("data-" + sel.dataset.attr);
      }
      var body = new URLSearchParams(new FormData(f));
      fetch(f.action, { method: "POST", body: body, credentials: "same-origin",
        headers: { "HX-Request": "true", "X-CSRF-Token": meta("csrf-token") } })
        .then(function () { if (sel.hasAttribute("data-reload")) location.reload(); })
        .catch(function () { f.submit(); });
    });
  }

  // ---- after sending a submission --------------------------------------
  function onSubmitted(e) {
    var d = e.detail || {};
    notify(d.title || "", d.text || "", "", false);
  }

  // ---- checks of the submission form before sending ------------------------
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

  // ---- the code editor ---------------------------------------------------
  // Tab indents (Shift+Tab unindents), Esc then Tab leaves it (no keyboard
  // trap), Ctrl+Enter submits, and a draft survives reloads.
  var INDENT = "    ";
  function insert(t, text) {
    t.focus();
    if (!document.execCommand || !document.execCommand("insertText", false, text)) {
      t.setRangeText(text, t.selectionStart, t.selectionEnd, "end");
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
    if (t.dataset.ready) return;
    t.dataset.ready = "1";
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
  // The Submissions tab is replaced after sending: set up its editor again.
  document.addEventListener("htmx:afterSwap", function () { $$("textarea[data-draft]").forEach(editor); tickAll(); });

  document.addEventListener("DOMContentLoaded", function () {
    document.body.addEventListener("cms-submitted", onSubmitted);
    connect();
    countdowns();
    soundToggle();
    prefs();
    $$("textarea[data-draft]").forEach(editor);
    var ask = document.getElementById("enable-notifications");
    if (ask && window.Notification) {
      ask.hidden = Notification.permission !== "default";
      ask.addEventListener("click", function () { Notification.requestPermission(); ask.hidden = true; });
    }
  });
})();
