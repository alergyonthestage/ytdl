"use strict";
const $ = (id) => document.getElementById(id);
const FORMATS = ["mp3", "flac", "m4a", "opus", "wav"];
const NOTIFY_ON = { both: "sempre", success: "solo successo", failure: "solo errore" };
// Mirrors config.DefaultConcurrency: the fallback when the field is left blank.
const DEFAULT_CONCURRENCY = 3;
// How many rows the Download view's "ultimi download" shows. The server sends
// exactly this many on /api/state; the constant is here for the empty state.
const RECENT_LIMIT = 3;

// Live state: the last queue frame plus per-job progress, keyed by spool id.
let counts = { pending: 0, running: 0, done: 0, failed: 0 };
let running = [], pending = [];
const progress = new Map();

// ---- DOM helpers ---------------------------------------------------------
// Everything user-controlled (titles, URLs, failure reasons, launcher errors)
// goes in through textContent, never through innerHTML. The CSP is the backstop
// for a mistake here; not making the mistake is the actual defence.

function el(tag, className, text) {
  const n = document.createElement(tag);
  if (className) n.className = className;
  if (text != null) n.textContent = text;
  return n;
}

function button(label, className, onClick) {
  const b = el("button", className, label);
  b.type = "button";
  b.addEventListener("click", onClick);
  return b;
}

// setMsg replaces a status line. kind is "ok" | "bad" | "" (neutral).
function setMsg(id, kind, text) {
  const box = $(id);
  box.textContent = "";
  if (!text) return;
  const mark = kind === "ok" ? "✓ " : kind === "bad" ? "✗ " : "";
  box.appendChild(el("span", kind, mark + text));
}

function replaceChildren(node, children) {
  node.textContent = "";
  for (const c of children) node.appendChild(c);
}

function option(sel, value, label, current) {
  const o = document.createElement("option");
  o.value = value; o.textContent = label;
  if (value === current) o.selected = true;
  sel.appendChild(o);
}

function fillSelect(sel, values, current, labels) {
  sel.textContent = "";
  for (const v of values) option(sel, v, labels ? labels[v] : v, current);
}

function whenText(iso) {
  const d = new Date(iso);
  if (isNaN(d)) return "";
  return d.toLocaleString("it-IT", { dateStyle: "short", timeStyle: "short" });
}

// ---- routing -------------------------------------------------------------
// Client-side hash routing over ONE document that never reloads. This is a
// correctness requirement, not only a UX one: a real navigation would drop the
// SSE connection, and an open SSE connection is the "GUI connected" clause of
// the daemon's exit test (ADR-0008). A daemon could idle-exit in the gap
// between two page loads, mid-download.

const ROUTES = {
  "#/": "download",
  "#/cronologia": "history",
  "#/impostazioni": "settings",
};

function currentView() {
  return ROUTES[window.location.hash] || "download";
}

function showView(name) {
  for (const view of ["download", "history", "settings"]) {
    $("view-" + view).hidden = view !== name;
  }
  for (const a of document.querySelectorAll("nav a")) {
    if (a.dataset.view === name) a.setAttribute("aria-current", "page");
    else a.removeAttribute("aria-current");
  }
  if (name === "history") loadHistory(false);
  if (name === "download") $("url").focus();
}

window.addEventListener("hashchange", () => showView(currentView()));

// ---- the shared row ------------------------------------------------------
// One component behind the queue, "ultimi download" and the history view, so a
// download looks and behaves the same wherever it is shown.

function itemRow(titleText, metaNodes, actionNodes) {
  const row = el("div", "item");
  const main = el("div", "item-main");
  main.appendChild(el("div", "item-title", titleText));
  for (const n of metaNodes) if (n) main.appendChild(n);
  row.appendChild(main);
  if (actionNodes && actionNodes.length) {
    const acts = el("div", "item-actions");
    for (const n of actionNodes) if (n) acts.appendChild(n);
    row.appendChild(acts);
  }
  return row;
}

// ---- queue ---------------------------------------------------------------

function progressLine(p) {
  if (!p) return "avvio…";
  if (p.status === "processing") return "elaborazione…";
  return [
    p.percent >= 0 ? p.percent.toFixed(1) + "%" : null,
    p.speed,
    p.eta ? "ETA " + p.eta : null,
  ].filter(Boolean).join(" · ");
}

function runningRow(job) {
  const p = progress.get(job.id);
  const meta = [];
  const bar = el("div", "bar");
  const fill = el("i");
  // The one value that becomes a style rather than text: forced numeric and
  // clamped, so it can never carry anything but a percentage.
  const pct = Math.max(0, Math.min(100, Number(p && p.percent) || 0));
  fill.style.width = pct + "%";
  bar.appendChild(fill);
  meta.push(bar);
  meta.push(el("div", "meta", progressLine(p) + " · ." + job.format));
  const title = (p && p.title) || job.title || job.url;
  return itemRow(title, meta, [cancelButton(job)]);
}

function pendingRow(job) {
  const detail = "." + job.format + (job.playlist ? " · playlist" : "");
  const meta = [];
  if (job.title) meta.push(el("div", "meta", job.url));
  meta.push(el("div", "meta", detail));
  return itemRow(job.title || job.url, meta, [cancelButton(job)]);
}

function cancelButton(job) {
  return button("Annulla", "secondary small", async (ev) => {
    const b = ev.currentTarget;
    b.disabled = true;
    try {
      const data = await api("/api/queue/cancel", { id: job.id });
      if (data.queue) applyQueue(data.queue);
      setMsg("msg", "ok", "Annullato.");
    } catch (err) {
      b.disabled = false;
      setMsg("msg", "bad", err.message);
    }
  });
}

function renderQueue() {
  const rows = [];
  if (running.length) {
    rows.push(el("h3", null, "In corso (" + running.length + ")"));
    for (const j of running) rows.push(runningRow(j));
  }
  if (pending.length) {
    rows.push(el("h3", null, "In attesa (" + pending.length + ")"));
    for (const j of pending) rows.push(pendingRow(j));
  }
  if (!rows.length) {
    // An empty state teaches the next step rather than only reporting emptiness.
    rows.push(el("p", "empty", "Niente in corso. Incolla un link qui accanto per iniziare."));
  }
  replaceChildren($("queue"), rows);
}

function applyQueue(q) {
  counts = q.counts || counts;
  running = q.running || [];
  pending = q.pending || [];
  // Drop progress for jobs that are no longer running, so the map stays bounded.
  const live = new Set(running.map((j) => j.id));
  for (const id of progress.keys()) if (!live.has(id)) progress.delete(id);
  renderQueue();
}

// ---- history rows --------------------------------------------------------

// primaryAction ranks a row's actions per ux-principles.md §4: exactly ONE
// button, chosen by the row's state. The state comes from the server, which is
// the only side that can tell whether the file is still on disk.
function primaryAction(h) {
  if (h.success && h.canOpenFile) return button("Apri", "small", (ev) => openRecord(ev, h, "file"));
  return button("Riscarica", "secondary small", (ev) => againRecord(ev, h));
}

// overflowItems is everything the row can do EXCEPT its primary action. An
// action that cannot work here is listed disabled with the reason, rather than
// hidden (so the row does not change shape) or live-and-failing.
function overflowItems(h) {
  const items = [];
  if (h.success && h.canOpenFile) {
    items.push(["Mostra nel Finder", null, (ev) => openRecord(ev, h, "folder")]);
    items.push(["Riscarica", null, (ev) => againRecord(ev, h)]);
  } else if (h.success) {
    if (h.canOpenFolder) items.push(["Mostra la cartella", null, (ev) => openRecord(ev, h, "folder")]);
    else items.push(["Apri", "il file non è più al suo posto", null]);
  } else {
    items.push(h.hasLog
      ? ["Vedi errore", null, () => showLog(h)]
      : ["Vedi errore", "nessun log disponibile", null]);
  }
  items.push(h.url
    ? ["Copia link", null, (ev) => copyLink(ev, h)]
    : ["Copia link", "nessun link registrato", null]);
  return items;
}

// overflowMenu builds the ··· control. Only one menu is open at a time; it
// closes on outside click and on Escape, and its items are ordinary buttons so
// they are reachable and operable from the keyboard.
function overflowMenu(h) {
  const wrap = el("div", "overflow");
  const menu = el("div", "menu");
  menu.hidden = true;

  const toggle = button("···", "secondary small overflow-btn", (ev) => {
    ev.stopPropagation();
    const open = menu.hidden;
    closeMenus();
    menu.hidden = !open;
    toggle.setAttribute("aria-expanded", String(open));
    if (open) {
      const first = menu.querySelector("button:not(:disabled)");
      if (first) first.focus();
    }
  });
  toggle.setAttribute("aria-haspopup", "menu");
  toggle.setAttribute("aria-expanded", "false");
  toggle.setAttribute("aria-label", "Altre azioni");

  for (const [label, disabledReason, onClick] of overflowItems(h)) {
    const b = button(label, null, (ev) => {
      closeMenus();
      if (onClick) onClick(ev);
    });
    if (disabledReason) {
      b.disabled = true;
      b.title = disabledReason;
      b.textContent = label + " — " + disabledReason;
    }
    menu.appendChild(b);
  }
  wrap.appendChild(toggle);
  wrap.appendChild(menu);
  return wrap;
}

function closeMenus() {
  for (const m of document.querySelectorAll(".menu")) m.hidden = true;
  for (const b of document.querySelectorAll(".overflow-btn")) b.setAttribute("aria-expanded", "false");
}

document.addEventListener("click", closeMenus);
document.addEventListener("keydown", (ev) => {
  if (ev.key === "Escape") closeMenus();
});

// historyRow renders one record. withOverflow is false for the Download view's
// "ultimi download", which shows the primary action only.
function historyRow(h, withOverflow) {
  const meta = [];
  const bits = [whenText(h.time)];
  if (h.format) bits.push("." + h.format);
  if (h.count > 1) bits.push(h.count + " tracce");
  if (h.success && h.location) bits.push(h.location);
  meta.push(el("div", "meta", bits.filter(Boolean).join(" · ")));
  // Why it failed, inline: "why" must need no click. The full .log is one.
  if (!h.success && h.error) meta.push(el("div", "reason", h.error));

  const mark = h.success ? "✓ " : "✗ ";
  const actions = [primaryAction(h)];
  if (withOverflow) actions.push(overflowMenu(h));
  return itemRow(mark + (h.title || h.url), meta, actions);
}

async function copyLink(ev, h) {
  try {
    if (!navigator.clipboard) throw new Error("appunti non disponibili");
    await navigator.clipboard.writeText(h.url);
    setMsg(msgBox(), "ok", "Link copiato.");
  } catch (err) {
    setMsg(msgBox(), "bad", "Impossibile copiare il link: " + err.message);
  }
}

// showLog fetches the per-job .log and shows it in the panel. The response is
// plain text served with nosniff, and it lands in a <pre> via textContent, so a
// log full of markup is displayed, never interpreted.
async function showLog(h) {
  const panel = $("logPanel");
  $("logTitle").textContent = "Dettaglio: " + (h.title || h.url);
  $("logBody").textContent = "Carico…";
  panel.hidden = false;
  try {
    const r = await fetch("/api/history/log?id=" + encodeURIComponent(h.id));
    const text = await r.text();
    if (!r.ok) throw new Error(text || "errore " + r.status);
    $("logBody").textContent = text;
  } catch (err) {
    $("logBody").textContent = "Log non disponibile: " + err.message;
  }
}

$("logClose").addEventListener("click", () => { $("logPanel").hidden = true; });

// msgBox is the status line of whichever view is showing, so a message from a
// shared row action lands where the user is looking.
function msgBox() {
  return currentView() === "history" ? "historyMsg" : "msg";
}

async function openRecord(ev, h, target) {
  const b = ev.currentTarget;
  b.disabled = true;
  try {
    await api("/api/history/open", { id: h.id, target: target });
  } catch (err) {
    setMsg(msgBox(), "bad", err.message);
  } finally {
    b.disabled = false;
  }
}

async function againRecord(ev, h) {
  const b = ev.currentTarget;
  b.disabled = true;
  try {
    const data = await api("/api/history/again", { id: h.id });
    if (data.queue) applyQueue(data.queue);
    setMsg(msgBox(), "ok", "Rimesso in coda.");
  } catch (err) {
    setMsg(msgBox(), "bad", err.message);
  } finally {
    b.disabled = false;
  }
}

function renderRecent(items) {
  if (!items || !items.length) {
    replaceChildren($("recent"), [el("p", "empty", "Nessun download registrato. Il primo comparirà qui.")]);
    return;
  }
  replaceChildren($("recent"), items.slice(0, RECENT_LIMIT).map((h) => historyRow(h, false)));
}

// ---- the history view ----------------------------------------------------

// The view's query. Filters and search go to the SERVER, so they search the
// whole retention window rather than only the rows already loaded.
const historyQuery = { filter: "all", q: "", offset: 0 };
// Set once the user has paged past the first screen: a live refresh would then
// yank away rows they are reading, so auto-refresh stops until they re-filter.
let historyPaged = false;

function historyURL() {
  const p = new URLSearchParams();
  if (historyQuery.filter === "failed") p.set("failed", "1");
  if (historyQuery.filter === "ok") p.set("ok", "1");
  if (historyQuery.q) p.set("q", historyQuery.q);
  if (historyQuery.offset) p.set("offset", String(historyQuery.offset));
  const qs = p.toString();
  return "/api/history" + (qs ? "?" + qs : "");
}

function historyEmptyText() {
  if (historyQuery.q) return "Nessun download corrisponde a questa ricerca.";
  if (historyQuery.filter === "failed") return "Nessun download non riuscito. Buon segno.";
  if (historyQuery.filter === "ok") return "Nessun download completato in questo periodo.";
  return "Nessun download registrato. Il primo comparirà qui.";
}

// loadHistory replaces the list; loadMoreHistory appends the next page. Paging
// APPENDS so the rows already on screen do not jump.
async function loadHistory(append) {
  const list = $("historyList");
  if (!append) {
    historyQuery.offset = 0;
    historyPaged = false;
    $("logPanel").hidden = true;
  } else {
    historyPaged = true;
  }
  try {
    const r = await fetch(historyURL());
    const page = await r.json();
    if (!r.ok) throw new Error(page.error || "errore " + r.status);
    const rows = (page.items || []).map((h) => historyRow(h, true));
    if (append) {
      for (const row of rows) list.appendChild(row);
    } else if (rows.length) {
      replaceChildren(list, rows);
    } else {
      replaceChildren(list, [el("p", "empty", historyEmptyText())]);
    }
    historyQuery.offset += (page.items || []).length;
    $("loadMore").hidden = !page.more;
  } catch (err) {
    setMsg("historyMsg", "bad", "Impossibile leggere la cronologia: " + err.message);
  }
}

for (const chip of document.querySelectorAll(".chip")) {
  chip.addEventListener("click", () => {
    historyQuery.filter = chip.dataset.filter;
    for (const c of document.querySelectorAll(".chip")) {
      c.setAttribute("aria-pressed", String(c === chip));
    }
    loadHistory(false);
  });
}

// A short debounce: typing a query should not fire a request per keystroke.
let searchTimer = 0;
$("search").addEventListener("input", () => {
  clearTimeout(searchTimer);
  searchTimer = setTimeout(() => {
    historyQuery.q = $("search").value.trim();
    loadHistory(false);
  }, 200);
});

$("loadMore").addEventListener("click", () => loadHistory(true));

// ---- API helper ----------------------------------------------------------

// api POSTs JSON and turns a non-2xx into an Error carrying the server's own
// message, so every call site can just show err.message.
async function api(path, body) {
  const r = await fetch(path, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  let data = {};
  try { data = await r.json(); } catch (e) { /* empty or non-JSON body */ }
  if (!r.ok) throw new Error(data.error || "errore " + r.status);
  return data;
}

// ---- engine status + state ----------------------------------------------

function setDaemon(on) {
  $("daemonDot").className = "dot" + (on ? " on" : "");
  $("daemonTxt").textContent = on ? "motore attivo" : "motore inattivo";
}

function applyState(s) {
  applyQueue(s.queue);
  renderRecent(s.history);
  setDaemon(s.daemonRunning);
  $("sessionOut").value = s.sessionOutputDir || "";
}

async function loadState() {
  const r = await fetch("/api/state");
  applyState(await r.json());
}

function connect() {
  const es = new EventSource("/api/events");
  es.addEventListener("queue", (e) => {
    applyQueue(JSON.parse(e.data));
    // A queue change usually means a job finished: refresh the rest too.
    fetch("/api/state").then((r) => r.json()).then((s) => {
      renderRecent(s.history);
      setDaemon(s.daemonRunning);
      // Only refresh the history view while the user is on the first page:
      // reloading under someone who has clicked "Carica altri" would yank away
      // the rows they are reading.
      if (currentView() === "history" && !historyPaged) loadHistory(false);
    }).catch(() => {});
  });
  es.addEventListener("progress", (e) => {
    const p = JSON.parse(e.data);
    progress.set(p.id, p);
    renderQueue();
  });
  es.onerror = () => setDaemon(false);
}

// ---- new download --------------------------------------------------------

$("dl").addEventListener("submit", async (ev) => {
  ev.preventDefault();
  const body = {
    url: $("url").value.trim(),
    format: $("format").value || null,
    outputDir: $("outDir").value.trim() || null,
    playlist: $("playlist").checked,
  };
  if (!body.url) return;
  $("go").disabled = true;
  setMsg("msg", "", "");
  try {
    const data = await api("/api/downloads", body);
    setMsg("msg", "ok", "Accodato.");
    $("url").value = "";
    $("url").focus();
    if (data.queue) applyQueue(data.queue);
  } catch (err) {
    setMsg("msg", "bad", err.message);
  } finally {
    $("go").disabled = false;
  }
});

// ---- settings ------------------------------------------------------------

$("saveSession").addEventListener("click", async () => {
  const dir = $("sessionOut").value.trim();
  try {
    await fetch("/api/session", {
      method: "PUT", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ outputDir: dir }),
    });
    setMsg("sessionMsg", "ok", "Cartella di sessione aggiornata.");
  } catch (err) {
    setMsg("sessionMsg", "bad", err.message);
  }
});

const SETTING_IDS = ["outputDir", "format", "audioQuality", "playlistDefault", "nameTemplate",
  "stripBrackets", "stripTags", "embedThumbnail", "embedMetadata", "logDir", "logRetentionDays",
  "breadcrumbOnFailure", "notify", "notifyOn", "notifyForeground", "notifySound", "concurrency",
  "jobTimeout", "openFolderOnDone"];

function fillSettings(s) {
  // concurrency 0 means "no cap"; show it as an explicit choice rather than as a
  // number, so an empty box can never silently mean unlimited.
  $("s_noLimit").checked = s.concurrency === 0;
  $("s_concurrency").value = s.concurrency === 0 ? "" : s.concurrency;
  $("s_concurrency").disabled = s.concurrency === 0;
  fillSelect($("s_format"), FORMATS, s.format);
  fillSelect($("s_audioQuality"), ["0", "1", "2", "3", "4", "5", "6", "7", "8", "9"], s.audioQuality);
  fillSelect($("s_notifyOn"), Object.keys(NOTIFY_ON), s.notifyOn, NOTIFY_ON);
  for (const k of SETTING_IDS) {
    const e = $("s_" + k);
    if (!e) continue;
    if (e.type === "checkbox") e.checked = !!s[k];
    else e.value = s[k];
  }
}

function readSettings() {
  const out = {};
  for (const k of SETTING_IDS) {
    if (k === "concurrency") continue; // handled explicitly below
    const e = $("s_" + k);
    if (!e) continue;
    if (e.type === "checkbox") out[k] = e.checked;
    else if (e.type === "number") out[k] = parseInt(e.value, 10) || 0;
    else out[k] = e.value;
  }
  // 0 = unlimited, and only when the user ticked the box on purpose. An empty or
  // unparseable field keeps the recommended default instead.
  const n = parseInt($("s_concurrency").value, 10);
  out.concurrency = $("s_noLimit").checked ? 0 : (n >= 1 ? n : DEFAULT_CONCURRENCY);
  return out;
}

$("s_noLimit").addEventListener("change", () => {
  $("s_concurrency").disabled = $("s_noLimit").checked;
  if (!$("s_noLimit").checked && !$("s_concurrency").value) {
    $("s_concurrency").value = DEFAULT_CONCURRENCY;
  }
  refreshSaveBar();
});

// ---- unsaved-changes bar -------------------------------------------------
// The settings form is long enough that a Save button at the bottom is easy to
// miss and easy to leave un-pressed. A sticky bar appears the moment anything
// differs from what is persisted, and offers the way back as well as forward.
//
// The API is a whole-document PUT, so there is one bar for the whole form
// rather than one per group: per-group saves would imply a guarantee the
// transport does not make.

let savedSettings = null; // the last state known to be persisted

function settingsDirty() {
  if (!savedSettings) return false;
  return JSON.stringify(readSettings()) !== JSON.stringify(savedSettings);
}

function refreshSaveBar() {
  $("saveBar").hidden = !settingsDirty();
}

$("settings").addEventListener("input", refreshSaveBar);
$("settings").addEventListener("change", refreshSaveBar);

$("revertSettings").addEventListener("click", () => {
  if (savedSettings) fillSettings(savedSettings);
  refreshSaveBar();
  setMsg("settingsMsg", "", "");
});

// applySettings fills the form and records the state as persisted, so the
// unsaved-changes bar is measured against what is really on disk.
function applySettings(s) {
  savedSettings = s;
  fillSettings(s);
  refreshSaveBar();
}

async function loadSettings() {
  const r = await fetch("/api/settings");
  const s = await r.json();
  applySettings(s);
  fillSelect($("format"), FORMATS, s.format);
  $("playlist").checked = !!s.playlistDefault;
}

$("settings").addEventListener("submit", async (ev) => {
  ev.preventDefault();
  $("saveSettings").disabled = true;
  try {
    const r = await fetch("/api/settings", {
      method: "PUT", headers: { "Content-Type": "application/json" },
      body: JSON.stringify(readSettings()),
    });
    const data = await r.json();
    if (!r.ok) throw new Error(data.error || "errore");
    // The response is the RESOLVED settings, which may differ from what was
    // sent (a trailing slash trimmed, say) — so the saved snapshot comes from
    // the server, not from the form.
    applySettings(data);
    fillSelect($("format"), FORMATS, data.format);
    setMsg("settingsMsg", "ok", "Impostazioni salvate.");
  } catch (err) {
    setMsg("settingsMsg", "bad", err.message);
  } finally {
    $("saveSettings").disabled = false;
  }
});

// ADR-0008: closing the GUI while the queue still has work would leave the
// daemon draining unattended — warn, as the ADR prescribes.
window.addEventListener("beforeunload", (e) => {
  if (counts.pending + counts.running > 0) {
    e.preventDefault();
    e.returnValue = "Vuoi uscire? Hai download in coda.";
    return e.returnValue;
  }
});

showView(currentView());
loadSettings().then(loadState).then(connect).catch(() => {
  setMsg("msg", "bad", "Motore non raggiungibile.");
});
