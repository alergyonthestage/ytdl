package webui

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/alergyonthestage/ytdl/internal/update"
)

// Updater is the update capability, injected like every other engine seam so
// webui stays a front-end.
//
// A nil Updater means the capability is ABSENT: the state carries no update
// object, the three routes answer 404, and the page renders no update control at
// all — never a dead one (ux-principles.md §4).
type Updater interface {
	// Verdict is the cached round. false means none has ever completed, which is
	// "non verificato" and emphatically not "up to date".
	Verdict() (update.Verdict, bool)
	// Check runs one round now. The user is waiting on this one, so it may block;
	// it is the only update path that probes on request.
	Check(ctx context.Context) (update.Verdict, error)
	// Start launches the installer. force reinstalls everything, which is what a
	// retry after a failure needs.
	Start(force bool) error
	// Progress is how the running or last-finished installer went.
	Progress() update.Progress
	// Deps is what this machine actually has, in the shape built for surfaces that
	// SHOW it rather than the flattened one that gets COMPARED. The page needs
	// both: an empty version means "nobody recorded one", which is not the same
	// fact as the tool being absent.
	Deps() []update.Dependency
	// Enabled is the resolved update_check. It gates the automatic probe, never
	// the manual one and never what is displayed (ADR-0016 §6).
	Enabled() bool
}

// checkTimeout bounds a user-initiated check. It is longer than one probe because
// a round is up to three requests, and shorter than a person's patience.
const checkTimeout = 25 * time.Second

// updateDTO is the update object in /api/state.
type updateDTO struct {
	Enabled   bool       `json:"enabled"`
	CheckedAt *time.Time `json:"checkedAt,omitempty"` // absent = never checked
	Known     bool       `json:"known"`
	Available bool       `json:"available"`
	Busy      bool       `json:"busy"`

	Installed installedDTO `json:"installed"`
	Changes   []changeDTO  `json:"changes,omitempty"`
	Foreign   []string     `json:"foreign,omitempty"`
	// Missing names the dependencies that are not on this machine at all. It is
	// what lets the page tell "nobody recorded a version for this" from "this is
	// not here": Installed is the COMPARISON shape, so a copy we cannot vouch for
	// carries no version, and without this the page called an installed ffmpeg
	// "non installato" (V12).
	Missing []string `json:"missing,omitempty"`
	// Unattested: installed by us, but not the build the pin vouches for, because
	// that build was withdrawn upstream (ADR-0016 §15).
	Unattested []string `json:"unattested,omitempty"`
	// Unreadable: asked for a version and gave no usable answer, so it was not
	// compared. Distinct from Missing (not there) and from a version nobody ever
	// recorded — the page must not call any of the three by another's name
	// (ADR-0016 §16.5, finding `V20`).
	Unreadable []string `json:"unreadable,omitempty"`

	// Blocked is absent when the update can start. It carries the REASON and the
	// count, because an action that cannot work is disabled with a reason rather
	// than rendered live and failed (ux-principles.md §4).
	Blocked *blockedDTO `json:"blocked,omitempty"`

	// Run is the installer run the page must show WITHOUT having been the tab that
	// started it: one in flight, or one nobody was left to follow. Absent
	// otherwise — a finished run from a previous session is not news, and carrying
	// its log tail on every page load would be.
	Run *updateStatusDTO `json:"run,omitempty"`
}

type installedDTO struct {
	Ytdl   string `json:"ytdl,omitempty"`
	YtDlp  string `json:"ytDlp,omitempty"`
	FFmpeg string `json:"ffmpeg,omitempty"`
}

type changeDTO struct {
	Component string `json:"component"`
	From      string `json:"from"`
	To        string `json:"to"`
}

type blockedDTO struct {
	Reason  string `json:"reason"` // "queue" | "running"
	Pending int    `json:"pending,omitempty"`
}

// buildUpdateDTO assembles the update object, or nil when the capability is
// absent.
func (s *Server) buildUpdateDTO() *updateDTO {
	u := s.deps.Updater
	if u == nil {
		return nil
	}
	v, have := u.Verdict()
	// One Progress call for the whole object. It opens the run file and reads up
	// to 8 KB of update.log, and /api/state is on the page's load path — so Busy
	// and the blocked reason share the answer rather than each paying for it (V7).
	runState := u.Progress().State
	dto := &updateDTO{
		Enabled:   u.Enabled(),
		Known:     have && v.Known(),
		Available: have && v.Available(),
		Busy:      runState == update.StateRunning,
		Installed: installedDTO{
			Ytdl:   v.Installed.Ytdl,
			YtDlp:  v.Installed.YtDlp,
			FFmpeg: update.FFmpegVersion(v.Installed.FFmpeg),
		},
		Foreign:    v.Installed.Foreign,
		Unattested: v.Installed.Unattested,
		Unreadable: v.Installed.Unreadable,
		Missing:    missingNames(u.Deps()),
	}
	if have && !v.CheckedAt.IsZero() {
		at := v.CheckedAt
		dto.CheckedAt = &at
	}
	if have {
		for _, c := range v.Changes() {
			dto.Changes = append(dto.Changes, changeDTO{
				Component: c.Component,
				From:      displayVersion(c.Component, c.From),
				To:        displayVersion(c.Component, c.To),
			})
		}
	}
	dto.Blocked = s.updateBlocked(runState)
	if runState == update.StateRunning || runState == update.StateAbandoned {
		run := s.updateStatusDTO()
		dto.Run = &run
	}
	return dto
}

// missingNames lists the dependencies that are not on this machine at all —
// a different state from one whose version simply was never written down, and the
// only one the page may call "non installato".
func missingNames(deps []update.Dependency) []string {
	var out []string
	for _, d := range deps {
		if d.Missing() {
			out = append(out, d.Name)
		}
	}
	return out
}

// displayVersion shows ffmpeg by version rather than by build id: the build
// number means nothing to the reader, though it is what gets compared.
func displayVersion(component, value string) string {
	if component == update.ComponentFFmpeg {
		return update.FFmpegVersion(value)
	}
	return value
}

// updateBlocked reports why an update cannot START right now, or nil when it can.
//
// It gates the ACTION and never the news: a user with a full queue is still told
// there is an update, and told what to do about it. Emptiness gating the notice
// too would hide exactly the information that explains the disabled button
// (ADR-0016 §9).
//
// The run state is a parameter rather than something read here: reading it costs
// a file open and a log tail, and the caller on the page's load path already has
// the answer. Only StateRunning blocks — an abandoned record is a run nobody is
// left to finish, and it must not refuse the next one (V1).
func (s *Server) updateBlocked(runState string) *blockedDTO {
	if runState == update.StateRunning {
		return &blockedDTO{Reason: "running"}
	}
	if s.deps.Spool == nil {
		return nil
	}
	snap, err := s.deps.Spool.List()
	if err != nil {
		// We cannot prove the queue is empty, so we must not claim it is. Refusing
		// with the honest reason beats starting an update over a download we simply
		// failed to see.
		return &blockedDTO{Reason: "queue"}
	}
	if p, r, _, _ := snap.Counts(); p+r > 0 {
		return &blockedDTO{Reason: "queue", Pending: p + r}
	}
	return nil
}

// handleUpdateCheck runs one probe round on demand and answers with the fresh
// state.
//
// This is the one update path that probes while someone waits, and it works with
// update_check off: that key is consent for the machine to phone home BY ITSELF,
// not a restriction on the user's right to ask (ADR-0016 §6).
func (s *Server) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	u := s.deps.Updater
	if u == nil {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), checkTimeout)
	defer cancel()

	if _, err := u.Check(ctx); err != nil {
		// A round where a source stayed silent is not an error the user must act
		// on: the state below reports "non verificato", which is the honest answer
		// and the one the surface already knows how to show.
		writeJSON(w, http.StatusOK, s.buildUpdateDTO())
		return
	}
	writeJSON(w, http.StatusOK, s.buildUpdateDTO())
}

// handleUpdateStart launches the installer.
//
// The queue's emptiness is re-checked HERE, not only when the button was drawn,
// so a job enqueued between render and click is still refused.
func (s *Server) handleUpdateStart(w http.ResponseWriter, r *http.Request) {
	u := s.deps.Updater
	if u == nil {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var body struct {
		Force bool `json:"force"`
	}
	// An absent or empty body simply means force=false; only Retry sends one.
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<12)).Decode(&body)

	if b := s.updateBlocked(u.Progress().State); b != nil {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":   blockedMessage(b),
			"blocked": b,
		})
		return
	}
	if err := u.Start(body.Force); err != nil {
		if errors.Is(err, update.ErrAlreadyRunning) {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":   "un aggiornamento è già in corso",
				"blocked": &blockedDTO{Reason: "running"},
			})
			return
		}
		writeErr(w, http.StatusInternalServerError, "impossibile avviare l'aggiornamento: "+err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, s.updateStatusDTO())
}

// blockedMessage names the reason and the count. Never a number without what it
// counts (ux-principles.md §5).
func blockedMessage(b *blockedDTO) string {
	if b.Reason == "running" {
		return "un aggiornamento è già in corso"
	}
	switch b.Pending {
	case 0:
		return "non riesco a leggere la coda: l'aggiornamento parte a coda vuota"
	case 1:
		return "1 download in corso: l'aggiornamento parte a coda vuota"
	default:
		return strconv.Itoa(b.Pending) + " download in corso: l'aggiornamento parte a coda vuota"
	}
}

// handleUpdateStatus is what the update panel polls.
//
// It POLLS rather than listening on SSE, deliberately: the SSE connection dies at
// the handover by construction, and a transport that disappears exactly when the
// news matters is the wrong transport. Independent polls fail during the gap and
// succeed after it, which is precisely the signal the page needs (design §6.2).
func (s *Server) handleUpdateStatus(w http.ResponseWriter, r *http.Request) {
	u := s.deps.Updater
	if u == nil {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	writeJSON(w, http.StatusOK, s.updateStatusDTO())
}

// updateStatusDTO is one installer run as the page sees it.
type updateStatusDTO struct {
	State     string     `json:"state"`
	Changed   bool       `json:"changed"`
	StartedAt *time.Time `json:"startedAt,omitempty"`
	EndedAt   *time.Time `json:"endedAt,omitempty"`
	ExitCode  int        `json:"exitCode"`
	Version   string     `json:"version,omitempty"`
	LogTail   string     `json:"logTail,omitempty"`
}

func (s *Server) updateStatusDTO() updateStatusDTO {
	p := s.deps.Updater.Progress()
	dto := updateStatusDTO{
		State:    p.State,
		Changed:  p.Changed,
		ExitCode: p.ExitCode,
		Version:  p.Version,
		LogTail:  p.LogTail,
	}
	if !p.StartedAt.IsZero() {
		at := p.StartedAt
		dto.StartedAt = &at
	}
	if !p.EndedAt.IsZero() {
		at := p.EndedAt
		dto.EndedAt = &at
	}
	return dto
}
