package webui

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/alergyonthestage/ytdl/internal/config"
	"github.com/alergyonthestage/ytdl/internal/logstore"
	"github.com/alergyonthestage/ytdl/internal/queue"
)

// pollInterval is how often each SSE client re-scans the spool for queue
// changes. Progress updates are pushed live; only the queue shape is polled.
const pollInterval = 1 * time.Second

// historyLimit caps the recent-history list embedded in the state payload.
const historyLimit = 25

// ---- DTOs (JSON shapes; the engine types have no json tags) --------------

type jobDTO struct {
	ID         string `json:"id"`
	URL        string `json:"url"`
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

type historyDTO struct {
	Time    string `json:"time"`
	URL     string `json:"url"`
	Title   string `json:"title,omitempty"`
	Mode    string `json:"mode"`
	Format  string `json:"format"`
	RC      int    `json:"rc"`
	Success bool   `json:"success"`
}

type stateDTO struct {
	Queue         queueDTO     `json:"queue"`
	History       []historyDTO `json:"history"`
	DaemonRunning bool         `json:"daemonRunning"`
	SessionOut    string       `json:"sessionOutputDir"`
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
	return jobDTO{
		ID: e.ID, URL: e.Job.URL, Format: e.Job.Settings.Format,
		Playlist: e.Job.Playlist, EnqueuedAt: e.Job.EnqueuedAt.Format(time.RFC3339),
	}
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
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(indexHTML)
}

// validateDownloadURL rejects anything that is not a plain http(s) URL. This is
// a security boundary, not a nicety: core.BuildArgs appends the URL as the LAST
// argv element with no "--" separator, and is frozen byte-for-byte by the parity
// gate, so a value starting with "-" reaches yt-dlp as an OPTION. Real examples
// an attacker would reach for: --update-to=<repo>@<tag> makes yt-dlp replace its
// own binary from an arbitrary GitHub repo, and --config-locations=<file> loads
// arbitrary options (including --exec) from a planted file. The CLI path is not
// exposed the same way — its flag parser rejects a leading "-" first.
func validateDownloadURL(raw string) (string, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "", errors.New("url mancante")
	}
	if strings.HasPrefix(v, "-") {
		return "", errors.New("url non valido: non può iniziare con '-'")
	}
	u, err := url.Parse(v)
	if err != nil {
		return "", errors.New("url non valido")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("url non valido: sono ammessi solo http e https")
	}
	if u.Host == "" {
		return "", errors.New("url non valido: manca il dominio")
	}
	return v, nil
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	writeJSON(w, http.StatusOK, stateDTO{
		Queue:         s.buildQueueDTO(),
		History:       s.recentHistory(),
		DaemonRunning: s.daemonRunning(),
		SessionOut:    s.sessionOut(),
	})
}

// recentHistory reads the durable log store (foreground + background), newest
// first, within the retention window. A read error degrades to an empty list.
func (s *Server) recentHistory() []historyDTO {
	if s.deps.LogDir == "" {
		return []historyDTO{}
	}
	var since time.Time
	if s.deps.RetentionDays > 0 {
		since = time.Now().AddDate(0, 0, -s.deps.RetentionDays)
	}
	entries, err := logstore.Load(s.deps.LogDir, logstore.QueryOpts{Since: since, Limit: historyLimit})
	if err != nil {
		return []historyDTO{}
	}
	out := make([]historyDTO, 0, len(entries))
	for _, e := range entries {
		out = append(out, historyDTO{
			Time: e.Time.Format(time.RFC3339), URL: e.URL, Title: e.Title,
			Mode: e.Mode, Format: e.Format, RC: e.RC, Success: e.Success,
		})
	}
	return out
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
	cleanURL, err := validateDownloadURL(req.URL)
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
		// job_timeout and open_folder_on_done have no GUI control YET (the Cycle 5
		// settings view adds both), and the editor rewrites the whole document — so
		// without this a save would reset them to their zero values. Carry the
		// current persisted values through untouched. Both overrides go away in the
		// same commit as the controls.
		cur, _ := s.deps.Resolve(config.Partial{})
		settings.JobTimeout = cur.JobTimeout
		settings.OpenFolderOnDone = cur.OpenFolderOnDone
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
