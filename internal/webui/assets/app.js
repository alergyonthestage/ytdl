"use strict";
const $ = (id) => document.getElementById(id);
const FORMATS = ["mp3", "flac", "m4a", "opus", "wav"];
const NOTIFY_ON = { both: "sempre", success: "solo successo", failure: "solo errore" };
// Mirrors config.MaxAudioQuality: yt-dlp's VBR scale is 0 (best) to 10 (worst).
// The list stopped at 9 while the server accepted 10, so a config file set to 10
// lost its value the first time the settings form was saved (G7).
const AUDIO_QUALITIES = Array.from({ length: 11 }, (_, i) => String(i));
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

// hashForView is currentView backwards: ROUTES is keyed by hash, so a caller that
// wants to NAVIGATE to a view has to look it up the other way. Writing the hash
// literally at the call site would be a second source of truth for something
// ROUTES and index.html already agree on — and `ROUTES.settings`, which reads
// like it should work, is undefined.
function hashForView(name) {
  for (const hash of Object.keys(ROUTES)) {
    if (ROUTES[hash] === name) return hash;
  }
  return "#/";
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

// liveRows maps a running job's id to the nodes a progress frame updates, so a
// frame does NOT rebuild the queue. Rebuilding replaced the "Annulla" button
// five times a second: a keyboard user could never keep focus on it long enough
// to press it, and a mouse click whose mousedown and mouseup land on different
// nodes fires no click event at all.
const liveRows = new Map();

// destinationLine states where a queued job will land. The dir was resolved AT
// ENQUEUE and frozen in the job's settings, so this is what THIS job will do,
// not what the settings would do now. The queue used to be the only list that
// never said where a file was going (G1). It is its own node rather than part
// of the progress line, which a frame rewrites five times a second.
function destinationLine(job) {
  if (!job.location) return null;
  return el("div", "meta", "cartella: " + job.location);
}

function runningRow(job) {
  const meta = [];
  const bar = el("div", "bar");
  const fill = el("i");
  bar.appendChild(fill);
  meta.push(bar);
  const metaLine = el("div", "meta", "");
  meta.push(metaLine);
  const dest = destinationLine(job);
  if (dest) meta.push(dest);

  const titleEl = document.createTextNode("");
  const row = itemRow("", meta, [cancelButton(job)]);
  const titleBox = row.querySelector(".item-title");
  titleBox.textContent = "";
  titleBox.appendChild(titleEl);

  liveRows.set(job.id, { fill, metaLine, titleEl, job });
  paintProgress(job.id);
  return row;
}

// paintProgress writes one job's live numbers into the nodes already on screen.
function paintProgress(id) {
  const row = liveRows.get(id);
  if (!row) return;
  const p = progress.get(id);
  // The one value that becomes a style rather than text: forced numeric and
  // clamped, so it can never carry anything but a percentage.
  const pct = Math.max(0, Math.min(100, Number(p && p.percent) || 0));
  row.fill.style.width = pct + "%";
  row.metaLine.textContent = progressLine(p) + " · ." + row.job.format;
  row.titleEl.textContent = (p && p.title) || row.job.title || row.job.url;
}

function pendingRow(job) {
  const detail = "." + job.format + (job.playlist ? " · playlist" : "");
  const meta = [];
  if (job.title) meta.push(el("div", "meta", job.url));
  meta.push(el("div", "meta", detail));
  // Pushed only when there is one: a row must not depend on itemRow filtering
  // nulls out for it.
  const dest = destinationLine(job);
  if (dest) meta.push(dest);
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

// queueShape is what actually requires a rebuild: which jobs are in the list and
// in what order. Progress numbers never change it.
function queueShape() {
  return running.map((j) => "r" + j.id).concat(pending.map((j) => "p" + j.id)).join("|");
}
let lastQueueShape = null;

function applyQueue(q) {
  counts = q.counts || counts;
  running = q.running || [];
  pending = q.pending || [];
  // Drop progress for jobs that are no longer running, so the map stays bounded.
  const live = new Set(running.map((j) => j.id));
  for (const id of progress.keys()) if (!live.has(id)) progress.delete(id);

  // Rebuild only when the SET of jobs changed; otherwise repaint in place, so a
  // control the user is aiming at (or has focused) survives.
  const shape = queueShape();
  if (shape !== lastQueueShape) {
    lastQueueShape = shape;
    liveRows.clear();
    renderQueue();
    return;
  }
  for (const id of liveRows.keys()) paintProgress(id);
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
    items.push(["Mostra nella cartella", null, (ev) => openRecord(ev, h, "folder")]);
    items.push(["Riscarica", null, (ev) => againRecord(ev, h)]);
  } else if (h.success) {
    if (h.canOpenFolder) items.push(["Apri la cartella", null, (ev) => openRecord(ev, h, "folder")]);
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
    closeMenus(false);
    menu.hidden = !open;
    toggle.setAttribute("aria-expanded", String(open));
    openMenuToggle = open ? toggle : null;
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
      closeMenus(false);
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

// openMenuToggle remembers which control opened the menu, so Escape can hand
// focus back instead of dropping the user at the top of the document.
let openMenuToggle = null;

function closeMenus(restoreFocus) {
  for (const m of document.querySelectorAll(".menu")) m.hidden = true;
  for (const b of document.querySelectorAll(".overflow-btn")) b.setAttribute("aria-expanded", "false");
  const toggle = openMenuToggle;
  openMenuToggle = null;
  if (restoreFocus && toggle && document.contains(toggle)) toggle.focus();
}

document.addEventListener("click", () => closeMenus(false));
document.addEventListener("keydown", (ev) => {
  if (ev.key === "Escape") closeMenus(true);
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
  // And what to do about it: a message that only states what went wrong is
  // incomplete (ux-principles.md §5). The server derives it from the same
  // stored line, so both channels say the same thing (G8).
  if (!h.success && h.hint) meta.push(el("div", "hint", h.hint));

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

// revealPanel brings a panel that has just been shown into view. The log panel
// sits ABOVE the list, so opening it from a row far down revealed it
// off-screen and "Vedi errore" appeared to do nothing (G6). Focus follows, or
// the same thing happens to a keyboard user with the scrolling fixed.
function revealPanel(panel) {
  const reduce = window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  panel.scrollIntoView({ behavior: reduce ? "auto" : "smooth", block: "start" });
  panel.focus({ preventScroll: true });
}

// logSeq orders concurrent log loads, exactly as historySeq orders history
// loads. Without it an older response lands last and the panel shows one
// download's log under another download's title — and therefore, since G8, next
// to the wrong hint. `log_dir` is configurable and may sit on a slow or network
// volume, so "the loopback is fast" is not the guarantee it looks like.
let logSeq = 0;

// showLog fetches the per-job .log and shows it in the panel. The response is
// plain text served with nosniff, and it lands in a <pre> via textContent, so a
// log full of markup is displayed, never interpreted.
async function showLog(h) {
  const panel = $("logPanel");
  $("logTitle").textContent = "Dettaglio: " + (h.title || h.url);
  $("logBody").textContent = "Carico…";
  panel.hidden = false;
  // Before the fetch: the panel must be on screen while it loads, not after.
  revealPanel(panel);
  const seq = ++logSeq;
  try {
    const r = await fetch("/api/history/log?id=" + encodeURIComponent(h.id));
    const text = await r.text();
    if (seq !== logSeq) return; // a newer log already won the panel
    if (!r.ok) throw new Error(text || "errore " + r.status);
    $("logBody").textContent = text;
  } catch (err) {
    if (seq !== logSeq) return;
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
  const window = " (la cronologia copre " + retentionLabel() + ")";
  if (historyQuery.q) return "Nessun download corrisponde a questa ricerca." + window;
  if (historyQuery.filter === "failed") return "Nessun download non riuscito. Buon segno." + window;
  if (historyQuery.filter === "ok") return "Nessun download completato." + window;
  return "Nessun download registrato. Il primo comparirà qui.";
}

// loadHistory replaces the list; loadMoreHistory appends the next page. Paging
// APPENDS so the rows already on screen do not jump.
// historySeq orders concurrent loads. Without it an older response could land
// after a newer one — type a search, click a filter within the debounce, and the
// unfiltered rows would replace the filtered ones while the chip showed pressed;
// worse, BOTH responses added to the same offset, so the next "Carica altri"
// skipped a whole page the user never saw. The same race fires with no user
// haste at all when an SSE queue frame refreshes the view mid-load.
let historySeq = 0;

async function loadHistory(append) {
  const list = $("historyList");
  if (!append) {
    historyQuery.offset = 0;
    historyPaged = false;
  } else {
    historyPaged = true;
  }
  const seq = ++historySeq;
  try {
    const r = await fetch(historyURL());
    const page = await r.json().catch(() => ({}));
    if (seq !== historySeq) return; // a newer load already won
    if (!r.ok) throw new Error(page.error || "errore " + r.status);
    const rows = (page.items || []).map((h) => historyRow(h, true));
    if (append) {
      for (const row of rows) list.appendChild(row);
    } else if (rows.length) {
      replaceChildren(list, rows);
    } else {
      replaceChildren(list, [el("p", "empty", historyEmptyText())]);
    }
    // Take the offset from the SERVER's echo rather than accumulating locally,
    // so a dropped or superseded response cannot desynchronise the paging.
    historyQuery.offset = (page.offset || 0) + (page.items || []).length;
    $("loadMore").hidden = !page.more;
  } catch (err) {
    if (seq !== historySeq) return;
    setMsg("historyMsg", "bad", "Impossibile leggere la cronologia: " + err.message);
  }
}

for (const chip of document.querySelectorAll(".chip")) {
  chip.addEventListener("click", () => {
    $("logPanel").hidden = true; // an explicit change; the panel is about the old list
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
    $("logPanel").hidden = true;
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

// retentionDays is the window the history covers, as the server reports it.
// "Never a number without its window" (ux-principles.md §5): without saying so,
// a search for something downloaded two months ago answers "nessun download
// corrisponde" and the user concludes ytdl lost their downloads.
let retentionDays = 0;

function retentionLabel() {
  if (retentionDays <= 0) return "da sempre";
  if (retentionDays === 1) return "ultimo giorno";
  return "ultimi " + retentionDays + " giorni";
}

// setOpenFolderAvailability applies ux-principles.md §4 to the one setting that
// depends on a desktop launcher: where ytdl has none, open_folder_on_done can do
// nothing on ANY channel, so the control is disabled with the reason rather than
// left live to fail. (Its other limit — never firing for a download started in
// the GUI, because those are all queued — is stated by the label and by the
// disclosure it sits in; the CLI still honours it, so the control stays.)
// The two hints are swapped by `hidden`, never by rewriting text: writing
// textContent over the normal hint flattened the <code>ytdl &lt;link&gt;</code>
// sample into prose on the first state frame, which is every time.
function setOpenFolderAvailability(canOpen) {
  $("s_openFolderOnDone").disabled = !canOpen;
  $("openFolderHint").hidden = !canOpen;
  $("openFolderUnavailable").hidden = canOpen;
}

// sessionTrusted says whether this state frame may speak for the session
// override. It may not when a PUT was issued after the frame was requested: the
// frame is then OLDER than a value the user has just applied, and adopting it
// would blank the field while the daemon keeps using the folder they set — G2
// again, in the opposite direction and with no marker to show it. Same shape as
// historySeq, and the same reason.
function applyState(s, sessionTrusted) {
  applyQueue(s.queue);
  renderRecent(s.history);
  setDaemon(s.daemonRunning);
  setOpenFolderAvailability(s.canOpen !== false);
  applyUpdate(s.update);
  retentionDays = Number(s.retentionDays) || 0;
  $("historyWindow").textContent = "— " + retentionLabel();
  if (sessionTrusted === false) return;
  // Adopt the server's value only while the field agrees with what WAS in force:
  // a state refresh (a reconnect, say) must never overwrite an edit in progress,
  // and must never silently make a pending edit look applied.
  const wasDirty = sessionDirty();
  appliedSessionOut = s.sessionOutputDir || "";
  if (!wasDirty) $("sessionOut").value = appliedSessionOut;
  refreshSessionPending();
}

async function loadState() {
  // Read the epoch BEFORE the request goes out, so a PUT that lands while it is
  // in flight invalidates this frame's view of the session folder.
  const epoch = sessionEpoch;
  const r = await fetch("/api/state");
  if (!r.ok) {
    const data = await r.json().catch(() => ({}));
    throw new Error(data.error || "errore " + r.status);
  }
  applyState(await r.json(), epoch === sessionEpoch);
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
    // Repaint the numbers only — never rebuild the row and its button.
    paintProgress(p.id);
  });
  es.onerror = () => {
    setDaemon(false);
    // A transient drop is retried by the browser itself. A CLOSED EventSource is
    // NOT: per the spec a non-200 response (a 401 from a daemon restarted with a
    // fresh token, say) fails the connection permanently. Without a rebuild the
    // tab would sit there holding no SSE connection at all — and an open SSE
    // connection is the "GUI connected" clause of the daemon's exit test
    // (ADR-0008), so the daemon could idle-exit under a tab the user still has
    // open. Reconnect with a backoff instead of dying quietly.
    if (es.readyState === EventSource.CLOSED) {
      es.close();
      scheduleReconnect();
    }
  };
}

let reconnectDelay = 1000;
let reconnectTimer = 0;

function scheduleReconnect() {
  clearTimeout(reconnectTimer);
  reconnectTimer = setTimeout(() => {
    reconnectDelay = Math.min(reconnectDelay * 2, 30000);
    connect();
    // A fresh state fetch re-syncs anything missed while disconnected.
    loadState().then(() => { reconnectDelay = 1000; }).catch(() => {});
  }, reconnectDelay);
}

// ---- new download --------------------------------------------------------

// resetPerDownloadControls returns the one-shot controls to their resolved
// default after a SUCCESSFUL submit (ux-principles.md §8.1). The folder field
// promises "vale solo per questo download" and used to stay set, with its
// disclosure still open, so the next download silently went to the same
// override: the label said one-shot and the surface delivered sticky (G3).
// The playlist checkbox was initialised from playlist_default and then never
// touched again (G4). Left ticked, the next link that carries "&list=" turns one
// track into the whole playlist — which is why it goes back to the DEFAULT here,
// not simply to unticked.
// The format select is deliberately NOT reset here, though §8.1 names it: the
// rule reaches it in Cycle 6, together with the visible effective value and
// explicit promotion (roadmap, "Cycle 5 closing" § deliberately out).
function resetPerDownloadControls() {
  $("url").value = "";
  $("outDir").value = "";
  $("outDirBox").open = false;
  $("playlist").checked = playlistDefault();
}

// playlistDefault is the resolved default as the SERVER last stated it, so the
// control returns to what is actually configured rather than to a guess.
function playlistDefault() {
  return !!(savedSettings && savedSettings.playlistDefault);
}

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
    resetPerDownloadControls();
    $("url").focus();
    if (data.queue) applyQueue(data.queue);
  } catch (err) {
    setMsg("msg", "bad", err.message);
  } finally {
    $("go").disabled = false;
  }
});

// ---- settings ------------------------------------------------------------

// ---- the session folder --------------------------------------------------
// Only "Applica alla sessione" issues the PUT; typing in the field changes
// nothing. Nothing said so, while the settings form immediately below has had a
// sticky unsaved-changes bar all along — so the field could show one folder
// while the download went to another (G2). ux-principles.md §8.4: a control
// changed but not yet applied must say so.

let appliedSessionOut = ""; // the override the server says is in force
// Bumped by every PUT. A /api/state frame requested before the bump cannot speak
// for the session folder any more (see applyState).
let sessionEpoch = 0;

// Both sides are trimmed. Comparing a trimmed field against an untrimmed stored
// value pins the marker open for ever on a value the GUI never typed (the API
// stores whatever it is given), which is the "a warning that is always on is a
// warning nobody reads" failure the settings bar already paid for once.
function sessionDirty() {
  return $("sessionOut").value.trim() !== appliedSessionOut.trim();
}

function refreshSessionPending() {
  $("sessionPending").hidden = !sessionDirty();
}

$("sessionOut").addEventListener("input", refreshSessionPending);

$("saveSession").addEventListener("click", async () => {
  const dir = $("sessionOut").value.trim();
  const btn = $("saveSession");
  btn.disabled = true; // every other mutating control in this file does this
  sessionEpoch++;
  try {
    // fetch rejects only on a NETWORK error, so without an r.ok check a 400 or a
    // 401 landed in the success branch: the GUI answered "✓ aggiornata" and the
    // next download went to the configured folder anyway. A ✓ on a failed write
    // is worse than an error.
    const r = await fetch("/api/session", {
      method: "PUT", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ outputDir: dir }),
    });
    const data = await r.json().catch(() => ({}));
    if (!r.ok) throw new Error(data.error || "errore " + r.status);
    // What is in force comes from the SERVER's echo, not from the field: only
    // then does "no pending change" mean the two actually agree.
    appliedSessionOut = typeof data.sessionOutputDir === "string" ? data.sessionOutputDir : dir;
    // Only OUR value is ours to normalise: if the user kept typing while the PUT
    // was in flight, overwriting the field would discard what they wrote AND
    // hide the pending marker, so the loss would leave no trace.
    if ($("sessionOut").value.trim() === dir) $("sessionOut").value = appliedSessionOut;
    refreshSessionPending();
    setMsg("sessionMsg", "ok", "Cartella di sessione aggiornata.");
  } catch (err) {
    setMsg("sessionMsg", "bad", err.message);
  } finally {
    btn.disabled = false;
  }
});

const SETTING_IDS = ["outputDir", "format", "audioQuality", "playlistDefault", "nameTemplate",
  "stripBrackets", "stripTags", "embedThumbnail", "embedMetadata", "logDir", "logRetentionDays",
  "breadcrumbOnFailure", "notify", "notifyOn", "notifyForeground", "notifySound", "concurrency",
  "jobTimeout", "openFolderOnDone", "updateCheck"];

function fillSettings(s) {
  // concurrency 0 means "no cap"; show it as an explicit choice rather than as a
  // number, so an empty box can never silently mean unlimited.
  $("s_noLimit").checked = s.concurrency === 0;
  $("s_concurrency").value = s.concurrency === 0 ? "" : s.concurrency;
  $("s_concurrency").disabled = s.concurrency === 0;
  fillSelect($("s_format"), FORMATS, s.format);
  fillSelect($("s_audioQuality"), AUDIO_QUALITIES, s.audioQuality);
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

// settingsDirty compares FIELD BY FIELD, deliberately not by serialising both
// sides. JSON.stringify is key-order sensitive: readSettings() builds its object
// in SETTING_IDS order with concurrency appended last, while the server document
// carries it in Go struct order — so two objects with identical values produced
// different strings and the bar was pinned open from the first paint, in every
// state, which is the "a warning that is always on is a warning nobody reads"
// failure. A field-wise loop also survives the server adding a key.
function settingsDirty() {
  if (!savedSettings) return false;
  const current = readSettings();
  for (const k of SETTING_IDS) {
    const a = current[k], b = savedSettings[k];
    if (a === undefined && b === undefined) continue;
    // Numeric fields round-trip through form values as strings; compare on
    // value, not on type.
    if (typeof a === "number" || typeof b === "number") {
      if (Number(a) !== Number(b)) return true;
      continue;
    }
    if (a !== b) return true;
  }
  return false;
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
  if (!r.ok) {
    // Without this the 401 body {"error":…} was fed to fillSettings, which wrote
    // the literal string "undefined" into every text input.
    const data = await r.json().catch(() => ({}));
    throw new Error(data.error || "errore " + r.status);
  }
  const s = await r.json();
  applySettings(s);
  fillSelect($("format"), FORMATS, s.format);
  $("playlist").checked = playlistDefault();
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
    // The Download view's one-shot controls follow the new defaults, exactly as
    // the format select already did: a default the user has just changed must
    // not leave the other view showing the old one.
    fillSelect($("format"), FORMATS, data.format);
    $("playlist").checked = playlistDefault();
    setMsg("settingsMsg", "ok", "Impostazioni salvate.");
  } catch (err) {
    setMsg("settingsMsg", "bad", err.message);
  } finally {
    $("saveSettings").disabled = false;
  }
});

// ADR-0008: closing the GUI while the queue still has work leaves the daemon
// draining UNATTENDED — it keeps going until the queue empties. That is what the
// warning is about, so it must not imply the opposite: the old wording ("Vuoi
// uscire? Hai download in coda.") read as a threat to cancel the queue (G10).
window.addEventListener("beforeunload", (e) => {
  if (counts.pending + counts.running > 0) {
    e.preventDefault();
    e.returnValue = "I download proseguono anche se chiudi: qui smetteresti solo di vederli.";
    return e.returnValue;
  }
});

// The SSE connection is opened WHATEVER happens to the initial fetches. It used
// to be chained after them, so a single failed /api/state left the tab with no
// EventSource for its whole lifetime — and an open SSE connection is the "GUI
// connected" clause of the daemon's exit test (ADR-0008), so the daemon could
// idle-exit under a tab the user still had open.
showView(currentView());
loadSettings()
  .then(loadState)
  .catch((err) => setMsg("msg", "bad", err.message || "Motore non raggiungibile."))
  .finally(connect);

// ---- updates -------------------------------------------------------------
//
// One axis, one verdict, one action (ADR-0016 §8). Two surfaces, because
// noticing and checking are different jobs: a banner above every view for the
// news, and a block in Impostazioni that is always there for "which version am
// I on?".
//
// Everything below builds DOM and writes textContent. No innerHTML anywhere —
// these strings include versions and installer output, and spa_test.go enforces
// the ban.

let updateInfo = null;      // the last /api/state update object, or null
let updatePollTimer = 0;
let updateVersionBefore = "";  // the ytdl version in force when the update started
let updateDeadline = 0;        // when to stop waiting for the interface to return

// How long to wait for the new interface to come back before saying so. Past
// this the page stops guessing and names the one thing that reopens it — the
// only place a Terminal is mentioned, and the thing Cycle 6-launch removes.
const RESTART_TIMEOUT_MS = 60000;
const UPDATE_POLL_MS = 1500;

function applyUpdate(u) {
  // No update object means the capability is absent: render nothing at all
  // rather than a dead control (ux-principles.md §4).
  if (!u) {
    $("updateBanner").hidden = true;
    $("updateSection").hidden = true;
    return;
  }
  $("updateSection").hidden = false;
  updateInfo = u;
  renderUpdateBanner();
  renderUpdateVersions();
  renderUpdateState();
  renderUpdateChanges();
  renderUpdateAction();
}

// updateStateText is the three verdict states, kept distinct. "Non verificato"
// is not "sei aggiornato", and rounding one up to the other is the defect this
// whole surface exists to avoid (ADR-0016 §8).
function updateStateText(u) {
  if (!u.enabled && !u.checkedAt) return "Controllo automatico disattivato.";
  if (!u.checkedAt) return "Aggiornamenti non verificati: nessun controllo ancora eseguito.";
  const when = whenText(u.checkedAt);
  if (!u.known) return "Aggiornamenti non verificati — ultimo tentativo " + when + ".";
  if (u.available) return "È disponibile un aggiornamento — verificato " + when + ".";
  return "Sei aggiornato — verificato " + when + ".";
}

function renderUpdateState() {
  const p = $("updateState");
  p.textContent = updateStateText(updateInfo);
  // The consent state is a separate sentence, so it never gets folded into the
  // verdict: a user who turned the check off is up to date as of a date, not
  // "unknown".
  if (!updateInfo.enabled && updateInfo.checkedAt) {
    p.textContent += " Il controllo automatico è disattivato.";
  }
}

// The installed versions are LOCAL facts and are always shown, whether or not
// any probe ever succeeded (ADR-0016 §8).
function renderUpdateVersions() {
  const dl = $("updateVersions");
  const inst = updateInfo.installed || {};
  const rows = [];
  for (const [label, value] of [["ytdl", inst.ytdl], ["yt-dlp", inst.ytDlp], ["ffmpeg", inst.ffmpeg]]) {
    const dt = el("dt", "", label);
    const dd = el("dd", "", value || "non installato");
    if (!value) dd.className = "muted";
    rows.push(dt, dd);
  }
  // A dependency ytdl did not install is a different problem with a different
  // remedy: the pin is simply not in force for it.
  for (const name of updateInfo.foreign || []) {
    rows.push(el("dt", "warn", name), el("dd", "warn",
      "non installato da ytdl: la versione verificata non è quella in uso"));
  }
  // Installed by us, but not the build ytdl vouches for — that build was
  // withdrawn upstream, and staying installable won over staying verifiable.
  // Saying nothing here would be claiming a guarantee that was not obtained.
  for (const name of updateInfo.unattested || []) {
    rows.push(el("dt", "warn", name), el("dd", "warn",
      "non verificato: la versione attestata non è più disponibile"));
  }
  replaceChildren(dl, rows);
}

function renderUpdateChanges() {
  const table = $("updateChanges");
  const changes = updateInfo.changes || [];
  table.hidden = changes.length === 0;
  replaceChildren($("updateChangesBody"), changes.map((c) => {
    const tr = el("tr");
    tr.append(el("td", "", c.component), el("td", "", c.from), el("td", "", c.to));
    return tr;
  }));
}

// blockedText names the reason AND the count — an action that cannot work is
// disabled with a reason, never rendered live and failed (ux-principles.md §4),
// and no number appears without what it counts (§5).
function blockedText(b) {
  if (!b) return "";
  if (b.reason === "running") return "Un aggiornamento è già in corso.";
  if (!b.pending) return "Non riesco a leggere la coda: l'aggiornamento parte a coda vuota.";
  const n = b.pending;
  return n + (n === 1 ? " download in corso" : " download in corso") +
    ": l'aggiornamento parte a coda vuota.";
}

// The banner shows whenever an update is available, INCLUDING with a non-empty
// queue. When the queue blocks the action it says so here too, so the disabled
// button in Impostazioni is never a mystery.
function renderUpdateBanner() {
  const banner = $("updateBanner");
  const show = updateInfo.available && !updateInfo.busy;
  banner.hidden = !show;
  if (!show) return;

  let text = "È disponibile un aggiornamento di ytdl.";
  if (updateInfo.blocked) text += " " + blockedText(updateInfo.blocked);
  $("updateBannerText").textContent = text;

  const go = button("Vedi", "secondary", () => {
    location.hash = hashForView("settings");
    $("updateSection").scrollIntoView({ block: "start" });
    $("checkUpdate").focus();
  });
  replaceChildren($("updateBannerActions"), [go]);
}

// renderUpdateAction draws the one action, or the disabled one with its reason.
function renderUpdateAction() {
  const slot = $("updateActionSlot");
  if (!updateInfo.available || updateInfo.busy) {
    replaceChildren(slot, []);
    return;
  }
  if (updateInfo.blocked) {
    const b = button("Aggiorna", "", () => {});
    b.disabled = true;
    b.title = blockedText(updateInfo.blocked);
    replaceChildren(slot, [b, el("span", "muted", blockedText(updateInfo.blocked))]);
    return;
  }
  replaceChildren(slot, [button("Aggiorna", "", confirmUpdate)]);
}

// confirmUpdate names the cost honestly, and only the cost that will actually be
// paid: when ytdl itself is not changing there is no handover, so the sentence
// drops the restart clause rather than promising one (design §5, §6.2).
function confirmUpdate() {
  const changes = updateInfo.changes || [];
  const restarts = changes.some((c) => c.component === "ytdl");
  const what = changes.map((c) => c.component + " alla " + c.to).join(" e ");
  let msg = "Aggiorna " + (what || "ytdl") + ".";
  if (restarts) msg += " L'interfaccia si chiude e si riapre da sola.";
  msg += " I download devono essere finiti.";

  const slot = $("updateActionSlot");
  const go = button("Conferma", "", () => startUpdate(false));
  const cancel = button("Annulla", "secondary", renderUpdateAction);
  replaceChildren(slot, [el("span", "confirm-text", msg), go, cancel]);
  go.focus();
}

async function startUpdate(force) {
  setMsg("settingsMsg", "", "");
  updateVersionBefore = (updateInfo && updateInfo.installed && updateInfo.installed.ytdl) || "";
  // Reset per run: a deadline left over from a previous attempt would make a
  // retry report "non sono riuscito a riaprire l'interfaccia" the instant it
  // finished.
  updateDeadline = 0;
  try {
    const st = await api("/api/update", { force: !!force });
    showUpdatePanel(st);
    scheduleUpdatePoll();
  } catch (err) {
    // A refusal carries its reason (the queue filled up between render and
    // click, say) rather than a bare failure.
    setMsg("settingsMsg", "bad", err.message);
    loadState().catch(() => {});
  }
}

function scheduleUpdatePoll() {
  clearTimeout(updatePollTimer);
  updatePollTimer = setTimeout(pollUpdate, UPDATE_POLL_MS);
}

// The panel POLLS rather than listening on SSE. The SSE connection dies at the
// handover by construction, and a transport that disappears exactly when the
// news matters is the wrong transport: independent polls fail during the gap and
// succeed after it, which is precisely the signal this needs (design §6.2).
async function pollUpdate() {
  let st = null;
  try {
    const r = await fetch("/api/update/status");
    if (r.ok) st = await r.json();
  } catch (e) { /* the server is mid-handover; that is expected, keep polling */ }

  if (st) showUpdatePanel(st);

  if (st && st.state === "done" && st.changed) {
    // The old daemon reported success and is now handing over. Wait for the
    // server that answers to be the NEW build, then reload exactly once.
    if (!updateDeadline) updateDeadline = Date.now() + RESTART_TIMEOUT_MS;
    if (await newBuildIsServing()) {
      // The one legitimate reload (ADR-0016 §10): at this moment the document is
      // stale BY DEFINITION, because the server that will answer its next
      // request is a different build from the one that served it.
      location.reload();
      return;
    }
    if (Date.now() > updateDeadline) {
      $("updatePanelText").textContent =
        "L'aggiornamento è riuscito, ma non sono riuscito a riaprire l'interfaccia da solo. " +
        "Riaprila con  ytdl gui  dal Terminale.";
      return;
    }
  } else if (st && (st.state === "done" || st.state === "failed" || st.state === "abandoned")) {
    // Done without a restart, failed, or abandoned: nothing more to wait for.
    // Abandoned belongs here or the page polls a run nobody will ever finish.
    loadState().catch(() => {});
    return;
  }
  scheduleUpdatePoll();
}

// newBuildIsServing reports whether the server answering now is running the
// version the installer put down. Comparing versions is exact; waiting for a
// failed request and then a successful one would miss a handover fast enough to
// leave no gap.
async function newBuildIsServing() {
  try {
    const r = await fetch("/api/state");
    if (!r.ok) return false;
    const s = await r.json();
    const now = s.update && s.update.installed && s.update.installed.ytdl;
    return !!now && now !== updateVersionBefore;
  } catch (e) {
    return false;
  }
}

function showUpdatePanel(st) {
  const panel = $("updatePanel");
  panel.hidden = false;
  $("updateBanner").hidden = true;

  const text = $("updatePanelText");
  const actions = $("updatePanelActions");
  const log = $("updateLog");
  log.hidden = true;
  replaceChildren(actions, []);

  if (st.state === "running") {
    text.textContent = "Aggiornamento in corso…";
    return;
  }
  if (st.state === "failed" || st.state === "abandoned") {
    // A partial failure is never summarised: what is offered is the exit code's
    // consequence in words, the log, and a retry that reinstalls everything.
    //
    // "abandoned" is the case where nobody was left to read the exit code at all:
    // the process watching the installer died first (the machine was restarted
    // mid-install, say). The installer was detached and very probably finished —
    // but that is exactly the guess a surface may not make, so this says what is
    // actually known and points at the same log (design §7.3).
    text.textContent = st.state === "abandoned"
      ? "Non so come sia andato questo aggiornamento: ytdl si è chiuso prima che finisse."
      : "L'aggiornamento non è riuscito. ytdl è rimasto quello di prima.";
    const detail = button("Vedi il dettaglio", "secondary", () => {
      log.hidden = !log.hidden;
      log.textContent = st.logTail || "(nessun output)";
    });
    replaceChildren(actions, [detail, button("Riprova", "", () => startUpdate(true))]);
    return;
  }
  if (st.state === "done") {
    text.textContent = st.changed
      ? "Aggiornato. Riapro l'interfaccia…"
      : "Aggiornato. Non serve riavviare nulla.";
    return;
  }
  panel.hidden = true;
}

$("checkUpdate").addEventListener("click", async () => {
  const b = $("checkUpdate");
  b.disabled = true;
  const was = b.textContent;
  b.textContent = "Controllo…";
  try {
    // The manual check works even with the automatic one off: consent is about
    // the machine phoning home by itself, not the user's right to ask.
    applyUpdate(await api("/api/update/check", {}));
  } catch (err) {
    setMsg("settingsMsg", "bad", err.message);
  } finally {
    b.textContent = was;
    b.disabled = false;
  }
});
