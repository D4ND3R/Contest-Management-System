// Admin web glue: CSRF header for htmx, confirmations, live notifications
// and task type parameter templates. No inline code (strict CSP).
(function () {
  "use strict";
  var meta = function (n) { var m = document.querySelector('meta[name="' + n + '"]'); return m ? m.content : ""; };

  document.addEventListener("htmx:configRequest", function (e) {
    e.detail.headers["X-CSRF-Token"] = meta("csrf-token");
  });

  document.addEventListener("submit", function (e) {
    var msg = e.target.getAttribute && e.target.getAttribute("data-confirm");
    if (msg && !window.confirm(msg)) e.preventDefault();
  }, true);

  document.addEventListener("click", function (e) {
    var a = e.target.closest && e.target.closest("[data-back]");
    if (a && history.length > 1) { e.preventDefault(); history.back(); }
  });

  function notify(text, cls) {
    var box = document.getElementById("notifications");
    if (!box) return;
    var n = document.createElement("div");
    n.className = "notice" + (cls ? " " + cls : "");
    n.textContent = text;
    n.addEventListener("click", function () { n.remove(); });
    box.appendChild(n);
    setTimeout(function () { n.remove(); }, 15000);
  }

  function connect() {
    var url = meta("cms-events");
    if (!url || !window.EventSource) return;
    var es = new EventSource(url);
    // Translated labels come from the page (data-* of #notifications).
    var t = (document.getElementById("notifications") || { dataset: {} }).dataset;
    es.addEventListener("alert", function (e) {
      var d = JSON.parse(e.data);
      notify((t.alert || "System error:") + " " + (d.text || t.overview || ""), "bad");
    });
    es.addEventListener("question_new", function (e) {
      var d = JSON.parse(e.data);
      notify((t.question || "New question:") + " " + (d.text || ""));
      ["questions", "q-count"].forEach(function (id) {
        var el = document.getElementById(id);
        if (el && el.dataset.src && window.htmx) htmx.ajax("GET", el.dataset.src, { target: el, swap: "outerHTML" });
      });
    });
  }

  // Dataset form: show only the options of the chosen task or score type
  // (select[data-type-switch=G] drives the [data-group=G][data-for] parts).
  function switches() {
    document.querySelectorAll("select[data-type-switch]").forEach(function (sel) {
      var group = sel.getAttribute("data-type-switch");
      function apply() {
        sel.form.querySelectorAll('[data-for][data-group="' + group + '"]').forEach(function (el) {
          el.hidden = el.dataset["for"].split(" ").indexOf(sel.value) < 0;
        });
      }
      if (!sel.dataset.bound) { sel.dataset.bound = "1"; sel.addEventListener("change", apply); }
      apply();
    });
  }

  // Drop zones: files dropped on a [data-dropzone] fill its file input.
  function dropzones() {
    document.querySelectorAll("[data-dropzone]").forEach(function (z) {
      var input = z.querySelector("input[type=file]"), name = z.querySelector("[data-filename]");
      if (!input || z.dataset.bound) return;
      z.dataset.bound = "1";
      var show = function () { if (name) name.textContent = Array.prototype.map.call(input.files, function (f) { return f.name; }).join(", "); };
      ["dragenter", "dragover"].forEach(function (ev) { z.addEventListener(ev, function (e) { e.preventDefault(); z.classList.add("over"); }); });
      ["dragleave", "drop"].forEach(function (ev) { z.addEventListener(ev, function () { z.classList.remove("over"); }); });
      z.addEventListener("drop", function (e) {
        e.preventDefault();
        if (e.dataTransfer.files.length) { input.files = e.dataTransfer.files; show(); }
      });
      input.addEventListener("change", show);
    });
  }

  function init() { switches(); dropzones(); }
  document.addEventListener("DOMContentLoaded", function () { connect(); init(); });
  document.addEventListener("htmx:afterSettle", init);
})();
