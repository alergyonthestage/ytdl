package webui

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/alergyonthestage/ytdl/internal/config"
	"github.com/alergyonthestage/ytdl/internal/jobs"
	"github.com/alergyonthestage/ytdl/internal/logstore"
	"github.com/alergyonthestage/ytdl/internal/queue"
)

// pollInterval is how often each SSE client re-scans the spool for queue
// changes. Progress updates are pushed live; only the queue shape is polled.
const pollInterval = 1 * time.Second

// stateHistoryLimit caps the history embedded in the state payload. Since Cycle
// 5 the Download view shows only "ultimi download" and the full list lives in
// its own view behind /api/history, so the payload every SSE tick carries no
// longer has to hold 25 rows.
const stateHistoryLimit = 3

// historyPageLimit is the default page size of /api/history, and the cap on what
// one request may ask for — an unbounded limit would let a single request read
// the whole retention window into memory.
const historyPageLimit = 50

// historyPageMax bounds an explicit ?limit=.
const historyPageMax = 200

// logMaxBytes caps what /api/history/log returns. A per-job log is normally a
// few kilobytes; the cap is what keeps a pathological one from being streamed
// into the browser whole.
const logMaxBytes = 64 * 1024

// ---- DTOs (JSON shapes; the engine types have no json tags) --------------

type jobDTO struct {
	ID         string `json:"id"`
	URL        string `json:"url"`
	Title      string `json:"title,omitempty"` // resolved "Artist - Track" once the run knows it
	Format     string `json:"format"`
	Playlist   bool   `json:"playlist"`
	EnqueuedAt string `json:"enqueuedAt"`
}

type countsDTO struct {
	Pending int `json:"pending"`
	Running int `json:"running"`
	Done    int `json:"done"`
	Failed  int `json:"failed"`
}

type queueDTO struct {
	Pending []jobDTO  `json:"pending"`
	Running []jobDTO  `json:"running"`
	Counts  countsDTO `json:"counts"`
}

// historyDTO is one row of the history view. Beyond the record's own fields it
// carries three SERVER-COMPUTED capability flags, because the browser cannot
// stat the disk and the design ranks each row's primary action by exactly that
// (§8.3): "Apri" when the file is still there, "Riscarica" when it is not.
// Computing them here is also what keeps a rendered button and the action behind
// it from disagreeing — both come from jobs.Available.
//
// Note what is NOT here: the absolute path. Location is a display string, and
// every action is addressed by record ID. The client never sends a path back,
// which is the property the open endpoint's security rests on (design §7).
type historyDTO struct {
	ID       string `json:"id"`
	Time     string `json:"time"`
	URL      string `json:"url"`
	Title    string `json:"title,omitempty"`
	Mode     string `json:"mode"`
	Format   string `json:"format"`
	RC       int    `json:"rc"`
	Success  bool   `json:"success"`
	Count    int    `json:"count,omitempty"`
	Playlist bool   `json:"playlist,omitempty"`
	Error    string `json:"error,omitempty"`    // one-line failure reason, shown inline
	Location string `json:"location,omitempty"` // where it landed, ~-contracted, display only

	CanOpenFile   bool `json:"canOpenFile"`   // the audio file is there and openable
	CanOpenFolder bool `json:"canOpenFolder"` // its folder can be shown
	HasLog        bool `json:"hasLog"`        // a per-job .log survives for "vedi errore"
}

type historyPageDTO struct {
	Items  []historyDTO `json:"items"`
	Offset int          `json:"offset"`
	More   bool         `json:"more"` // whether another page exists ("Carica altri")
}

type stateDTO struct {
	Queue         queueDTO     `json:"queue"`
	History       []historyDTO `json:"history"`
	DaemonRunning bool         `json:"daemonRunning"`
	SessionOut    string       `json:"sessionOutputDir"`
	// CanOpen is the platform capability: false where ytdl has no launcher, so
	// the open/reveal controls are not rendered at all rather than rendered and
	// failed (ux-principles.md §4).
	CanOpen bool `json:"canOpen"`
	// RetentionDays is the window the history covers. "Never a number without its
	// window" (ux-principles.md §5) applied to the GUI: without it, a search for
	// something downloaded two months ago answers "nessun download corrisponde"
	// and the user concludes ytdl lost their downloads. 0 = kept forever.
	RetentionDays int `json:"retentionDays"`
}

type progressPayload struct {
	ID string `json:"id"`
	Progress
}

// settingsDTO mirrors config.Settings with the GUI's json field names; it is
// both the GET response and the PUT request body for the settings editor.
type settingsDTO struct {
	OutputDir           string `json:"outputDir"`
	Format              string `json:"format"`
	AudioQuality        string `json:"audioQuality"`
	PlaylistDefault     bool   `json:"playlistDefault"`
	NameTemplate        string `json:"nameTemplate"`
	StripBrackets       string `json:"stripBrackets"`
	StripTags           string `json:"stripTags"`
	EmbedThumbnail      bool   `json:"embedThumbnail"`
	EmbedMetadata       bool   `json:"embedMetadata"`
	LogDir              string `json:"logDir"`
	LogRetentionDays    int    `json:"logRetentionDays"`
	BreadcrumbOnFailure bool   `json:"breadcrumbOnFailure"`
	Notify              bool   `json:"notify"`
	NotifyOn            string `json:"notifyOn"`
	NotifyForeground    bool   `json:"notifyForeground"`
	NotifySound         bool   `json:"notifySound"`
	Concurrency         int    `json:"concurrency"` // 0 = unlimited
	JobTimeout          int    `json:"jobTimeout"`  // seconds; 0 = no limit
	OpenFolderOnDone    bool   `json:"openFolderOnDone"`
}

func toSettingsDTO(s config.Settings) settingsDTO {
	return settingsDTO{
		OutputDir: s.OutputDir, Format: s.Format, AudioQuality: s.AudioQuality,
		PlaylistDefault: s.PlaylistDefault, NameTemplate: s.NameTemplate,
		StripBrackets: s.StripBrackets, StripTags: s.StripTags,
		EmbedThumbnail: s.EmbedThumbnail, EmbedMetadata: s.EmbedMetadata,
		LogDir: s.LogDir, LogRetentionDays: s.LogRetentionDays,
		BreadcrumbOnFailure: s.BreadcrumbOnFailure, Notify: s.Notify,
		NotifyOn: s.NotifyOn, NotifyForeground: s.NotifyForeground,
		NotifySound: s.NotifySound, Concurrency: s.Concurrency,
		JobTimeout: s.JobTimeout, OpenFolderOnDone: s.OpenFolderOnDone,
	}
}

func (d settingsDTO) toSettings() config.Settings {
	return config.Settings{
		OutputDir: d.OutputDir, Format: d.Format, AudioQuality: d.AudioQuality,
		PlaylistDefault: d.PlaylistDefault, NameTemplate: d.NameTemplate,
		StripBrackets: d.StripBrackets, StripTags: d.StripTags,
		EmbedThumbnail: d.EmbedThumbnail, EmbedMetadata: d.EmbedMetadata,
		LogDir: d.LogDir, LogRetentionDays: d.LogRetentionDays,
		BreadcrumbOnFailure: d.BreadcrumbOnFailure, Notify: d.Notify,
		NotifyOn: d.NotifyOn, NotifyForeground: d.NotifyForeground,
		NotifySound: d.NotifySound, Concurrency: d.Concurrency,
		JobTimeout: d.JobTimeout, OpenFolderOnDone: d.OpenFolderOnDone,
	}
}

// ---- helpers -------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func entryToDTO(e queue.Entry) jobDTO {
	d := jobDTO{
		ID: e.ID, URL: e.Job.URL, Format: e.Job.Settings.Format,
		Playlist: e.Job.Playlist, EnqueuedAt: e.Job.EnqueuedAt.Format(time.RFC3339),
	}
	// A playlist's single resolved title would misrepresent the whole job, so it
	// stays unnamed — the same rule the CLI queue view follows (Cycle 4).
	if !e.Job.Playlist {
		d.Title = e.Job.Title
	}
	return d
}

func entriesToDTO(es []queue.Entry) []jobDTO {
	out := make([]jobDTO, 0, len(es))
	for _, e := range es {
		out = append(out, entryToDTO(e))
	}
	return out
}

// buildQueueDTO reads a spool snapshot into the wire shape. On a read error it
// returns an empty queue rather than failing — the GUI degrades to "nothing
// visible" instead of erroring the whole state fetch.
func (s *Server) buildQueueDTO() queueDTO {
	snap, err := s.deps.Spool.List()
	if err != nil {
		return queueDTO{Pending: []jobDTO{}, Running: []jobDTO{}}
	}
	p, r, d, f := snap.Counts()
	return queueDTO{
		Pending: entriesToDTO(snap.Pending),
		Running: entriesToDTO(snap.Running),
		Counts:  countsDTO{Pending: p, Running: r, Done: d, Failed: f},
	}
}

func (s *Server) daemonRunning() bool {
	return s.deps.DaemonRunning != nil && s.deps.DaemonRunning()
}

// queueSignature is a cheap change-detector for the SSE poll: two snapshots with
// the same signature are visually identical, so no queue frame is resent.
func (s *Server) queueSignature() string {
	snap, err := s.deps.Spool.List()
	if err != nil {
		return "err"
	}
	p, r, d, f := snap.Counts()
	ids := make([]string, 0, len(snap.Pending)+len(snap.Running))
	for _, e := range snap.Pending {
		ids = append(ids, "p"+e.ID)
	}
	for _, e := range snap.Running {
		ids = append(ids, "r"+e.ID)
	}
	sort.Strings(ids)
	return fmt.Sprintf("%d/%d/%d/%d|", p, r, d, f) + fmt.Sprint(ids)
}

// ---- handlers ------------------------------------------------------------

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if s.serveAsset(w, r) {
		return
	}
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeErr(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	// `ytdl gui` opens the page with ?t=<token>. Exchange it for a
	// SameSite=Strict cookie — which a cross-site page can never cause the
	// browser to send — then redirect to drop the secret from the address bar,
	// the history and any Referer.
	if t := r.URL.Query().Get("t"); t != "" && s.tokenMatches(t) {
		http.SetCookie(w, &http.Cookie{
			Name: tokenCookie, Value: s.deps.Token, Path: "/",
			HttpOnly: true, SameSite: http.SameSiteStrictMode,
		})
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	// no-store for the same reason the assets carry it: an upgraded ytdl serves a
	// new app.js against the same URL, and a stale DOCUMENT paired with a new
	// script is the other half of that pair — a missing element id makes $()
	// return null and the script dies half-initialised.
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(indexHTML)
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	_, retentionDays := s.historyStore()
	writeJSON(w, http.StatusOK, stateDTO{
		Queue:         s.buildQueueDTO(),
		History:       s.recentHistory(),
		DaemonRunning: s.daemonRunning(),
		SessionOut:    s.sessionOut(),
		CanOpen:       jobs.CanOpen(),
		RetentionDays: retentionDays,
	})
}

// recentHistory reads the last few records for the Download view's "ultimi
// download". A read error degrades to an empty list.
func (s *Server) recentHistory() []historyDTO {
	entries, err := s.loadHistory(logstore.QueryOpts{Limit: stateHistoryLimit})
	if err != nil {
		return []historyDTO{}
	}
	return s.toHistoryDTOs(entries)
}

// historyStore is where the GUI reads history from, and for how far back. It is
// re-resolved per request rather than taken from construction, because both are
// editable in the settings view this same server serves.
func (s *Server) historyStore() (dir string, retentionDays int) {
	dir, retentionDays = s.deps.LogDir, s.deps.RetentionDays
	if s.deps.Resolve != nil {
		if resolved, _ := s.deps.Resolve(config.Partial{}); resolved.LogDir != "" {
			dir, retentionDays = resolved.LogDir, resolved.LogRetentionDays
		}
	}
	return dir, retentionDays
}

// loadHistory runs one history query against the configured store, always within
// the retention window so a stale record outside it can never surface.
func (s *Server) loadHistory(opts logstore.QueryOpts) ([]logstore.Entry, error) {
	dir, retentionDays := s.historyStore()
	if dir == "" {
		return nil, nil
	}
	if retentionDays > 0 {
		opts.Since = time.Now().AddDate(0, 0, -retentionDays)
	}
	return logstore.Load(dir, opts)
}

func (s *Server) toHistoryDTOs(entries []logstore.Entry) []historyDTO {
	// Resolved once per response, not once per row: it is a PATH lookup.
	canOpen := jobs.CanOpen()
	out := make([]historyDTO, 0, len(entries))
	for _, e := range entries {
		out = append(out, s.toHistoryDTO(e, canOpen))
	}
	return out
}

func (s *Server) toHistoryDTO(e logstore.Entry, canOpen bool) historyDTO {
	d := historyDTO{
		ID: e.ID(), Time: e.Time.Format(time.RFC3339), URL: e.URL, Title: e.Title,
		Mode: e.Mode, Format: e.Format, RC: e.RC, Success: e.Success,
		Count: e.Count, Playlist: e.Playlist, Error: e.Error,
		Location: s.displayLocation(e),
	}
	// Only advertise what this platform can actually do, so a capability flag
	// never promises a button that would 501.
	if canOpen {
		d.CanOpenFile = jobs.Available(e, jobs.OpenFile)
		d.CanOpenFolder = jobs.Available(e, jobs.OpenFolder)
	}
	d.HasLog = !e.Success && s.logExists(e)
	return d
}

// displayLocation is where a successful job's files went, ~-contracted: the
// folder of the saved file when known (so a playlist shows its own subfolder),
// else the job's configured output dir. Display only — no action takes a path.
func (s *Server) displayLocation(e logstore.Entry) string {
	dir := e.Dir
	if e.Path != "" {
		dir = filepath.Dir(e.Path)
	}
	if dir == "" {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return dir
	}
	if dir == home {
		return "~"
	}
	prefix := home
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	if strings.HasPrefix(dir, prefix) {
		return "~" + string(filepath.Separator) + dir[len(prefix):]
	}
	return dir
}

// logPath is the per-job .log for a record, or "" when there is no log store or
// the derived name would somehow escape it. The name is DERIVED from the record
// (logstore.Entry.LogName), never supplied by the client; the containment check
// is a second line of defence in case a hand-edited record ever produced an
// exotic timestamp.
func (s *Server) logPath(e logstore.Entry) string {
	logDir, _ := s.historyStore()
	if logDir == "" {
		return ""
	}
	dir := filepath.Clean(logDir)
	path := filepath.Clean(filepath.Join(dir, e.LogName()))
	if filepath.Dir(path) != dir {
		return ""
	}
	return path
}

func (s *Server) logExists(e logstore.Entry) bool {
	path := s.logPath(e)
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

type downloadReq struct {
	URL       string  `json:"url"`
	Format    *string `json:"format"`
	OutputDir *string `json:"outputDir"`
	Playlist  bool    `json:"playlist"`
}

func (s *Server) handleDownloads(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var req downloadReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "corpo JSON non valido")
		return
	}
	cleanURL, err := jobs.ValidateURL(req.URL)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	settings, err := s.settingsForRun(req.Format, req.OutputDir)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	id, err := s.deps.Spool.Enqueue(queue.Job{URL: cleanURL, Playlist: req.Playlist, Settings: settings})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "impossibile accodare: "+err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"id": id, "queue": s.buildQueueDTO()})
}

// settingsForRun resolves the settings for one enqueue, applying the per-run
// output dir / format if given, else the per-session output dir, over the same
// env+config precedence the CLI uses.
func (s *Server) settingsForRun(format, outDir *string) (config.Settings, error) {
	flags := config.Partial{}
	if format != nil {
		if !config.ValidFormat(*format) {
			return config.Settings{}, fmt.Errorf("formato non valido: %s (ammessi %s)", *format, config.FormatList)
		}
		f := *format
		flags.Format = &f
	}
	switch {
	case outDir != nil && *outDir != "":
		d := *outDir
		flags.OutputDir = &d
	default:
		if so := s.sessionOut(); so != "" {
			flags.OutputDir = &so
		}
	}
	settings, _ := s.deps.Resolve(flags)
	return settings, nil
}

// handleHistory serves the full history view: window-filtered, searchable,
// paginated. The Download view's three rows ride along on /api/state; this is
// the separate query the Cronologia view pages through, so the SSE state payload
// does not have to carry a whole history every tick.
func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	q := r.URL.Query()
	limit := clampQueryInt(q.Get("limit"), historyPageLimit, 1, historyPageMax)
	offset := clampQueryInt(q.Get("offset"), 0, 0, 1<<20)

	// One extra row is fetched to answer "is there another page?" without a
	// second count query — the alternative is a "Carica altri" button that
	// sometimes loads nothing.
	entries, err := s.loadHistory(logstore.QueryOpts{
		Limit:  limit + 1,
		Offset: offset,
		// The view's three filters are tutti / completati / non riusciti, so the
		// query needs both halves; `failed` alone cannot express "only the ones
		// that worked".
		OnlyFailed: isTrue(q.Get("failed")),
		OnlyOK:     isTrue(q.Get("ok")),
		Search:     q.Get("q"),
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "impossibile leggere lo storico: "+err.Error())
		return
	}
	more := len(entries) > limit
	if more {
		entries = entries[:limit]
	}
	writeJSON(w, http.StatusOK, historyPageDTO{
		Items: s.toHistoryDTOs(entries), Offset: offset, More: more,
	})
}

// isTrue reads a boolean query parameter in the forms a URL actually carries.
func isTrue(v string) bool { return v == "1" || v == "true" || v == "yes" }

// clampQueryInt parses a query parameter, falling back to def for anything
// missing or unparseable and clamping the rest into [lo, hi]. A garbage limit
// must not become an unbounded read.
func clampQueryInt(raw string, def, lo, hi int) int {
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	if n < lo {
		return lo
	}
	if n > hi {
		return hi
	}
	return n
}

// idReq is the body shape of every record- or job-addressed action. The client
// sends an ID and nothing else — never a path, never a URL — which is what makes
// these endpoints safe to expose (design §7).
type idReq struct {
	ID     string `json:"id"`
	Target string `json:"target,omitempty"` // open only: "file" (default) | "folder"
}

func decodeIDReq(w http.ResponseWriter, r *http.Request) (idReq, bool) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "POST only")
		return idReq{}, false
	}
	var req idReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "corpo JSON non valido")
		return idReq{}, false
	}
	if strings.TrimSpace(req.ID) == "" {
		writeErr(w, http.StatusBadRequest, "id mancante")
		return idReq{}, false
	}
	return req, true
}

// handleQueueCancel stops a live job. This lifts ADR-0010's read-only-queue
// decision: a GUI-only user must be able to stop a stuck download without
// opening a terminal (design goal 3).
//
// The requested id is resolved against the CURRENT spool listing and refused if
// it is not there. That is not politeness about stale UI — a job id becomes a
// filename in the spool, so accepting an arbitrary string from an HTTP body
// would be a file-creation primitive. jobs.Cancel validates the shape as well;
// this is the layer that also makes the answer honest ("that job is gone").
func (s *Server) handleQueueCancel(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeIDReq(w, r)
	if !ok {
		return
	}
	snap, err := s.deps.Spool.List()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "impossibile leggere la coda: "+err.Error())
		return
	}
	if !liveJobExists(snap, req.ID) {
		writeErr(w, http.StatusNotFound, "questo download non è più in coda")
		return
	}
	wasPending, err := jobs.Cancel(s.deps.Spool, req.ID)
	if err != nil {
		// The marker is HOW a running job stops, so a failure to write it means
		// the cancel is a no-op. Never report success here.
		writeErr(w, http.StatusInternalServerError, "impossibile annullare: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"cancelled": true, "wasPending": wasPending, "queue": s.buildQueueDTO(),
	})
}

// liveJobExists reports whether id names a job that is currently pending or
// running. Terminal jobs are deliberately excluded: there is nothing to cancel.
func liveJobExists(snap queue.Snapshot, id string) bool {
	for _, group := range [][]queue.Entry{snap.Pending, snap.Running} {
		for _, e := range group {
			if e.ID == id {
				return true
			}
		}
	}
	return false
}

// handleHistoryAgain enqueues a fresh job from a history record ("Riscarica").
func (s *Server) handleHistoryAgain(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeIDReq(w, r)
	if !ok {
		return
	}
	e, ok := s.findRecord(w, req.ID)
	if !ok {
		return
	}
	settings, err := s.settingsForRun(nil, nil)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	id, err := jobs.Again(s.deps.Spool, e, settings)
	switch {
	case errors.Is(err, jobs.ErrAlreadyQueued):
		// Without this a double-click silently queues the same download twice.
		writeErr(w, http.StatusConflict, "è già in coda")
		return
	case errors.Is(err, jobs.ErrNoURL):
		writeErr(w, http.StatusBadRequest, "questo record non ha un link da riscaricare")
		return
	case errors.Is(err, jobs.ErrBadURL):
		writeErr(w, http.StatusBadRequest, "il link registrato per questo download non è valido: "+err.Error())
		return
	case err != nil:
		writeErr(w, http.StatusInternalServerError, "impossibile accodare: "+err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"id": id, "queue": s.buildQueueDTO()})
}

// handleHistoryOpen opens a downloaded file, or shows it in the file manager.
// This is the one endpoint that causes a program to be launched, so its whole
// design is that the CLIENT NEVER SUPPLIES A PATH: it sends a record id, the
// server looks the record up in its OWN store, and jobs.Open re-validates
// everything before anything is executed (design §7). The guards in front of it
// (loopback Host, session token, Origin, JSON content-type) mean a cross-site
// page never reaches this handler at all.
func (s *Server) handleHistoryOpen(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeIDReq(w, r)
	if !ok {
		return
	}
	if !jobs.CanOpen() {
		writeErr(w, http.StatusNotImplemented, "su questo sistema ytdl non sa aprire file")
		return
	}
	e, ok := s.findRecord(w, req.ID)
	if !ok {
		return
	}
	target := jobs.OpenFile
	switch req.Target {
	case "", "file":
	case "folder":
		target = jobs.OpenFolder
	default:
		writeErr(w, http.StatusBadRequest, "target non valido: usa file o folder")
		return
	}
	if err := openRecord(e, target); err != nil {
		writeErr(w, openStatus(err), openMessage(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"opened": true})
}

// openRecord performs the platform action. It is a package var only so a handler
// test can assert what WOULD be opened without opening windows on the machine
// running the tests; production is jobs.Open, which is where every check lives.
var openRecord = jobs.Open

// openStatus maps a refusal to a status code: a record that simply has nothing
// to open is a 404, a record whose path is not one ytdl may open is a 400, and
// anything else (the launcher itself failing) is a 500.
func openStatus(err error) int {
	switch {
	case errors.Is(err, jobs.ErrNoPath), errors.Is(err, jobs.ErrGone):
		return http.StatusNotFound
	case errors.Is(err, jobs.ErrOutsideDir), errors.Is(err, jobs.ErrNotAudio):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

func openMessage(err error) string {
	switch {
	case errors.Is(err, jobs.ErrNoPath):
		return "di questo download non è registrato il percorso del file"
	case errors.Is(err, jobs.ErrGone):
		return "il file non è più al suo posto: spostato, rinominato o eliminato"
	case errors.Is(err, jobs.ErrOutsideDir), errors.Is(err, jobs.ErrNotAudio):
		return "il percorso registrato non è un file audio prodotto da ytdl"
	default:
		return "impossibile aprire: " + err.Error()
	}
}

// handleHistoryLog serves a failed job's .log as plain text, for the "vedi
// errore" panel. The file name is DERIVED from the record, so no part of the
// request reaches the filesystem.
func (s *Server) handleHistoryLog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		writeErr(w, http.StatusBadRequest, "id mancante")
		return
	}
	e, ok := s.findRecord(w, id)
	if !ok {
		return
	}
	path := s.logPath(e)
	if path == "" {
		writeErr(w, http.StatusNotFound, "log non disponibile")
		return
	}
	// Refuse anything that is not a plain regular file BEFORE opening it. Lstat
	// does not follow symlinks, which is the point twice over:
	//
	//   - a FIFO planted under the derived log name makes os.Open block inside
	//     the open(2) syscall until a writer appears. Neither the request context
	//     nor a server shutdown can interrupt that, so every such request would
	//     pin a goroutine and a connection for the process's lifetime;
	//   - a symlink under that name served any file on the disk, because the
	//     containment check compares the derived PATH and os.Open resolves the
	//     link afterwards.
	//
	// Both need local write access to the log dir — the same trust level the
	// design already declares untrusted for history.jsonl itself.
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		writeErr(w, http.StatusNotFound, "log non disponibile (scaduto o mai scritto)")
		return
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		writeErr(w, http.StatusNotFound, "log non disponibile (scaduto o mai scritto)")
		return
	}
	defer f.Close()
	// Re-check on the open handle, closing the window between Lstat and open.
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() {
		writeErr(w, http.StatusNotFound, "log non disponibile")
		return
	}

	// Serve the TAIL, not the head. yt-dlp's actual error is the last thing in
	// the file, so on the pathological log the cap exists for, reading from the
	// start would show everything except the reason the user opened the panel.
	notice := ""
	budget := int64(logMaxBytes)
	if size := info.Size(); size > logMaxBytes {
		notice = fmt.Sprintf("[… log troppo lungo: mostro solo la parte finale, %d KB …]\n\n", logMaxBytes/1024)
		budget -= int64(len(notice))
		if _, err := f.Seek(size-budget, io.SeekStart); err != nil {
			notice, budget = "", logMaxBytes
		}
	}

	// text/plain + nosniff (set globally) so a .log full of HTML can never be
	// rendered as a document.
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", "inline")
	// The notice counts against the cap, so the response is bounded by
	// logMaxBytes whatever happens.
	io.WriteString(w, notice)
	_, _ = io.Copy(w, io.LimitReader(f, budget))
}

// findRecord resolves a record id, writing the 404 itself when there is no such
// record. The second return says whether the caller may continue.
//
// Two constraints the CLI's own lookup does not have:
//
//   - the id must be COMPLETE. logstore.Find accepts an unambiguous prefix
//     because that is the CLI's targeting grammar; over HTTP every caller holds
//     a full id from a DTO, and accepting prefixes would mean which record a
//     given request designates depends on the whole store's contents and can
//     change as records are appended.
//   - the record must be inside the retention window, the same one /api/history
//     clamps its listing to. Without it a record the policy says is gone stayed
//     a live launch and enqueue target — merely invisible.
func (s *Server) findRecord(w http.ResponseWriter, id string) (logstore.Entry, bool) {
	logDir, retentionDays := s.historyStore()
	if logDir == "" {
		writeErr(w, http.StatusNotFound, "storico non disponibile")
		return logstore.Entry{}, false
	}
	if !isFullRecordID(id) {
		writeErr(w, http.StatusBadRequest, "id non valido")
		return logstore.Entry{}, false
	}
	e, err := logstore.Find(logDir, id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "record non trovato")
		return logstore.Entry{}, false
	}
	if retentionDays > 0 && e.Time.Before(time.Now().AddDate(0, 0, -retentionDays)) {
		writeErr(w, http.StatusNotFound, "record non trovato")
		return logstore.Entry{}, false
	}
	return e, true
}

// isFullRecordID reports whether id is a complete 16-hex record id, the shape
// logstore.Entry.ID produces.
func isFullRecordID(id string) bool {
	id = strings.TrimSpace(id)
	if len(id) != recordIDLen {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}

// recordIDLen mirrors the length logstore.Entry.ID returns.
const recordIDLen = 16

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		resolved, _ := s.deps.Resolve(config.Partial{})
		writeJSON(w, http.StatusOK, toSettingsDTO(resolved))
	case http.MethodPut:
		var dto settingsDTO
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&dto); err != nil {
			writeErr(w, http.StatusBadRequest, "corpo JSON non valido")
			return
		}
		settings := dto.toSettings()
		// The job_timeout / open_folder_on_done carry-through that used to live
		// here is gone: both are now fields on the DTO, so the editor sends them
		// like every other setting and a save no longer resets what it could not
		// see.
		if err := validateSettings(settings); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if s.deps.ConfigPath == "" {
			writeErr(w, http.StatusInternalServerError, "percorso di configurazione non disponibile")
			return
		}
		if err := config.Save(s.deps.ConfigPath, settings); err != nil {
			writeErr(w, http.StatusInternalServerError, "impossibile salvare: "+err.Error())
			return
		}
		resolved, _ := s.deps.Resolve(config.Partial{})
		writeJSON(w, http.StatusOK, toSettingsDTO(resolved))
	default:
		writeErr(w, http.StatusMethodNotAllowed, "GET or PUT")
	}
}

// validateSettings rejects values the settings editor must not persist, before
// they reach config.Save. It mirrors the config layer's own validation.
func validateSettings(s config.Settings) error {
	if strings.TrimSpace(s.OutputDir) == "" {
		// A whitespace-only value would be written, then trimmed to "" on reload
		// and ignored with a warning — the GUI would report success while the
		// value silently snapped back to the default.
		return errors.New("la cartella di destinazione è obbligatoria")
	}
	if strings.TrimSpace(s.NameTemplate) == "" {
		// An empty name_template is a VALID config value that overrides the
		// built-in default, and yields "-o <dir>/.%(ext)s": every download becomes
		// the same hidden file, each overwriting the last. Silent data loss.
		return errors.New("il template del nome file è obbligatorio")
	}
	if !config.ValidFormat(s.Format) {
		return fmt.Errorf("formato non valido: %s (ammessi %s)", s.Format, config.FormatList)
	}
	if !config.ValidNotifyOn(s.NotifyOn) {
		return fmt.Errorf("notify_on non valido: %s (ammessi %s)", s.NotifyOn, config.NotifyOnList)
	}
	if len(s.AudioQuality) != 1 || s.AudioQuality[0] < '0' || s.AudioQuality[0] > '9' {
		return fmt.Errorf("audio_quality non valido: %s (0-9)", s.AudioQuality)
	}
	if s.LogRetentionDays < 0 {
		return fmt.Errorf("log_retention_days non può essere negativo")
	}
	if s.Concurrency < 0 {
		return fmt.Errorf("concurrency non può essere negativo (0 = unlimited)")
	}
	if s.JobTimeout < 0 {
		// config.Save rejects it too; catching it here turns a 500 into a 400
		// with a message the settings form can show next to the field.
		return fmt.Errorf("job_timeout non può essere negativo (0 = nessun limite)")
	}
	return nil
}

type sessionReq struct {
	OutputDir string `json:"outputDir"`
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		writeErr(w, http.StatusMethodNotAllowed, "PUT only")
		return
	}
	var req sessionReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "corpo JSON non valido")
		return
	}
	s.setSessionOut(req.OutputDir) // "" clears the override
	writeJSON(w, http.StatusOK, map[string]string{"sessionOutputDir": req.OutputDir})
}

// handleEvents is the SSE stream: an initial queue frame, then live progress
// frames (pushed) and queue frames (on change, polled), plus a keepalive comment
// so a dropped connection is noticed. It owns the single writing goroutine for
// this client, so no locking of w is needed.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming non supportato", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	c := s.hub.addClient()
	defer s.hub.removeClient(c)

	sendQueue := func() {
		data, err := json.Marshal(s.buildQueueDTO())
		if err != nil {
			return
		}
		writeSSE(w, "queue", data)
		flusher.Flush()
	}
	sendQueue()
	lastSig := s.queueSignature()

	poll := time.NewTicker(pollInterval)
	defer poll.Stop()
	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-c.ch: // never closed; the context is what ends this loop
			writeSSE(w, msg.event, msg.data)
			flusher.Flush()
		case <-poll.C:
			if sig := s.queueSignature(); sig != lastSig {
				lastSig = sig
				sendQueue()
			}
		case <-keepalive.C:
			io.WriteString(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

func writeSSE(w io.Writer, event string, data []byte) {
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
}
