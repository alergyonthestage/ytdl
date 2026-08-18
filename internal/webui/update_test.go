package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alergyonthestage/ytdl/internal/config"
	"github.com/alergyonthestage/ytdl/internal/queue"
	"github.com/alergyonthestage/ytdl/internal/update"
)

// fakeUpdater stands in for the real capability, so these tests are about the
// SEAM — what the routes answer and what the state carries — rather than about
// probing or installing.
type fakeUpdater struct {
	verdict  update.Verdict
	have     bool
	enabled  bool
	progress update.Progress

	checkErr   error
	checked    int
	startErr   error
	started    int
	lastForce  bool
	checkDelay time.Duration
}

func (f *fakeUpdater) Verdict() (update.Verdict, bool) { return f.verdict, f.have }

func (f *fakeUpdater) Check(ctx context.Context) (update.Verdict, error) {
	f.checked++
	if f.checkDelay > 0 {
		select {
		case <-time.After(f.checkDelay):
		case <-ctx.Done():
			return f.verdict, ctx.Err()
		}
	}
	return f.verdict, f.checkErr
}

func (f *fakeUpdater) Start(force bool) error {
	f.started++
	f.lastForce = force
	if f.startErr != nil {
		return f.startErr
	}
	f.progress = update.Progress{Run: update.Run{State: update.StateRunning}}
	return nil
}

func (f *fakeUpdater) Progress() update.Progress { return f.progress }
func (f *fakeUpdater) Enabled() bool             { return f.enabled }

const testBuild = "1785863997_9.0"

func upToDateVerdict() update.Verdict {
	return update.Verdict{
		CheckedAt:  time.Date(2026, 8, 12, 14, 2, 11, 0, time.UTC),
		LatestYtdl: "v2.1.0",
		Pin:        update.Pin{YtDlp: "2026.07.04", FFmpeg: testBuild},
		Installed: update.Installed{
			Ytdl: "v2.1.0", YtDlp: "2026.07.04", FFmpeg: testBuild,
		},
	}
}

// serverWithUpdater builds the harness with the update capability wired in.
func serverWithUpdater(t *testing.T, u Updater) (*Server, *queue.Spool) {
	t.Helper()
	srv, sp, _ := newTestServer(t)
	srv.deps.Updater = u
	return srv, sp
}

func call(t *testing.T, srv *Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	r.Header.Set("Content-Type", "application/json")
	// httptest.NewRequest defaults Host to example.com, which the DNS-rebinding
	// guard correctly refuses.
	r.Host = "127.0.0.1:8765"
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	return w
}

func decode(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("body is not a JSON object: %v\n%s", err, w.Body.String())
	}
	return m
}

// A capability that is not there renders no control at all — never a dead one
// (ux-principles.md §4). Absent from the state AND absent from the routes, so
// nothing can call what nothing can serve.
func TestNoUpdaterMeansNoSurfaceAtAll(t *testing.T) {
	srv, _, _ := newTestServer(t)

	state := decode(t, call(t, srv, http.MethodGet, "/api/state", ""))
	if _, present := state["update"]; present {
		t.Errorf("state carries an update object with no Updater injected: %v", state["update"])
	}

	for _, path := range []string{"/api/update", "/api/update/check", "/api/update/status"} {
		method := http.MethodPost
		if strings.HasSuffix(path, "/status") {
			method = http.MethodGet
		}
		if w := call(t, srv, method, path, ""); w.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404", method, path, w.Code)
		}
	}
}

func TestStateCarriesTheUpdateObject(t *testing.T) {
	v := upToDateVerdict()
	v.LatestYtdl = "v2.2.0"
	v.Pin.YtDlp = "2026.08.02"
	srv, _ := serverWithUpdater(t, &fakeUpdater{verdict: v, have: true, enabled: true})

	state := decode(t, call(t, srv, http.MethodGet, "/api/state", ""))
	u, ok := state["update"].(map[string]any)
	if !ok {
		t.Fatalf("state has no update object: %v", state)
	}
	if u["known"] != true || u["available"] != true || u["enabled"] != true {
		t.Errorf("update = %v", u)
	}
	if u["checkedAt"] == nil {
		t.Error("checkedAt is absent though a round completed")
	}
	changes, _ := u["changes"].([]any)
	if len(changes) != 2 {
		t.Fatalf("changes = %v, want ytdl and yt-dlp", changes)
	}
	first, _ := changes[0].(map[string]any)
	if first["component"] != "ytdl" || first["to"] != "v2.2.0" {
		t.Errorf("first change = %v, want ytdl → v2.2.0", first)
	}
	installed, _ := u["installed"].(map[string]any)
	// ffmpeg is compared by build id but SHOWN by version: the build number means
	// nothing to the reader.
	if installed["ffmpeg"] != "9.0" {
		t.Errorf("installed.ffmpeg = %v, want the readable 9.0", installed["ffmpeg"])
	}
}

// Never checked is not up to date, and the state has to keep them apart.
func TestStateWithNoVerdictClaimsNothing(t *testing.T) {
	srv, _ := serverWithUpdater(t, &fakeUpdater{have: false, enabled: true})

	state := decode(t, call(t, srv, http.MethodGet, "/api/state", ""))
	u := state["update"].(map[string]any)
	if u["known"] != false || u["available"] != false {
		t.Errorf("update = %v, want neither known nor available", u)
	}
	if _, present := u["checkedAt"]; present {
		t.Error("checkedAt is present though nothing was ever checked")
	}
}

// Emptiness gates the ACTION, never the news. A user with a full queue is told
// there is an update AND told what to do about it (ADR-0016 §9).
func TestQueueBlocksTheActionButNotTheNotice(t *testing.T) {
	v := upToDateVerdict()
	v.LatestYtdl = "v2.2.0"
	srv, sp := serverWithUpdater(t, &fakeUpdater{verdict: v, have: true, enabled: true})

	for i := 0; i < 2; i++ {
		if _, err := sp.Enqueue(queue.Job{URL: "https://youtu.be/X", Settings: config.Defaults()}); err != nil {
			t.Fatal(err)
		}
	}

	state := decode(t, call(t, srv, http.MethodGet, "/api/state", ""))
	u := state["update"].(map[string]any)

	// The news survives.
	if u["available"] != true {
		t.Error("a non-empty queue hid the fact that an update exists")
	}
	if len(u["changes"].([]any)) == 0 {
		t.Error("a non-empty queue hid what would change")
	}
	// The action does not, and says why, with the count.
	blocked, ok := u["blocked"].(map[string]any)
	if !ok {
		t.Fatalf("no blocked reason with a full queue: %v", u)
	}
	if blocked["reason"] != "queue" {
		t.Errorf("reason = %v, want queue", blocked["reason"])
	}
	if blocked["pending"] != float64(2) {
		t.Errorf("pending = %v, want 2", blocked["pending"])
	}
}

// The emptiness is re-checked at the CLICK, not only when the button was drawn,
// so a job enqueued in between is still refused.
func TestStartRefusesANonEmptyQueueServerSide(t *testing.T) {
	f := &fakeUpdater{verdict: upToDateVerdict(), have: true, enabled: true}
	srv, sp := serverWithUpdater(t, f)

	if _, err := sp.Enqueue(queue.Job{URL: "https://youtu.be/X", Settings: config.Defaults()}); err != nil {
		t.Fatal(err)
	}
	w := call(t, srv, http.MethodPost, "/api/update", "")
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", w.Code)
	}
	if f.started != 0 {
		t.Error("the installer was launched over a non-empty queue")
	}
	body := decode(t, w)
	msg, _ := body["error"].(string)
	// The refusal carries its reason and its count, not just a status code.
	if !strings.Contains(msg, "coda vuota") || !strings.Contains(msg, "1 download") {
		t.Errorf("error = %q, want it to name the reason and the count", msg)
	}
}

func TestStartLaunchesOnAnEmptyQueue(t *testing.T) {
	f := &fakeUpdater{verdict: upToDateVerdict(), have: true, enabled: true}
	srv, _ := serverWithUpdater(t, f)

	w := call(t, srv, http.MethodPost, "/api/update", "")
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", w.Code, w.Body.String())
	}
	if f.started != 1 {
		t.Errorf("Start called %d times, want 1", f.started)
	}
	if f.lastForce {
		t.Error("force was set without being asked for")
	}
	if got := decode(t, w)["state"]; got != update.StateRunning {
		t.Errorf("state = %v, want %q", got, update.StateRunning)
	}
}

// Retry after a failure reinstalls everything, which is what --force is for.
func TestStartHonoursForce(t *testing.T) {
	f := &fakeUpdater{verdict: upToDateVerdict(), have: true, enabled: true}
	srv, _ := serverWithUpdater(t, f)

	if w := call(t, srv, http.MethodPost, "/api/update", `{"force":true}`); w.Code != http.StatusAccepted {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if !f.lastForce {
		t.Error("force was not passed through")
	}
}

func TestStartRefusesWhenOneIsAlreadyRunning(t *testing.T) {
	f := &fakeUpdater{
		verdict:  upToDateVerdict(),
		have:     true,
		enabled:  true,
		progress: update.Progress{Run: update.Run{State: update.StateRunning}},
	}
	srv, _ := serverWithUpdater(t, f)

	w := call(t, srv, http.MethodPost, "/api/update", "")
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", w.Code)
	}
	if f.started != 0 {
		t.Error("a second installer was launched")
	}
	if blocked, _ := decode(t, w)["blocked"].(map[string]any); blocked["reason"] != "running" {
		t.Errorf("reason = %v, want running", blocked["reason"])
	}
}

// Consent is about the machine phoning home on its own, not about the user's
// right to ask: the manual check works with update_check off (ADR-0016 §6).
func TestManualCheckWorksWithTheAutomaticCheckOff(t *testing.T) {
	f := &fakeUpdater{verdict: upToDateVerdict(), have: true, enabled: false}
	srv, _ := serverWithUpdater(t, f)

	w := call(t, srv, http.MethodPost, "/api/update/check", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if f.checked != 1 {
		t.Errorf("Check called %d times, want 1", f.checked)
	}
	if decode(t, w)["enabled"] != false {
		t.Error("the state stopped reporting that the automatic check is off")
	}
}

// A probe that did not answer is not an error the user must act on: the state
// says "non verificato", which the surface already knows how to show.
func TestCheckThatFailsStillAnswersWithTheState(t *testing.T) {
	f := &fakeUpdater{have: false, enabled: true, checkErr: context.DeadlineExceeded}
	srv, _ := serverWithUpdater(t, f)

	w := call(t, srv, http.MethodPost, "/api/update/check", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with an honest state rather than an error", w.Code)
	}
	if decode(t, w)["known"] != false {
		t.Error("a failed round reported itself as known")
	}
}

func TestUpdateStatusReportsTheRun(t *testing.T) {
	started := time.Date(2026, 8, 13, 10, 0, 0, 0, time.UTC)
	f := &fakeUpdater{
		enabled: true,
		progress: update.Progress{
			Run: update.Run{
				State: update.StateFailed, StartedAt: started, EndedAt: started.Add(time.Minute),
				ExitCode: 3,
			},
			LogTail: "something went wrong",
		},
	}
	srv, _ := serverWithUpdater(t, f)

	body := decode(t, call(t, srv, http.MethodGet, "/api/update/status", ""))
	if body["state"] != update.StateFailed {
		t.Errorf("state = %v", body["state"])
	}
	if body["exitCode"] != float64(3) {
		t.Errorf("exitCode = %v, want the installer's own 3", body["exitCode"])
	}
	// The log is what a failure is explained with; it is never summarised away.
	if body["logTail"] != "something went wrong" {
		t.Errorf("logTail = %v", body["logTail"])
	}
}

// Whether the ytdl binary changed is what tells the page a reload is coming.
func TestUpdateStatusCarriesWhetherARestartIsDue(t *testing.T) {
	for _, changed := range []bool{true, false} {
		f := &fakeUpdater{
			enabled:  true,
			progress: update.Progress{Run: update.Run{State: update.StateDone, Changed: changed}},
		}
		srv, _ := serverWithUpdater(t, f)
		if got := decode(t, call(t, srv, http.MethodGet, "/api/update/status", ""))["changed"]; got != changed {
			t.Errorf("changed = %v, want %v", got, changed)
		}
	}
}

func TestUpdateRoutesRejectTheWrongMethod(t *testing.T) {
	srv, _ := serverWithUpdater(t, &fakeUpdater{enabled: true})
	cases := []struct{ method, path string }{
		{http.MethodGet, "/api/update"},
		{http.MethodGet, "/api/update/check"},
		{http.MethodPost, "/api/update/status"},
	}
	for _, tc := range cases {
		if w := call(t, srv, tc.method, tc.path, ""); w.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s = %d, want 405", tc.method, tc.path, w.Code)
		}
	}
}

// A foreign dependency is a different fact from being out of date, and the state
// carries it separately so the page can say so separately.
func TestStateCarriesForeignDependencies(t *testing.T) {
	v := upToDateVerdict()
	v.Installed.Foreign = []string{"yt-dlp"}
	srv, _ := serverWithUpdater(t, &fakeUpdater{verdict: v, have: true, enabled: true})

	u := decode(t, call(t, srv, http.MethodGet, "/api/state", ""))["update"].(map[string]any)
	foreign, _ := u["foreign"].([]any)
	if len(foreign) != 1 || foreign[0] != "yt-dlp" {
		t.Errorf("foreign = %v", foreign)
	}
	// It is NOT an update: nothing is out of date, the pin is simply not in force.
	if u["available"] != false {
		t.Error("a foreign dependency was reported as an available update")
	}
}

// A run record left at "running" by a process that died before it could finish it
// must NOT block the update path.
//
// This was V1's blocking half: the record blocked, the banner hid, the action
// slot emptied and POST answered 409 — permanently, and with no reason the user
// could see or act on (ux-principles.md §4). The engine reports such a record as
// abandoned, and the seam must treat that as "no run in flight".
func TestAnAbandonedRunDoesNotBlockTheUpdatePath(t *testing.T) {
	v := upToDateVerdict()
	v.LatestYtdl = "v2.2.0"
	f := &fakeUpdater{
		verdict: v, have: true, enabled: true,
		progress: update.Progress{
			Run:     update.Run{State: update.StateAbandoned, ExitCode: 0},
			LogTail: "installing ffmpeg…",
		},
	}
	srv, _ := serverWithUpdater(t, f)

	state := decode(t, call(t, srv, http.MethodGet, "/api/state", ""))
	u := state["update"].(map[string]any)
	if u["busy"] != false {
		t.Error("an abandoned run still reports the machine as busy")
	}
	if b, ok := u["blocked"]; ok {
		t.Errorf("an abandoned run still blocks the action: %v", b)
	}
	if u["available"] != true {
		t.Error("the news was withheld as well")
	}

	// And the action actually works, rather than merely looking enabled.
	w := call(t, srv, http.MethodPost, "/api/update", "")
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: an abandoned run refused a new one", w.Code)
	}
	if f.started != 1 {
		t.Errorf("the installer was launched %d times, want 1", f.started)
	}
}

// The status endpoint carries the abandoned state and the log with it, because
// what the page can honestly say is "I could not follow this to the end — here is
// the output" (design §7.3).
func TestUpdateStatusCarriesAnAbandonedRun(t *testing.T) {
	f := &fakeUpdater{
		verdict: upToDateVerdict(), have: true, enabled: true,
		progress: update.Progress{
			Run:     update.Run{State: update.StateAbandoned},
			LogTail: "got as far as ffmpeg",
		},
	}
	srv, _ := serverWithUpdater(t, f)

	body := decode(t, call(t, srv, http.MethodGet, "/api/update/status", ""))
	if body["state"] != update.StateAbandoned {
		t.Errorf("state = %v, want %q", body["state"], update.StateAbandoned)
	}
	if body["logTail"] != "got as far as ffmpeg" {
		t.Errorf("logTail = %v; the log is the only honest answer here", body["logTail"])
	}
}
