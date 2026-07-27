"use strict";
const $ = (id) => document.getElementById(id);
const FORMATS = ["mp3", "flac", "m4a", "opus", "wav"];
const NOTIFY_ON = { both: "sempre", success: "solo successo", failure: "solo errore" };
// Mirrors config.DefaultConcurrency: the fallback when the field is left blank.
const DEFAULT_CONCURRENCY = 3;

// Live state: the last queue frame plus per-job progress, keyed by spool id.
let counts = { pending: 0, running: 0, done: 0, failed: 0 };
let running = [], pending = [];
const progress = new Map();

function esc(s) {
  return String(s == null ? "" : s).replace(/[&<>"']/g, (c) =>
    ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
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

function renderJobs() {
  const rEl = $("running"), pEl = $("pending");
  if (!running.length) {
    rEl.innerHTML = '<p class="empty">Nessun download in corso.</p>';
  } else {
    rEl.innerHTML = running.map((j) => {
      const p = progress.get(j.id);
      // The only value interpolated into an attribute rather than through esc():
      // force it numeric so it can never carry markup, whatever the API sends.
      const pct = Math.max(0, Math.min(100, Number(p && p.percent) || 0));
      const title = p && p.title ? p.title : j.url;
      let line = p ? (p.status === "processing" ? "elaborazione…" :
        [p.percent >= 0 ? p.percent.toFixed(1) + "%" : null, p.speed, p.eta ? "ETA " + p.eta : null]
          .filter(Boolean).join(" · ")) : "avvio…";
      return '<div class="job"><div class="u">' + esc(title) + "</div>" +
        '<div class="bar"><i style="width:' + pct + '%"></i></div>' +
        '<div class="meta">' + esc(line) + " · ." + esc(j.format) + "</div></div>";
    }).join("");
  }
  if (!pending.length) {
    pEl.innerHTML = '<p class="empty">Coda vuota.</p>';
  } else {
    pEl.innerHTML = pending.map((j) =>
      '<div class="job"><div class="u">' + esc(j.url) + "</div>" +
      '<div class="meta">.' + esc(j.format) + (j.playlist ? " · playlist" : "") + "</div></div>").join("");
  }
}

function renderHistory(items) {
  const el = $("history");
  if (!items || !items.length) {
    el.innerHTML = '<p class="empty">Nessun download registrato.</p>';
    return;
  }
  el.innerHTML = items.map((h) => {
    const when = new Date(h.time).toLocaleString("it-IT", { dateStyle: "short", timeStyle: "short" });
    const mark = h.success ? '<span class="ok">✓</span>' : '<span class="bad">✗</span>';
    return '<div class="job"><div class="u">' + mark + " " + esc(h.title || h.url) + "</div>" +
      '<div class="meta">' + esc(when) + " · ." + esc(h.format) + " · " + esc(h.mode) + "</div></div>";
  }).join("");
}

function applyQueue(q) {
  counts = q.counts;
  running = q.running || [];
  pending = q.pending || [];
  // Drop progress for jobs that are no longer running, so the map stays bounded.
  const live = new Set(running.map((j) => j.id));
  for (const id of progress.keys()) if (!live.has(id)) progress.delete(id);
  renderJobs();
}

function setDaemon(on) {
  $("daemonDot").className = "dot" + (on ? " on" : "");
  $("daemonTxt").textContent = on ? "motore attivo" : "motore inattivo";
}

async function loadState() {
  const r = await fetch("/api/state");
  const s = await r.json();
  applyQueue(s.queue);
  renderHistory(s.history);
  setDaemon(s.daemonRunning);
  $("sessionOut").value = s.sessionOutputDir || "";
}

function connect() {
  const es = new EventSource("/api/events");
  es.addEventListener("queue", (e) => {
    applyQueue(JSON.parse(e.data));
    // A queue change usually means a job finished: refresh the history too.
    fetch("/api/state").then((r) => r.json()).then((s) => {
      renderHistory(s.history);
      setDaemon(s.daemonRunning);
    }).catch(() => {});
  });
  es.addEventListener("progress", (e) => {
    const p = JSON.parse(e.data);
    progress.set(p.id, p);
    renderJobs();
  });
  es.onerror = () => setDaemon(false);
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
  $("msg").textContent = "";
  try {
    const r = await fetch("/api/downloads", {
      method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body),
    });
    const data = await r.json();
    if (!r.ok) throw new Error(data.error || "errore");
    $("msg").innerHTML = '<span class="ok">✓ Accodato.</span>';
    $("url").value = "";
    if (data.queue) applyQueue(data.queue);
  } catch (err) {
    $("msg").innerHTML = '<span class="bad">✗ ' + esc(err.message) + "</span>";
  } finally {
    $("go").disabled = false;
  }
});

$("saveSession").addEventListener("click", async () => {
  const dir = $("sessionOut").value.trim();
  await fetch("/api/session", {
    method: "PUT", headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ outputDir: dir }),
  });
  $("settingsMsg").innerHTML = '<span class="ok">✓ Cartella di sessione aggiornata.</span>';
});

const SETTING_IDS = ["outputDir", "format", "audioQuality", "playlistDefault", "nameTemplate",
  "stripBrackets", "stripTags", "embedThumbnail", "embedMetadata", "logDir", "logRetentionDays",
  "breadcrumbOnFailure", "notify", "notifyOn", "notifyForeground", "notifySound", "concurrency"];

function fillSettings(s) {
  // concurrency 0 means "no cap"; show it as an explicit choice rather than as a
  // number, so an empty box can never silently mean unlimited.
  $("s_noLimit").checked = s.concurrency === 0;
  $("s_concurrency").value = s.concurrency === 0 ? "" : s.concurrency;
  $("s_concurrency").disabled = s.concurrency === 0;
  fillSelect($("s_format"), FORMATS, s.format);
  fillSelect($("s_audioQuality"), ["0","1","2","3","4","5","6","7","8","9"], s.audioQuality);
  fillSelect($("s_notifyOn"), Object.keys(NOTIFY_ON), s.notifyOn, NOTIFY_ON);
  for (const k of SETTING_IDS) {
    const el = $("s_" + k);
    if (!el) continue;
    if (el.type === "checkbox") el.checked = !!s[k];
    else el.value = s[k];
  }
}

function readSettings() {
  const out = {};
  for (const k of SETTING_IDS) {
    if (k === "concurrency") continue; // handled explicitly below
    const el = $("s_" + k);
    if (!el) continue;
    if (el.type === "checkbox") out[k] = el.checked;
    else if (el.type === "number") out[k] = parseInt(el.value, 10) || 0;
    else out[k] = el.value;
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
});

async function loadSettings() {
  const r = await fetch("/api/settings");
  const s = await r.json();
  fillSettings(s);
  fillSelect($("format"), FORMATS, s.format);
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
    fillSettings(data);
    $("settingsMsg").innerHTML = '<span class="ok">✓ Impostazioni salvate.</span>';
  } catch (err) {
    $("settingsMsg").innerHTML = '<span class="bad">✗ ' + esc(err.message) + "</span>";
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

loadSettings().then(loadState).then(connect).catch(() => {
  $("msg").innerHTML = '<span class="bad">✗ Motore non raggiungibile.</span>';
});
