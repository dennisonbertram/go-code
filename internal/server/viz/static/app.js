/* harnessd session visualizer — slice 1 shell.
 *
 * Token story: the API key arrives via the landing form or a ?token= query
 * param on first load, is kept in sessionStorage, and is sent only as an
 * Authorization: Bearer header on fetch() calls to the existing /v1 API.
 * The server never accepts tokens via query string; ?token= is consumed
 * client-side and immediately scrubbed from the address bar.
 *
 * This slice proves connectivity against GET /v1/runs and renders hash-based
 * placeholders. Real list/detail/timeline views land in later slices.
 */
(function () {
  "use strict";

  var TOKEN_KEY = "harness-viz-token";

  var statusEl = document.getElementById("status");
  var tokenPanel = document.getElementById("token-panel");
  var tokenForm = document.getElementById("token-form");
  var tokenInput = document.getElementById("token-input");
  var tokenError = document.getElementById("token-error");
  var viewPanel = document.getElementById("view-panel");
  var viewEl = document.getElementById("view");

  function setStatus(state, label) {
    statusEl.textContent = label;
    statusEl.className = "badge badge-" + state;
  }

  function currentToken() {
    return sessionStorage.getItem(TOKEN_KEY) || "";
  }

  function scrubTokenFromURL() {
    var params = new URLSearchParams(window.location.search);
    if (!params.has("token")) {
      return;
    }
    var token = params.get("token");
    if (token) {
      sessionStorage.setItem(TOKEN_KEY, token);
    }
    params.delete("token");
    var search = params.toString();
    var url = window.location.pathname + (search ? "?" + search : "") + window.location.hash;
    window.history.replaceState(null, "", url);
  }

  function showTokenForm(message) {
    tokenPanel.hidden = false;
    viewPanel.hidden = true;
    setStatus("idle", "disconnected");
    if (message) {
      tokenError.textContent = message;
      tokenError.hidden = false;
    } else {
      tokenError.hidden = true;
    }
  }

  function showView() {
    tokenPanel.hidden = true;
    viewPanel.hidden = false;
    renderRoute();
  }

  function renderRoute() {
    var hash = window.location.hash || "#/runs";
    var match = hash.match(/^#\/runs\/(.+)$/);
    if (match) {
      viewEl.innerHTML =
        "<h2>Run detail</h2><p class='muted'>Placeholder for run <code>" +
        escapeHTML(match[1]) +
        "</code> — metadata, summary, and the event timeline arrive in later slices.</p>";
      return;
    }
    viewEl.innerHTML =
      "<h2>Runs</h2><p class='muted'>Placeholder — the runs list arrives in a later slice. " +
      "Connectivity check passed: the API accepted this key.</p>";
  }

  function escapeHTML(s) {
    return s.replace(/[&<>"']/g, function (c) {
      return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c];
    });
  }

  function connect() {
    var token = currentToken();
    if (!token) {
      showTokenForm("");
      return;
    }
    setStatus("idle", "connecting…");
    fetch("/v1/runs?limit=1", {
      headers: { Authorization: "Bearer " + token }
    })
      .then(function (res) {
        if (res.status === 401) {
          sessionStorage.removeItem(TOKEN_KEY);
          showTokenForm("Unauthorized — check the API key.");
          return null;
        }
        if (res.status === 403) {
          sessionStorage.removeItem(TOKEN_KEY);
          showTokenForm("This key lacks the runs:read scope.");
          return null;
        }
        if (!res.ok) {
          throw new Error("GET /v1/runs failed: HTTP " + res.status);
        }
        return res.json();
      })
      .then(function (body) {
        if (body === null) {
          return;
        }
        setStatus("ok", "connected — read-only");
        showView();
      })
      .catch(function (err) {
        setStatus("err", "connection failed");
        showTokenForm(String(err && err.message ? err.message : err));
      });
  }

  tokenForm.addEventListener("submit", function (ev) {
    ev.preventDefault();
    var token = tokenInput.value.trim();
    if (!token) {
      showTokenForm("Enter an API key first.");
      return;
    }
    sessionStorage.setItem(TOKEN_KEY, token);
    tokenInput.value = "";
    connect();
  });

  window.addEventListener("hashchange", function () {
    if (!viewPanel.hidden) {
      renderRoute();
    }
  });

  scrubTokenFromURL();
  connect();
})();
