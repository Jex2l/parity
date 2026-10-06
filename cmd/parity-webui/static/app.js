// Parity report viewer — vanilla JS, no framework, no CDN dependency
// (this has to run fully offline on a local machine). State lives in
// `state`; every render* function is a pure function of it, so any
// interaction just mutates state and re-renders.
(() => {
  "use strict";

  const state = {
    report: null,
    view: "signal", // "signal" | "absorbed"
    selectedClusterIndex: null,
    selectedExampleIndex: 0,
    search: "",
    activeSeverities: new Set(["critical", "high", "medium", "low"]),
    sort: "severity",
    noiseSearch: "",
    activeNoiseClasses: null, // populated once we know what classes exist
    tab: "clusters",
    autoRefresh: false,
  };

  const SEVERITY_RANK = { critical: 3, high: 2, medium: 1, low: 0 };

  // ---------- data loading ----------

  async function loadRuns() {
    const res = await fetch("/api/runs");
    const runs = await res.json();
    const sel = document.getElementById("run-switcher");
    sel.innerHTML = "";
    for (const run of runs || []) {
      const opt = document.createElement("option");
      opt.value = run.file;
      opt.textContent = run.file;
      if (run.current) opt.selected = true;
      sel.appendChild(opt);
    }
  }

  async function loadReport(file) {
    const url = file ? `/api/report?file=${encodeURIComponent(file)}` : "/api/report";
    const res = await fetch(url);
    if (!res.ok) {
      document.getElementById("summary-bar").innerHTML =
        `<div class="chip warn"><span class="label">Error</span><span class="value" style="font-size:13px">${await res.text()}</span></div>`;
      return;
    }
    state.report = await res.json();
    state.selectedClusterIndex = null;
    state.selectedExampleIndex = 0;
    state.activeNoiseClasses = null;
    renderAll();
  }

  // ---------- rendering: summary ----------

  function renderSummary() {
    const r = state.report;
    const bar = document.getElementById("summary-bar");
    if (!r) { bar.innerHTML = ""; return; }
    const fidelityPct = (r.fidelity.score * 100).toFixed(0);
    const fidelityClass = r.fidelity.low_confidence ? "warn" : "ok";
    bar.innerHTML = `
      <div class="chip"><span class="label">Replayed</span><span class="value">${r.total_replayed}</span></div>
      <div class="chip ${fidelityClass}"><span class="label">Fidelity</span><span class="value">${fidelityPct}%</span></div>
      <div class="chip"><span class="label">Clusters</span><span class="value">${(r.clusters || []).length}</span></div>
      <div class="chip"><span class="label">Signal changes</span><span class="value">${r.signal_count}</span></div>
      <div class="chip"><span class="label">Absorbed (noise)</span><span class="value">${r.absorbed_count}</span></div>
      <div class="chip"><span class="label">Perf regressions</span><span class="value">${(r.perf_regressions || []).length}</span></div>
    `;
    let reasonEl = document.querySelector(".fidelity-reason");
    if (r.fidelity.low_confidence && r.fidelity.reason) {
      if (!reasonEl) {
        reasonEl = document.createElement("div");
        reasonEl.className = "fidelity-reason";
        bar.insertAdjacentElement("afterend", reasonEl);
      }
      reasonEl.textContent = `⚠ Low confidence: ${r.fidelity.reason}`;
    } else if (reasonEl) {
      reasonEl.remove();
    }
  }

  // ---------- rendering: cluster sidebar ----------

  function currentClusters() {
    const r = state.report;
    if (!r) return [];
    return state.view === "signal" ? (r.clusters || []) : (r.absorbed_clusters || []);
  }

  function filteredSortedClusters() {
    let list = currentClusters().map((c, i) => ({ c, i }));
    const q = state.search.trim().toLowerCase();
    if (q) {
      list = list.filter(({ c }) =>
        c.endpoint.toLowerCase().includes(q) ||
        (c.changed_paths || []).some(p => p.toLowerCase().includes(q))
      );
    }
    if (state.view === "signal") {
      list = list.filter(({ c }) => state.activeSeverities.has(c.severity));
    }
    const sorters = {
      severity: (a, b) => (SEVERITY_RANK[b.c.severity] - SEVERITY_RANK[a.c.severity]) || (b.c.weighted_count - a.c.weighted_count),
      blast: (a, b) => b.c.weighted_count - a.c.weighted_count,
      endpoint: (a, b) => a.c.endpoint.localeCompare(b.c.endpoint),
    };
    list.sort(sorters[state.sort] || sorters.severity);
    return list;
  }

  function renderSeverityFilters() {
    const el = document.getElementById("severity-filters");
    el.innerHTML = "";
    if (state.view !== "signal") { el.style.display = "none"; return; }
    el.style.display = "flex";
    for (const sev of ["critical", "high", "medium", "low"]) {
      const chip = document.createElement("span");
      chip.className = "sev-filter-chip" + (state.activeSeverities.has(sev) ? " active" : "");
      chip.dataset.sev = sev;
      chip.textContent = sev;
      chip.addEventListener("click", () => {
        if (state.activeSeverities.has(sev)) state.activeSeverities.delete(sev);
        else state.activeSeverities.add(sev);
        renderSeverityFilters();
        renderClusterList();
      });
      el.appendChild(chip);
    }
  }

  function renderClusterList() {
    const listEl = document.getElementById("cluster-list");
    const items = filteredSortedClusters();
    if (items.length === 0) {
      listEl.innerHTML = `<li class="empty-state" style="padding:14px;list-style:none">No ${state.view === "signal" ? "clusters" : "absorbed changes"} match the current filters.</li>`;
      return;
    }
    listEl.innerHTML = "";
    for (const { c, i } of items) {
      const li = document.createElement("li");
      li.className = "cluster-row" + (state.selectedClusterIndex === i ? " selected" : "");
      li.dataset.index = i;
      const badge = state.view === "signal"
        ? `<span class="badge badge-${c.severity}">${c.severity}</span>`
        : `<span class="badge badge-low">noise</span>`;
      li.innerHTML = `
        <div class="row-top">
          <span class="endpoint">${escapeHTML(c.endpoint)}</span>
          ${badge}
        </div>
        <div class="row-top">
          <span class="types">${escapeHTML((c.change_types || []).join(", "))}</span>
          <span class="count">${c.count} (${c.weighted_count.toFixed(1)} wtd)</span>
        </div>
      `;
      li.addEventListener("click", () => selectCluster(i));
      listEl.appendChild(li);
    }
  }

  function selectCluster(index) {
    state.selectedClusterIndex = index;
    state.selectedExampleIndex = 0;
    setHash();
    renderClusterList();
    renderClusterDetail();
  }

  // ---------- rendering: cluster detail + example browser ----------

  function renderClusterDetail() {
    const el = document.getElementById("cluster-detail");
    const clusters = currentClusters();
    const c = clusters[state.selectedClusterIndex];
    if (!c) {
      el.innerHTML = `<p class="empty-state">Select a cluster to inspect it.</p>`;
      return;
    }
    const badge = state.view === "signal" ? `<span class="badge badge-${c.severity}">${c.severity}</span>` : `<span class="badge badge-low">noise</span>`;
    el.innerHTML = `
      <div class="detail-header">
        <div>
          <h2>${escapeHTML(c.endpoint)}</h2>
          <div class="chip-row">
            ${(c.changed_paths || []).map(p => `<span class="path-chip">${escapeHTML(p)}</span>`).join("")}
          </div>
          <div class="chip-row">
            ${(c.change_types || []).map(t => `<span class="type-chip">${escapeHTML(t)}</span>`).join("")}
          </div>
        </div>
        <div>
          ${badge}
          <div style="margin-top:6px;font-size:12px;color:var(--text-muted)">${c.count} occurrence(s), ${c.weighted_count.toFixed(1)} weighted</div>
        </div>
      </div>
      <div id="example-tabs" class="example-tabs"></div>
      <div id="example-body"></div>
    `;
    renderExampleTabs(c);
    renderExample(c);
  }

  function renderExampleTabs(c) {
    const el = document.getElementById("example-tabs");
    const examples = c.examples && c.examples.length ? c.examples : (c.example_exchange_ids || []).map(id => ({ exchange_id: id }));
    if (examples.length === 0) { el.innerHTML = ""; return; }
    el.innerHTML = "";
    examples.forEach((ex, idx) => {
      const btn = document.createElement("button");
      btn.className = "example-tab" + (idx === state.selectedExampleIndex ? " active" : "");
      btn.textContent = `#${idx + 1}`;
      btn.title = ex.exchange_id;
      btn.addEventListener("click", () => {
        state.selectedExampleIndex = idx;
        setHash();
        renderExampleTabs(c);
        renderExample(c);
      });
      el.appendChild(btn);
    });
  }

  function renderExample(c) {
    const body = document.getElementById("example-body");
    const examples = c.examples || [];
    const ex = examples[state.selectedExampleIndex];
    if (!ex) {
      body.innerHTML = `<p class="empty-state">No example bodies were captured for this cluster (older report format, or examples exceeded the cap).</p>`;
      return;
    }

    const changedPaths = new Set((ex.changes || []).map(ch => ch.path));
    const beforeParsed = tryParseJSON(ex.before_body);
    const afterParsed = tryParseJSON(ex.after_body);

    body.innerHTML = `
      <div class="example-meta">
        <span>${escapeHTML(ex.method || "")} ${escapeHTML(ex.path || "")}</span>
        <span>·</span>
        <span>${escapeHTML(ex.exchange_id || "")}</span>
        <button class="copy-btn" data-copy="id">copy ID</button>
      </div>
      <div class="diff-grid">
        <div class="diff-pane before">
          <h3>Before (base) <button class="copy-btn" data-copy="before">copy</button></h3>
          <pre id="before-pane"></pre>
        </div>
        <div class="diff-pane after">
          <h3>After (candidate) <button class="copy-btn" data-copy="after">copy</button></h3>
          <pre id="after-pane"></pre>
        </div>
      </div>
      <table class="data-table" id="changes-table">
        <thead><tr><th>Path</th><th>Type</th><th>Before</th><th>After</th></tr></thead>
        <tbody></tbody>
      </table>
    `;

    document.getElementById("before-pane").innerHTML = beforeParsed !== undefined
      ? renderJSONValue(beforeParsed, "$", changedPaths, "before")
      : escapeHTML(ex.before_body || "(empty)");
    document.getElementById("after-pane").innerHTML = afterParsed !== undefined
      ? renderJSONValue(afterParsed, "$", changedPaths, "after")
      : escapeHTML(ex.after_body || "(empty)");

    const tbody = document.querySelector("#changes-table tbody");
    for (const ch of ex.changes || []) {
      const tr = document.createElement("tr");
      tr.innerHTML = `
        <td class="change-path">${escapeHTML(ch.path)}</td>
        <td class="change-type">${escapeHTML(ch.type)}</td>
        <td>${escapeHTML(formatScalar(ch.before))}</td>
        <td>${escapeHTML(formatScalar(ch.after))}</td>
      `;
      tbody.appendChild(tr);
    }

    body.querySelectorAll(".copy-btn").forEach(btn => {
      btn.addEventListener("click", () => {
        const kind = btn.dataset.copy;
        const text = kind === "id" ? ex.exchange_id : kind === "before" ? ex.before_body : ex.after_body;
        copyToClipboard(text || "", btn);
      });
    });
  }

  function formatScalar(v) {
    if (v === undefined || v === null) return "null";
    if (typeof v === "object") return JSON.stringify(v);
    return String(v);
  }

  function copyToClipboard(text, btn) {
    const done = () => { const orig = btn.textContent; btn.textContent = "copied!"; setTimeout(() => btn.textContent = orig, 1000); };
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text).then(done).catch(done);
    } else {
      done();
    }
  }

  // ---------- JSON pretty-printer with path-aware highlighting ----------
  // Highlighting is a best-effort visual aid: it re-derives a JSON path
  // for each node using the same "$.field" / "[N]" / "[id=val]"
  // convention internal/diff uses, including its keyed-array heuristic
  // (an array of objects sharing an "id" field diffs by that id). If a
  // path doesn't line up exactly, the node just won't highlight — the
  // Changes table above is always the authoritative source, so nothing
  // is lost, only the visual callout.

  function renderJSONValue(v, path, changedPaths, side) {
    if (v === null) return span("json-null", "null", path, changedPaths, side);
    if (typeof v === "boolean") return span("json-bool", String(v), path, changedPaths, side);
    if (typeof v === "number") return span("json-num", String(v), path, changedPaths, side);
    if (typeof v === "string") return span("json-str", JSON.stringify(v), path, changedPaths, side);
    if (Array.isArray(v)) return renderJSONArray(v, path, changedPaths, side);
    if (typeof v === "object") return renderJSONObject(v, path, changedPaths, side);
    return escapeHTML(String(v));
  }

  function renderJSONObject(obj, path, changedPaths, side) {
    const keys = Object.keys(obj);
    if (keys.length === 0) return "{}";
    const lines = keys.map(k => {
      const childPath = `${path}.${k}`;
      const valueHTML = renderJSONValue(obj[k], childPath, changedPaths, side);
      return `  <span class="json-key">"${escapeHTML(k)}"</span>: ${valueHTML}`;
    });
    return "{\n" + lines.join(",\n") + "\n}";
  }

  function renderJSONArray(arr, path, changedPaths, side) {
    if (arr.length === 0) return "[]";
    const keyField = keyedArrayField(arr);
    const lines = arr.map((el, i) => {
      const childPath = keyField ? `${path}[${keyField}=${el[keyField]}]` : `${path}[${i}]`;
      return "  " + renderJSONValue(el, childPath, changedPaths, side);
    });
    return "[\n" + lines.join(",\n") + "\n]";
  }

  function keyedArrayField(arr) {
    for (const candidate of ["id", "ID", "Id"]) {
      if (arr.every(el => el && typeof el === "object" && !Array.isArray(el) && candidate in el)) {
        return candidate;
      }
    }
    return null;
  }

  function span(cls, text, path, changedPaths, side) {
    const highlight = changedPaths.has(path) ? ` diff-changed-${side}` : "";
    return `<span class="${cls}${highlight}" title="${escapeHTML(path)}">${escapeHTML(text)}</span>`;
  }

  function tryParseJSON(s) {
    if (!s) return undefined;
    try { return JSON.parse(s); } catch { return undefined; }
  }

  // ---------- rendering: noise model tab ----------

  function renderNoiseTab() {
    const r = state.report;
    const entries = (r && r.noise_model) || [];
    const table = document.getElementById("noise-table");
    const empty = document.getElementById("noise-empty");
    if (entries.length === 0) {
      table.classList.add("hidden");
      empty.classList.remove("hidden");
      document.getElementById("noise-class-filters").innerHTML = "";
      return;
    }
    table.classList.remove("hidden");
    empty.classList.add("hidden");

    if (!state.activeNoiseClasses) {
      state.activeNoiseClasses = new Set(entries.map(e => e.class));
    }
    const classesEl = document.getElementById("noise-class-filters");
    const allClasses = [...new Set(entries.map(e => e.class))].sort();
    classesEl.innerHTML = "";
    for (const cls of allClasses) {
      const chip = document.createElement("span");
      chip.className = "class-chip" + (state.activeNoiseClasses.has(cls) ? " active" : "");
      chip.textContent = cls;
      chip.addEventListener("click", () => {
        if (state.activeNoiseClasses.has(cls)) state.activeNoiseClasses.delete(cls);
        else state.activeNoiseClasses.add(cls);
        renderNoiseTab();
      });
      classesEl.appendChild(chip);
    }

    const q = state.noiseSearch.trim().toLowerCase();
    const rows = entries.filter(e =>
      state.activeNoiseClasses.has(e.class) &&
      (!q || e.endpoint.toLowerCase().includes(q) || e.path.toLowerCase().includes(q))
    ).sort((a, b) => a.endpoint.localeCompare(b.endpoint) || a.path.localeCompare(b.path));

    const tbody = table.querySelector("tbody");
    tbody.innerHTML = "";
    for (const e of rows) {
      const tr = document.createElement("tr");
      tr.innerHTML = `<td class="mono">${escapeHTML(e.endpoint)}</td><td class="mono">${escapeHTML(e.path)}</td><td><span class="class-chip active" style="cursor:default">${escapeHTML(e.class)}</span></td>`;
      tbody.appendChild(tr);
    }
  }

  // ---------- rendering: performance tab ----------

  function renderPerfTab() {
    const r = state.report;
    const regs = (r && r.perf_regressions) || [];
    const table = document.getElementById("perf-table");
    const empty = document.getElementById("perf-empty");
    if (regs.length === 0) {
      table.classList.add("hidden");
      empty.classList.remove("hidden");
      return;
    }
    table.classList.remove("hidden");
    empty.classList.add("hidden");

    const sorted = [...regs].sort((a, b) => b.effect_pct - a.effect_pct);
    const maxCandidate = Math.max(...sorted.map(r => r.candidate_p95_ms), 1);
    const tbody = table.querySelector("tbody");
    tbody.innerHTML = "";
    for (const reg of sorted) {
      const pct = Math.min(100, (reg.candidate_p95_ms / maxCandidate) * 100);
      const tr = document.createElement("tr");
      tr.innerHTML = `
        <td class="mono">${escapeHTML(reg.endpoint)}</td>
        <td>${reg.base_p95_ms.toFixed(1)}ms</td>
        <td>${reg.candidate_p95_ms.toFixed(1)}ms</td>
        <td>+${(reg.effect_pct * 100).toFixed(0)}%</td>
        <td>${reg.p_value.toExponential(2)}</td>
        <td><div class="perf-bar-track"><div class="perf-bar-fill" style="width:${pct}%"></div></div></td>
      `;
      tbody.appendChild(tr);
    }
  }

  // ---------- tabs, hash routing ----------

  function setHash() {
    const parts = [state.tab];
    if (state.tab === "clusters" && state.selectedClusterIndex !== null) {
      parts.push(state.view, String(state.selectedClusterIndex), String(state.selectedExampleIndex));
    }
    history.replaceState(null, "", "#" + parts.join("/"));
  }

  function applyHash() {
    const parts = location.hash.replace(/^#/, "").split("/").filter(Boolean);
    if (parts.length === 0) return;
    state.tab = parts[0];
    if (parts[0] === "clusters" && parts.length >= 4) {
      state.view = parts[1];
      state.selectedClusterIndex = Number(parts[2]);
      state.selectedExampleIndex = Number(parts[3]);
    }
  }

  function showTab(tab) {
    state.tab = tab;
    document.querySelectorAll(".tab-btn").forEach(b => b.classList.toggle("active", b.dataset.tab === tab));
    document.querySelectorAll(".tab-panel").forEach(p => p.classList.toggle("hidden", p.id !== `tab-${tab}`));
    setHash();
  }

  // ---------- top-level render ----------

  function renderAll() {
    renderSummary();
    renderSeverityFilters();
    renderClusterList();
    renderClusterDetail();
    renderNoiseTab();
    renderPerfTab();
  }

  function escapeHTML(s) {
    return String(s).replace(/[&<>"']/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
  }

  // ---------- wiring ----------

  function init() {
    applyHash();

    document.querySelectorAll(".tab-btn").forEach(btn => {
      btn.addEventListener("click", () => showTab(btn.dataset.tab));
    });
    showTab(state.tab);

    document.querySelectorAll(".toggle-btn").forEach(btn => {
      btn.addEventListener("click", () => {
        state.view = btn.dataset.view;
        state.selectedClusterIndex = null;
        document.querySelectorAll(".toggle-btn").forEach(b => b.classList.toggle("active", b === btn));
        renderSeverityFilters();
        renderClusterList();
        renderClusterDetail();
      });
    });

    document.getElementById("cluster-search").addEventListener("input", e => {
      state.search = e.target.value;
      renderClusterList();
    });
    document.getElementById("sort-select").addEventListener("change", e => {
      state.sort = e.target.value;
      renderClusterList();
    });
    document.getElementById("noise-search").addEventListener("input", e => {
      state.noiseSearch = e.target.value;
      renderNoiseTab();
    });

    document.getElementById("refresh-btn").addEventListener("click", () => {
      loadReport(document.getElementById("run-switcher").value);
    });
    document.getElementById("run-switcher").addEventListener("change", e => {
      loadReport(e.target.value);
    });

    const autoToggle = document.getElementById("auto-refresh-toggle");
    let autoTimer = null;
    autoToggle.addEventListener("change", () => {
      state.autoRefresh = autoToggle.checked;
      if (autoTimer) clearInterval(autoTimer);
      if (state.autoRefresh) {
        autoTimer = setInterval(() => loadReport(document.getElementById("run-switcher").value), 3000);
      }
    });

    const themeBtn = document.getElementById("theme-toggle");
    const savedTheme = localStorage.getItem("parity-theme") || "auto";
    document.documentElement.setAttribute("data-theme", savedTheme);
    themeBtn.addEventListener("click", () => {
      const current = document.documentElement.getAttribute("data-theme");
      const next = current === "dark" ? "light" : current === "light" ? "auto" : "dark";
      document.documentElement.setAttribute("data-theme", next);
      localStorage.setItem("parity-theme", next);
    });

    document.getElementById("cluster-list").addEventListener("keydown", e => {
      if (e.key !== "ArrowDown" && e.key !== "ArrowUp") return;
      const items = filteredSortedClusters();
      if (items.length === 0) return;
      const curPos = items.findIndex(({ i }) => i === state.selectedClusterIndex);
      let nextPos = e.key === "ArrowDown" ? curPos + 1 : curPos - 1;
      nextPos = Math.max(0, Math.min(items.length - 1, nextPos));
      selectCluster(items[nextPos].i);
      e.preventDefault();
    });

    window.addEventListener("hashchange", () => {
      applyHash();
      showTab(state.tab);
      renderClusterList();
      renderClusterDetail();
    });

    loadRuns().then(() => {
      const sel = document.getElementById("run-switcher");
      loadReport(sel.value || undefined);
    });
  }

  document.addEventListener("DOMContentLoaded", init);
})();
