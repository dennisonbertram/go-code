/* harnessd session visualizer — slices 1–4.
 *
 * Token story: the API key arrives via the landing form or a ?token= query
 * param on first load, is kept in sessionStorage, and is sent only as an
 * Authorization: Bearer header on fetch() calls to the existing /v1 API.
 * The server never accepts tokens via query string; ?token= is consumed
 * client-side and immediately scrubbed from the address bar.
 *
 * Views: hash routing between #/runs (runs list from GET /v1/runs,
 * client-side status/text filtering) and #/runs/{id} (run detail from
 * GET /v1/runs/{id} plus GET /v1/runs/{id}/summary when available, plus the
 * event timeline streamed from GET /v1/runs/{id}/events — slice 4). Search
 * (slice 5) lands later. Read-only: only GET endpoints are ever called.
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

  // List-view client-side filter state (no new endpoints — filtering
  // happens on the already-fetched runs array).
  var listState = { runs: null, status: "", text: "" };

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

  function escapeHTML(s) {
    return String(s).replace(/[&<>"']/g, function (c) {
      return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c];
    });
  }

  // apiFetch wraps fetch with the Bearer header and uniform auth handling:
  // 401/403 clears the stored key and returns the user to the token form.
  // Resolves to the parsed JSON body, or null when auth handling took over.
  function apiFetch(path) {
    return fetch(path, {
      headers: { Authorization: "Bearer " + currentToken() }
    }).then(function (res) {
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
        var err = new Error("GET " + path + " failed: HTTP " + res.status);
        err.status = res.status;
        throw err;
      }
      return res.json();
    });
  }

  // ---- formatting helpers ----

  function formatTime(iso) {
    if (!iso) {
      return "—";
    }
    var d = new Date(iso);
    if (isNaN(d.getTime())) {
      return iso;
    }
    return d.toLocaleString();
  }

  function formatInt(n) {
    return Number(n).toLocaleString();
  }

  function formatUSD(n) {
    if (typeof n !== "number" || !isFinite(n)) {
      return "—";
    }
    return "$" + n.toFixed(4);
  }

  // runCost extracts a cost figure from a list-row run object when the API
  // provides one. The current /v1/runs payload does not serialize cost
  // fields, so this returns null and the column renders "—" until it does.
  function runCost(run) {
    var v = run.total_cost_usd != null ? run.total_cost_usd : run.cost_usd;
    return typeof v === "number" && isFinite(v) ? v : null;
  }

  function statusBadge(status) {
    var s = String(status || "unknown");
    return '<span class="status status-' + escapeHTML(s) + '">' + escapeHTML(s) + "</span>";
  }

  function excerpt(s, max) {
    s = String(s || "").replace(/\s+/g, " ").trim();
    if (s.length <= max) {
      return s;
    }
    return s.slice(0, max - 1) + "…";
  }

  // ---- routing ----

  function renderRoute() {
    stopTimeline();
    var hash = window.location.hash || "#/runs";
    var match = hash.match(/^#\/runs\/([^/]+)$/);
    if (match) {
      renderRunDetail(decodeURIComponent(match[1]));
      return;
    }
    renderRunsList();
  }

  // ---- runs list view (#/runs) ----

  function renderRunsList() {
    viewEl.innerHTML = '<p class="muted">Loading runs…</p>';
    apiFetch("/v1/runs")
      .then(function (body) {
        if (body === null) {
          return; // auth handling took over
        }
        listState.runs = (body && body.runs) || [];
        paintRunsList();
      })
      .catch(function (err) {
        viewEl.innerHTML =
          '<p class="error">Failed to load runs: ' + escapeHTML(err.message) + "</p>" +
          '<p><a href="#/runs">Retry</a></p>';
      });
  }

  function distinctStatuses(runs) {
    var seen = {};
    var out = [];
    runs.forEach(function (r) {
      var s = String(r.status || "unknown");
      if (!seen[s]) {
        seen[s] = true;
        out.push(s);
      }
    });
    return out.sort();
  }

  function filteredRuns() {
    var text = listState.text.toLowerCase();
    return listState.runs.filter(function (r) {
      if (listState.status && String(r.status || "unknown") !== listState.status) {
        return false;
      }
      if (!text) {
        return true;
      }
      var hay = [r.id, r.prompt, r.model, r.conversation_id]
        .map(function (v) { return String(v || "").toLowerCase(); })
        .join("\n");
      return hay.indexOf(text) !== -1;
    });
  }

  // paintRunsList builds the filter bar and results container once per
  // fetch; paintRunsRows re-renders only the results on filter changes so
  // the text input keeps focus while typing.
  function paintRunsList() {
    var runs = listState.runs;
    var html = "<h2>Runs</h2>";

    if (runs.length === 0) {
      viewEl.innerHTML = html +
        '<div class="empty-state">' +
        "<p>No runs yet.</p>" +
        '<p class="muted">Start one with <code>harnesscli --prompt "…"</code>, then reload this page.</p>' +
        "</div>";
      return;
    }

    var statuses = distinctStatuses(runs);
    html += '<div class="filter-bar">' +
      '<select id="filter-status" aria-label="Filter by status">' +
      '<option value="">all statuses</option>';
    statuses.forEach(function (s) {
      html += '<option value="' + escapeHTML(s) + '"' +
        (listState.status === s ? " selected" : "") + ">" + escapeHTML(s) + "</option>";
    });
    html += "</select>" +
      '<input id="filter-text" type="search" placeholder="filter by prompt, model, or id…" ' +
      'aria-label="Filter runs by text" value="' + escapeHTML(listState.text) + '">' +
      "</div>" +
      '<div id="runs-results"></div>';

    viewEl.innerHTML = html;

    document.getElementById("filter-status").addEventListener("change", function (ev) {
      listState.status = ev.target.value;
      paintRunsRows();
    });
    document.getElementById("filter-text").addEventListener("input", function (ev) {
      listState.text = ev.target.value;
      paintRunsRows();
    });

    paintRunsRows();
  }

  function paintRunsRows() {
    var runs = listState.runs;
    var rows = filteredRuns();
    var html;

    if (rows.length === 0) {
      html = '<div class="empty-state"><p>No runs match the current filters.</p></div>';
    } else {
      html = '<table class="runs-table"><thead><tr>' +
        "<th>Status</th><th>Model</th><th>Prompt</th><th>Created</th><th>Cost</th>" +
        "</tr></thead><tbody>";
      rows.forEach(function (r) {
        var cost = runCost(r);
        html += "<tr>" +
          "<td>" + statusBadge(r.status) + "</td>" +
          "<td>" + escapeHTML(r.model || "—") + "</td>" +
          '<td><a href="#/runs/' + encodeURIComponent(r.id) + '">' +
          escapeHTML(excerpt(r.prompt, 80) || "(no prompt)") + "</a></td>" +
          "<td>" + escapeHTML(formatTime(r.created_at)) + "</td>" +
          "<td>" + (cost === null ? "—" : escapeHTML(formatUSD(cost))) + "</td>" +
          "</tr>";
      });
      html += "</tbody></table>" +
        '<p class="muted">' + rows.length + " of " + runs.length + " runs shown · newest first</p>";
    }

    document.getElementById("runs-results").innerHTML = html;
  }

  // ---- run detail view (#/runs/{id}) ----

  function renderRunDetail(runID) {
    viewEl.innerHTML = '<p class="muted">Loading run…</p>';

    var run = null;
    var summary = null;
    var summaryNote = null;

    // The summary endpoint serves runs held in the daemon's in-memory
    // runner: 404 for historical (post-restart) runs, 409 for unfinished
    // ones. Both are expected states, not failures of the detail view.
    var fetchSummary = apiFetch("/v1/runs/" + encodeURIComponent(runID) + "/summary")
      .then(function (body) {
        summary = body;
      })
      .catch(function (err) {
        if (err && err.status === 404) {
          summaryNote = "Summary unavailable — the daemon no longer holds this run in memory (historical run).";
        } else if (err && err.status === 409) {
          summaryNote = "Summary unavailable — the run has not finished yet.";
        } else {
          summaryNote = "Summary unavailable — " + (err && err.message ? err.message : "unknown error");
        }
      });

    apiFetch("/v1/runs/" + encodeURIComponent(runID))
      .then(function (body) {
        if (body === null) {
          return null; // auth handling took over
        }
        run = body;
        return fetchSummary;
      })
      .then(function () {
        if (run === null) {
          return;
        }
        paintRunDetail(runID, run, summary, summaryNote);
      })
      .catch(function (err) {
        if (err && err.status === 404) {
          viewEl.innerHTML =
            '<div class="empty-state"><p>Run <code>' + escapeHTML(runID) + "</code> not found.</p>" +
            '<p><a href="#/runs">← back to runs</a></p></div>';
          return;
        }
        viewEl.innerHTML =
          '<p class="error">Failed to load run: ' + escapeHTML(err.message) + "</p>" +
          '<p><a href="#/runs">← back to runs</a></p>';
      });
  }

  function paintRunDetail(runID, run, summary, summaryNote) {
    var html = '<p><a href="#/runs">← back to runs</a></p>' +
      "<h2>Run <code>" + escapeHTML(runID) + "</code> " + statusBadge(run.status) + "</h2>";

    html += '<dl class="detail-grid">' +
      detailRow("model", run.model) +
      detailRow("provider", run.provider_name) +
      detailRow("created", formatTime(run.created_at)) +
      detailRow("updated", formatTime(run.updated_at)) +
      detailRow("conversation", run.conversation_id) +
      detailRow("tenant", run.tenant_id) +
      "</dl>";

    if (run.prompt) {
      html += "<h3>Prompt</h3><pre class=\"block\">" + escapeHTML(run.prompt) + "</pre>";
    }
    if (run.error) {
      html += '<h3>Error</h3><pre class="block error">' + escapeHTML(run.error) + "</pre>";
    }

    html += "<h3>Summary</h3>";
    if (summary) {
      html += '<dl class="detail-grid">' +
        detailRow("steps taken", formatInt(summary.steps_taken || 0)) +
        detailRow("prompt tokens", formatInt(summary.total_prompt_tokens || 0)) +
        detailRow("completion tokens", formatInt(summary.total_completion_tokens || 0)) +
        detailRow("total cost", formatUSD(summary.total_cost_usd)) +
        detailRow("cost status", summary.cost_status) +
        detailRow("cache hit rate", ((summary.cache_hit_rate || 0) * 100).toFixed(1) + "%") +
        "</dl>";
      if (summary.tool_calls && summary.tool_calls.length > 0) {
        html += '<table class="runs-table"><thead><tr><th>Tool</th><th>Calls</th></tr></thead><tbody>';
        summary.tool_calls.forEach(function (tc) {
          html += "<tr><td>" + escapeHTML(tc.name || "?") + "</td><td>" +
            escapeHTML(formatInt(tc.count || 0)) + "</td></tr>";
        });
        html += "</tbody></table>";
      }
    } else {
      html += '<p class="muted">' + escapeHTML(summaryNote || "Summary unavailable.") + "</p>";
    }

    html += "<h3>Event timeline</h3>" +
      '<p class="muted" id="timeline-status">Connecting to the event stream…</p>' +
      '<div id="timeline" class="timeline"></div>';
    viewEl.innerHTML = html;

    activeTimeline = startTimeline(
      runID,
      document.getElementById("timeline"),
      document.getElementById("timeline-status")
    );
  }

  function detailRow(label, value) {
    return "<dt>" + escapeHTML(label) + "</dt><dd>" +
      escapeHTML(value == null || value === "" ? "—" : value) + "</dd>";
  }

  // ---- event timeline (slice 4) ----
  //
  // The timeline is fed by GET /v1/runs/{id}/events: the server replays the
  // run's full history, then follows live, and closes after the terminal
  // event. Native EventSource cannot set headers, so a fetch-based SSE
  // reader sends the Bearer token; on a dropped stream it reconnects with
  // Last-Event-ID (honoring the server's retry: hint) and dedupes by seq so
  // an overlapped resume never renders a row twice.

  var activeTimeline = null;

  function stopTimeline() {
    if (activeTimeline) {
      activeTimeline.abort();
      activeTimeline = null;
    }
  }

  function startTimeline(runID, container, statusEl) {
    var state = {
      aborted: false,
      controller: typeof AbortController !== "undefined" ? new AbortController() : null,
      seen: {},
      lastEventID: "",
      retryMs: 3000, // server's retry: frame hint overrides this
      toolCalls: {},
      bubble: null,
      bubbleSeqEl: null,
      bubbleText: "",
      paused: false,
      terminal: false
    };

    container.addEventListener("scroll", function () {
      var wasPaused = state.paused;
      state.paused = container.scrollTop + container.clientHeight < container.scrollHeight - 40;
      if (wasPaused && !state.paused) {
        container.scrollTop = container.scrollHeight;
      }
    });

    function setStatus(msg) {
      statusEl.textContent = msg;
    }

    function mkEl(tag, className, html) {
      var e = document.createElement(tag);
      if (className) {
        e.className = className;
      }
      if (html != null) {
        e.innerHTML = html;
      }
      return e;
    }

    function seqOf(ev) {
      var id = ev.id || "";
      var idx = id.lastIndexOf(":");
      if (idx < 0) {
        return "";
      }
      return id.slice(idx + 1);
    }

    function rowWithBody(ev, extraClass) {
      var r = mkEl("div", "tl-row " + extraClass);
      r.appendChild(mkEl("span", "seq", "#" + escapeHTML(seqOf(ev))));
      var body = mkEl("div", "tl-body");
      r.appendChild(body);
      return { row: r, body: body };
    }

    function appendRow(r) {
      container.appendChild(r);
      if (!state.paused) {
        container.scrollTop = container.scrollHeight;
      }
    }

    function closeBubble() {
      state.bubble = null;
      state.bubbleSeqEl = null;
      state.bubbleText = "";
    }

    function renderEvent(ev) {
      var seq = seqOf(ev);
      if (seq !== "") {
        if (state.seen[seq]) {
          return; // duplicate from an overlapped resume
        }
        state.seen[seq] = true;
      }
      if (ev.id) {
        state.lastEventID = ev.id;
      }
      var p = ev.payload || {};
      var rb;
      switch (ev.type) {
        case "run.started":
          rb = rowWithBody(ev, "tl-lifecycle");
          rb.body.innerHTML = "<strong>Run started</strong>" +
            (p.prompt ? ' <span class="muted">— ' + escapeHTML(excerpt(String(p.prompt), 120)) + "</span>" : "");
          appendRow(rb.row);
          break;
        case "run.queued":
        case "run.waiting_for_user":
        case "run.resumed": {
          var label = {
            "run.queued": "Run queued",
            "run.waiting_for_user": "Waiting for user input…",
            "run.resumed": "Run resumed"
          }[ev.type];
          rb = rowWithBody(ev, "tl-lifecycle");
          rb.body.innerHTML = "<strong>" + label + "</strong>";
          appendRow(rb.row);
          break;
        }
        case "run.completed": {
          var extras = "";
          if (p.usage_totals && typeof p.usage_totals.total_tokens === "number") {
            extras += " · " + escapeHTML(formatInt(p.usage_totals.total_tokens)) + " tokens";
          }
          if (p.cost_totals && typeof p.cost_totals.cost_usd_total === "number") {
            extras += " · total cost " + escapeHTML(formatUSD(p.cost_totals.cost_usd_total));
          }
          rb = rowWithBody(ev, "tl-lifecycle tl-ok");
          rb.body.innerHTML = "<strong>Run completed</strong>" + extras;
          appendRow(rb.row);
          state.terminal = true;
          setStatus("Stream closed — run completed.");
          break;
        }
        case "run.failed":
        case "run.cancelled": {
          var failed = ev.type === "run.failed";
          rb = rowWithBody(ev, "tl-lifecycle tl-err");
          rb.body.innerHTML = "<strong>" + (failed ? "Run failed" : "Run cancelled") + "</strong>" +
            (failed && p.error ? ' <span class="muted">— ' + escapeHTML(String(p.error)) + "</span>" : "");
          appendRow(rb.row);
          state.terminal = true;
          setStatus("Stream closed — run " + (failed ? "failed" : "cancelled") + ".");
          break;
        }
        case "run.step.started":
        case "run.step.completed": {
          var stepLabel = ev.type === "run.step.started" ? "Step " + p.step + " started" : "Step " + p.step + " completed";
          rb = rowWithBody(ev, "tl-quiet");
          rb.body.innerHTML = '<span class="muted">' + escapeHTML(stepLabel) + "</span>";
          appendRow(rb.row);
          break;
        }
        case "llm.turn.requested":
          rb = rowWithBody(ev, "tl-llm");
          rb.body.innerHTML = "LLM turn " + escapeHTML(String(p.step != null ? p.step : "?")) + " requested";
          appendRow(rb.row);
          break;
        case "llm.turn.completed": {
          var parts = ["LLM turn " + escapeHTML(String(p.step != null ? p.step : "?")) + " completed"];
          if (typeof p.tool_calls === "number") {
            parts.push(escapeHTML(formatInt(p.tool_calls)) + " tool calls");
          }
          if (typeof p.total_duration_ms === "number") {
            parts.push(escapeHTML(formatInt(p.total_duration_ms)) + "ms");
          }
          if (p.provider) {
            parts.push("(" + escapeHTML(String(p.provider)) + ")");
          }
          rb = rowWithBody(ev, "tl-llm");
          rb.body.innerHTML = parts.join(" · ");
          appendRow(rb.row);
          break;
        }
        case "assistant.message.delta":
          if (!state.bubble) {
            rb = rowWithBody(ev, "tl-assistant");
            state.bubbleSeqEl = rb.row.children[0];
            state.bubble = mkEl("div", "tl-bubble", "");
            rb.body.appendChild(state.bubble);
            state.bubbleText = "";
            appendRow(rb.row);
          }
          state.bubbleText += p.content || "";
          state.bubble.innerHTML = escapeHTML(state.bubbleText);
          break;
        case "assistant.message":
          if (state.bubble) {
            // Deltas fold into one row per message: the final content
            // replaces the streamed preview and the final event's seq
            // claims the row badge, keeping seq correlation accurate.
            state.bubble.innerHTML = escapeHTML(String(p.content || ""));
            if (state.bubbleSeqEl) {
              state.bubbleSeqEl.innerHTML = "#" + escapeHTML(seq);
            }
            closeBubble();
          } else {
            rb = rowWithBody(ev, "tl-assistant");
            rb.body.appendChild(mkEl("div", "tl-bubble", escapeHTML(String(p.content || ""))));
            appendRow(rb.row);
          }
          break;
        case "tool.call.started": {
          closeBubble();
          rb = rowWithBody(ev, "tl-tool");
          var det = mkEl("details", "tool-call");
          det.setAttribute("data-call-id", String(p.call_id || ""));
          var summary = mkEl("summary", "", "🔧 " + escapeHTML(String(p.tool || "tool")));
          det.appendChild(summary);
          if (p.arguments) {
            det.appendChild(mkEl("pre", "tl-json", escapeHTML(excerpt(String(p.arguments), 2000))));
          }
          rb.body.appendChild(det);
          state.toolCalls[String(p.call_id)] = det;
          appendRow(rb.row);
          break;
        }
        case "tool.call.completed": {
          var callID = String(p.call_id || "");
          var target = state.toolCalls[callID] ||
            container.querySelector('[data-call-id="' + callID + '"]');
          var meta = "completed" + (typeof p.duration_ms === "number" ? " in " + escapeHTML(formatInt(p.duration_ms)) + "ms" : "");
          if (target) {
            if (p.output) {
              target.appendChild(mkEl("pre", "tl-json", escapeHTML(excerpt(String(p.output), 2000))));
            }
            target.appendChild(mkEl("div", "tl-tool-meta muted", escapeHTML(meta)));
            if (target.children.length > 0) {
              target.children[0].innerHTML += " ✓";
            }
          } else {
            rb = rowWithBody(ev, "tl-tool");
            rb.body.innerHTML = "🔧 " + escapeHTML(String(p.tool || "tool")) + " " + escapeHTML(meta);
            appendRow(rb.row);
          }
          if (!state.paused) {
            container.scrollTop = container.scrollHeight;
          }
          break;
        }
        case "run.cost_limit_reached":
          rb = rowWithBody(ev, "tl-warn");
          rb.body.innerHTML = "<strong>Cost limit reached</strong> — cumulative " +
            escapeHTML(formatUSD(p.cumulative_cost_usd)) + " of max " + escapeHTML(formatUSD(p.max_cost_usd));
          appendRow(rb.row);
          break;
        case "assistant.thinking.delta":
        case "tool.call.delta":
        case "tool.output.delta":
        case "usage.delta":
          // Ephemeral streaming signals; the substance lands in the
          // completed/final events.
          return;
        default: {
          rb = rowWithBody(ev, "tl-generic");
          rb.body.innerHTML = "<code>" + escapeHTML(String(ev.type)) + "</code>" +
            '<pre class="tl-json">' + escapeHTML(JSON.stringify(p, null, 2)) + "</pre>";
          appendRow(rb.row);
          break;
        }
      }
    }

    function handleFrame(raw) {
      var id = "";
      var eventType = "";
      var data = "";
      raw.split(/\r?\n/).forEach(function (line) {
        if (line.charAt(0) === ":") {
          return; // SSE comment (keepalive ping)
        }
        if (line.indexOf("id: ") === 0) {
          id = line.slice(4);
        } else if (line.indexOf("event: ") === 0) {
          eventType = line.slice(7);
        } else if (line.indexOf("retry: ") === 0) {
          var ms = parseInt(line.slice(7), 10);
          if (!isNaN(ms) && ms >= 0) {
            state.retryMs = ms;
          }
        } else if (line.indexOf("data: ") === 0) {
          data = data ? data + "\n" + line.slice(6) : line.slice(6);
        }
      });
      if (!data) {
        return;
      }
      var ev;
      try {
        ev = JSON.parse(data);
      } catch (e) {
        return; // malformed frame — skip, keep streaming
      }
      ev.id = ev.id || id;
      ev.type = ev.type || eventType;
      renderEvent(ev);
    }

    function readStream(reader) {
      var decoder = new TextDecoder();
      var buffer = "";
      function pump() {
        return reader.read().then(function (result) {
          if (result.done) {
            return;
          }
          buffer += decoder.decode(result.value, { stream: true });
          var parts = buffer.split(/\r?\n\r?\n/);
          buffer = parts.pop();
          parts.forEach(handleFrame);
          return pump();
        });
      }
      return pump();
    }

    function scheduleReconnect() {
      if (state.aborted || state.terminal) {
        return;
      }
      setStatus("Disconnected — reconnecting…");
      setTimeout(function () {
        if (!state.aborted && !state.terminal) {
          connectStream();
        }
      }, state.retryMs);
    }

    function connectStream() {
      if (state.aborted || state.terminal) {
        return;
      }
      var headers = { Authorization: "Bearer " + currentToken() };
      if (state.lastEventID) {
        headers["Last-Event-ID"] = state.lastEventID;
      }
      var opts = { headers: headers };
      if (state.controller) {
        opts.signal = state.controller.signal;
      }
      fetch("/v1/runs/" + encodeURIComponent(runID) + "/events", opts)
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
          if (res.status === 404) {
            setStatus("Event stream unavailable — the daemon no longer holds this run in memory (historical run).");
            return null;
          }
          if (!res.ok) {
            throw new Error("events stream: HTTP " + res.status);
          }
          setStatus("Live — replaying history, then following new events.");
          return readStream(res.body.getReader());
        })
        .then(function (done) {
          if (done === null || state.aborted || state.terminal) {
            return;
          }
          // Clean EOF before a terminal event: the server dropped us.
          scheduleReconnect();
        })
        .catch(function (err) {
          if (state.aborted || state.terminal || (err && err.name === "AbortError")) {
            return;
          }
          scheduleReconnect();
        });
    }

    connectStream();

    return {
      abort: function () {
        state.aborted = true;
        if (state.controller) {
          state.controller.abort();
        }
      }
    };
  }

  // ---- bootstrap ----

  function connect() {
    var token = currentToken();
    if (!token) {
      showTokenForm("");
      return;
    }
    setStatus("idle", "connecting…");
    apiFetch("/v1/runs?limit=1")
      .then(function (body) {
        if (body === null) {
          return; // auth handling took over
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
