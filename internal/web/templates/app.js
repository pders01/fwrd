// Progressive enhancement for the read/star toggle forms. With JavaScript
// off, the forms POST normally and the server's Post/Redirect/Get reloads
// the page — the baseline behaviour. With JavaScript on, this intercepts
// the submit, POSTs in the background, and flips the affected row in place
// so the page does not reload and scroll position is kept.
//
// The server stays the source of truth: the same POST endpoints, the same
// same-origin guard. `redirect: "manual"` keeps the browser from following
// the 303 to a full page render we would only throw away — an opaque
// redirect response signals success.
(function () {
  "use strict";

  function labelFor(kind, willActivate) {
    // willActivate is the state the article moves *into* after this click.
    if (kind === "read") return willActivate ? "mark unread" : "mark read";
    return willActivate ? "unstar" : "star"; // kind === "star"
  }

  function apply(form, kind) {
    var field = kind === "read" ? "read" : "starred";
    var input = form.querySelector('input[name="' + field + '"]');
    var button = form.querySelector("button");
    if (!input || !button) return;

    // input.value is the action just sent: "1" activates, "0" clears.
    var willActivate = input.value === "1";

    var li = form.closest("li");
    if (li) li.classList.toggle(kind === "read" ? "read" : "starred", willActivate);
    if (kind === "star") button.setAttribute("aria-pressed", String(willActivate));

    // Flip the hidden value so the next click sends the opposite action,
    // and relabel the button to match the new state.
    input.value = willActivate ? "0" : "1";
    button.textContent = labelFor(kind, !willActivate);
  }

  function onSubmit(event) {
    var form = event.target;
    if (!form.classList || !form.classList.contains("js-toggle")) return;
    var kind = form.dataset ? form.dataset.kind : null;
    if (kind !== "read" && kind !== "star") return;

    event.preventDefault();
    var button = form.querySelector("button");
    if (button) button.disabled = true;

    fetch(form.action, {
      method: "POST",
      body: new FormData(form),
      redirect: "manual",
      headers: { "X-Requested-With": "fetch" },
    })
      .then(function (res) {
        // A manual-redirect 303 surfaces as an opaque response (type
        // "opaqueredirect"); a 2xx is also success. Anything else means
        // the toggle did not take, so fall back to a real submit.
        if (res.type === "opaqueredirect" || res.ok) {
          apply(form, kind);
        } else {
          form.submit();
        }
      })
      .catch(function () {
        form.submit();
      })
      .finally(function () {
        if (button) button.disabled = false;
      });
  }

  document.addEventListener("submit", onSubmit);
})();

// Feed-management page: client-side filter and sort. The list is fully
// rendered server-side and title-sorted, so with JavaScript off the page is
// a complete, usable feed list; this only narrows and reorders what is
// already present. Marking <body> .has-js reveals the (otherwise hidden)
// filter/sort controls.
(function () {
  "use strict";
  var list = document.getElementById("feed-list");
  if (!list) return;
  document.body.classList.add("has-js");

  var items = Array.prototype.slice.call(list.querySelectorAll(".feed-item"));
  var filter = document.getElementById("feed-filter");
  var sorter = document.getElementById("feed-sort");
  var noMatch = document.getElementById("feed-no-match");

  function searchText(li) {
    var t = li.querySelector(".feed-title");
    var h = li.querySelector(".feed-host");
    return ((t ? t.textContent : "") + " " + (h ? h.textContent : "")).toLowerCase();
  }

  function applyFilter() {
    var q = (filter ? filter.value : "").trim().toLowerCase();
    var visible = 0;
    items.forEach(function (li) {
      var show = q === "" || searchText(li).indexOf(q) !== -1;
      li.hidden = !show;
      if (show) visible++;
    });
    if (noMatch) noMatch.hidden = visible !== 0;
  }

  function num(li, attr) {
    return parseInt(li.getAttribute(attr), 10) || 0;
  }

  function applySort() {
    var mode = sorter ? sorter.value : "title";
    var sorted = items.slice().sort(function (a, b) {
      if (mode === "unread") return num(b, "data-unread") - num(a, "data-unread");
      if (mode === "updated") return num(b, "data-updated") - num(a, "data-updated");
      var ta = a.querySelector(".feed-title"),
        tb = b.querySelector(".feed-title");
      return (ta ? ta.textContent : "").localeCompare(tb ? tb.textContent : "");
    });
    // Re-append in sorted order; appendChild moves existing nodes, so the
    // list is reordered without a rebuild.
    sorted.forEach(function (li) {
      list.appendChild(li);
    });
  }

  if (filter) filter.addEventListener("input", applyFilter);
  if (sorter) sorter.addEventListener("change", applySort);
})();

// Re-apply the front page's autofocus when the page is restored from the
// back/forward cache. A bfcache restore does not re-parse the document, so
// the `autofocus` attribute never fires again — navigating back from an
// article would otherwise leave the search field blurred. pageshow with
// event.persisted is the bfcache-restore signal; a normal load honours
// autofocus on its own and reports persisted === false here.
(function () {
  "use strict";
  window.addEventListener("pageshow", function (event) {
    if (!event.persisted) return;
    var input = document.querySelector("input[autofocus]");
    if (!input) return;
    input.focus();
    // Drop the caret after any text the browser restored.
    var n = input.value.length;
    try {
      input.setSelectionRange(n, n);
    } catch (e) {
      // type="search" rejects setSelectionRange in some browsers; focus alone is enough.
    }
  });
})();
