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
    es.addEventListener("alert", function (e) {
      var d = JSON.parse(e.data);
      notify("System error: " + (d.text || "see the overview"), "bad");
    });
    es.addEventListener("question_new", function (e) {
      var d = JSON.parse(e.data);
      notify("New question: " + (d.text || ""));
      var list = document.getElementById("questions");
      if (list && list.dataset.src && window.htmx) htmx.ajax("GET", list.dataset.src, { target: list, swap: "outerHTML" });
    });
  }

  // Dataset form: show only the options of the chosen task type.
  function taskTypes() {
    document.querySelectorAll("select[data-type-switch]").forEach(function (sel) {
      var form = sel.form;
      function apply() {
        form.querySelectorAll("fieldset[data-for]").forEach(function (fs) {
          var on = fs.dataset["for"].split(" ").indexOf(sel.value) >= 0;
          fs.hidden = !on;
        });
      }
      sel.addEventListener("change", apply);
      apply();
    });
  }

  document.addEventListener("DOMContentLoaded", function () { connect(); taskTypes(); });
})();
