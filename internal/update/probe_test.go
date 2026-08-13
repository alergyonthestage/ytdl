package update

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// This file establishes the repository's first outbound-HTTP test pattern
// (design §3.2). The rules it sets, for anything that probes later:
//
//   - no network. One httptest.Server stands in for BOTH hosts this package
//     knows, and the package's base URLs are pointed at it for the test's
//     lifetime and restored afterwards.
//   - every request is counted, so "this path must not probe" is an assertion
//     about zero requests rather than about a log line.
//   - the failure cases are the point. A probe that works is one test; a probe
//     that guesses when the answer is malformed is the bug class this cycle is
//     built to avoid, so most of what follows is malformed answers.

// hub is a stand-in for github.com and raw.githubusercontent.com at once. They
// differ only by path in what this package asks of them, so one server serving
// both shapes exercises the real URL construction.
type hub struct {
	srv *httptest.Server

	tags map[string]string // slug -> tag served by /<slug>/releases/latest
	deps string            // deps.conf body; "" serves a 404

	// location, when set, is served verbatim instead of a well-formed tag URL,
	// and status overrides the 302.
	location string
	status   int

	hits atomic.Int64
}

func newHub(t *testing.T) *hub {
	t.Helper()
	h := &hub{tags: map[string]string{}}
	h.srv = httptest.NewServer(http.HandlerFunc(h.serve))
	t.Cleanup(h.srv.Close)

	oldGitHub, oldRaw := githubBase, rawBase
	githubBase, rawBase = h.srv.URL, h.srv.URL
	t.Cleanup(func() { githubBase, rawBase = oldGitHub, oldRaw })
	return h
}

func (h *hub) serve(w http.ResponseWriter, r *http.Request) {
	h.hits.Add(1)
	path := strings.TrimPrefix(r.URL.Path, "/")

	if slug, ok := strings.CutSuffix(path, "/releases/latest"); ok {
		status := h.status
		if status == 0 {
			status = http.StatusFound
		}
		loc := h.location
		if loc == "" {
			tag, known := h.tags[slug]
			if !known {
				http.NotFound(w, r)
				return
			}
			loc = fmt.Sprintf("%s/%s/releases/tag/%s", h.srv.URL, slug, tag)
		}
		if loc != "-" { // "-" means: redirect with no Location at all
			w.Header().Set("Location", loc)
		}
		w.WriteHeader(status)
		return
	}

	if strings.HasSuffix(path, "/"+DepsFile) {
		if h.deps == "" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, h.deps)
		return
	}
	http.NotFound(w, r)
}

// validDeps is a well-formed pin with an explicit yt-dlp tag.
const validDeps = `# What ytdl requires.
yt_dlp_version              = 2026.07.04
ffmpeg_build                = 1785863997_9.0
ffmpeg_sha256_arm64_ffmpeg  = aaaa
ffmpeg_sha256_arm64_ffprobe = bbbb
ffmpeg_sha256_amd64_ffmpeg  = cccc
ffmpeg_sha256_amd64_ffprobe = dddd
`

func ctxT(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestLatestTagReadsTheRedirect(t *testing.T) {
	h := newHub(t)
	h.tags["alergyonthestage/ytdl"] = "v2.2.0"

	got, err := LatestTag(ctxT(t), nil, "alergyonthestage/ytdl")
	if err != nil {
		t.Fatalf("LatestTag: %v", err)
	}
	if got != "v2.2.0" {
		t.Errorf("tag = %q, want v2.2.0", got)
	}
	// The redirect target IS the answer: following it would be a second request
	// for a page nobody reads.
	if n := h.hits.Load(); n != 1 {
		t.Errorf("made %d requests, want exactly 1 (the redirect must not be followed)", n)
	}
}

// A malformed answer must be an error, never a guess: everything downstream
// compares this string, caches it and prints it.
func TestLatestTagRefusesEveryMalformedAnswer(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		location string
	}{
		{"200 instead of a redirect", http.StatusOK, "ignored"},
		{"404", http.StatusNotFound, "ignored"},
		{"500", http.StatusInternalServerError, "ignored"},
		{"redirect without a Location", http.StatusFound, "-"},
		{"Location that is not a tag URL", http.StatusFound, "https://example.invalid/somewhere"},
		{"tag URL with an empty tag", http.StatusFound, "https://example.invalid/o/r/releases/tag/"},
		{"tag with a path separator", http.StatusFound, "https://example.invalid/o/r/releases/tag/a/b"},
		{"tag with a control character", http.StatusFound, "https://example.invalid/o/r/releases/tag/v1%0A0"},
		{"tag with a shell metacharacter", http.StatusFound, "https://example.invalid/o/r/releases/tag/v1;rm"},
		{"absurdly long tag", http.StatusFound, "https://example.invalid/o/r/releases/tag/" + strings.Repeat("v", 500)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHub(t)
			h.status, h.location = tc.status, tc.location
			h.tags["o/r"] = "v1.0.0"

			got, err := LatestTag(ctxT(t), nil, "o/r")
			if err == nil {
				t.Fatalf("LatestTag returned %q, want an error", got)
			}
			if got != "" {
				t.Errorf("LatestTag returned %q alongside its error; a failed probe answers nothing", got)
			}
		})
	}
}

func TestLatestTagUnreachable(t *testing.T) {
	h := newHub(t)
	h.srv.Close() // nothing is listening now

	if _, err := LatestTag(ctxT(t), nil, "o/r"); err == nil {
		t.Fatal("LatestTag on a dead server: want an error")
	}
}

func TestLatestTagHonoursContextCancellation(t *testing.T) {
	newHub(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := LatestTag(ctx, nil, "o/r")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestFetchPinReadsAnExplicitTag(t *testing.T) {
	h := newHub(t)
	h.deps = validDeps

	pin, err := FetchPin(ctxT(t), nil, "o/r", "main")
	if err != nil {
		t.Fatalf("FetchPin: %v", err)
	}
	if pin.YtDlp != "2026.07.04" || pin.FFmpeg != "1785863997_9.0" {
		t.Errorf("pin = %+v", pin)
	}
	// An explicit tag needs no redirect read: the file already answered.
	if n := h.hits.Load(); n != 1 {
		t.Errorf("made %d requests, want 1", n)
	}
}

// "latest" is a policy, not a version. It must be resolved to a concrete tag
// before it leaves the package, so nothing downstream ever compares a
// placeholder (design §2).
func TestFetchPinResolvesTheLatestPolicy(t *testing.T) {
	h := newHub(t)
	h.deps = strings.Replace(validDeps, "2026.07.04", LatestPolicy, 1)
	h.tags[YtDlpSlug] = "2026.08.02"

	pin, err := FetchPin(ctxT(t), nil, "o/r", "main")
	if err != nil {
		t.Fatalf("FetchPin: %v", err)
	}
	if pin.YtDlp != "2026.08.02" {
		t.Errorf("YtDlp = %q, want the resolved tag 2026.08.02", pin.YtDlp)
	}
	if pin.YtDlp == LatestPolicy {
		t.Error("the policy placeholder survived resolution")
	}
}

// The whole point of failing closed: when the policy cannot be resolved, there is
// no pin — never a silent fallback to whatever upstream's newest happens to be,
// because that is indistinguishable from the policy currently BEING latest.
func TestFetchPinFailsClosedWhenLatestCannotBeResolved(t *testing.T) {
	h := newHub(t)
	h.deps = strings.Replace(validDeps, "2026.07.04", LatestPolicy, 1)
	// yt-dlp's slug is deliberately absent from h.tags, so its probe 404s.

	pin, err := FetchPin(ctxT(t), nil, "o/r", "main")
	if err == nil {
		t.Fatalf("FetchPin returned %+v, want an error", pin)
	}
	if pin.YtDlp != "" {
		t.Errorf("YtDlp = %q; a failed resolution must yield no pin at all", pin.YtDlp)
	}
}

func TestFetchPinRejectsBadFiles(t *testing.T) {
	cases := []struct {
		name string
		deps string
	}{
		{"missing yt_dlp_version", "ffmpeg_build = 1785863997_9.0\n"},
		{"missing ffmpeg_build", "yt_dlp_version = 2026.07.04\n"},
		{"unknown key", validDeps + "ytdl_version = v9.9.9\n"},
		{"malformed line", validDeps + "this line has no equals sign\n"},
		{"empty key", validDeps + "= orphan\n"},
		{"empty file", "#only a comment\n"},
		{"garbage version", "yt_dlp_version = ../../etc/passwd\nffmpeg_build = 1785863997_9.0\n"},
		{"garbage build", "yt_dlp_version = 2026.07.04\nffmpeg_build = a b c\n"},
		{"html error page", "<!DOCTYPE html><html><body>404</body></html>\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHub(t)
			h.deps = tc.deps

			if pin, err := FetchPin(ctxT(t), nil, "o/r", "main"); err == nil {
				t.Fatalf("FetchPin returned %+v, want an error", pin)
			}
		})
	}
}

func TestFetchPinUnreachable(t *testing.T) {
	newHub(t) // deps stays empty, so the server 404s the file

	if _, err := FetchPin(ctxT(t), nil, "o/r", "main"); err == nil {
		t.Fatal("FetchPin on a missing deps.conf: want an error")
	}
}

// deps.conf keeps its comments and its aligned spacing; the parser must not care.
func TestFetchPinToleratesCommentsAndSpacing(t *testing.T) {
	h := newHub(t)
	h.deps = "\n   # a comment\n\n\tyt_dlp_version\t=\t2026.07.04   \n" +
		"ffmpeg_build=1785863997_9.0\n\n# trailing comment\n"

	pin, err := FetchPin(ctxT(t), nil, "o/r", "main")
	if err != nil {
		t.Fatalf("FetchPin: %v", err)
	}
	if pin.YtDlp != "2026.07.04" || pin.FFmpeg != "1785863997_9.0" {
		t.Errorf("pin = %+v", pin)
	}
}

// The probes must build their URLs from slug and branch, so a fork checks itself
// rather than upstream (ADR-0016 §1).
func TestProbesUseTheConfiguredRepository(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if strings.HasSuffix(r.URL.Path, DepsFile) {
			fmt.Fprint(w, validDeps)
			return
		}
		w.Header().Set("Location", "https://x.invalid/releases/tag/v3.0.0")
		w.WriteHeader(http.StatusFound)
	}))
	defer srv.Close()

	oldGitHub, oldRaw := githubBase, rawBase
	githubBase, rawBase = srv.URL, srv.URL
	defer func() { githubBase, rawBase = oldGitHub, oldRaw }()

	if _, err := LatestTag(ctxT(t), nil, "fork/ytdl"); err != nil {
		t.Fatalf("LatestTag: %v", err)
	}
	if _, err := FetchPin(ctxT(t), nil, "fork/ytdl", "release"); err != nil {
		t.Fatalf("FetchPin: %v", err)
	}
	want := []string{"/fork/ytdl/releases/latest", "/fork/ytdl/release/deps.conf"}
	if len(paths) != len(want) {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Errorf("paths[%d] = %q, want %q", i, paths[i], want[i])
		}
	}
}

func TestSlugAndBranchHonourTheEnvironment(t *testing.T) {
	if got := Slug(); got != DefaultSlug {
		t.Errorf("Slug() = %q, want %q", got, DefaultSlug)
	}
	if got := Branch(); got != DefaultBranch {
		t.Errorf("Branch() = %q, want %q", got, DefaultBranch)
	}
	t.Setenv("YTDL_REPO", "fork/ytdl")
	t.Setenv("YTDL_BRANCH", "next")
	if got := Slug(); got != "fork/ytdl" {
		t.Errorf("Slug() = %q, want fork/ytdl", got)
	}
	if got := Branch(); got != "next" {
		t.Errorf("Branch() = %q, want next", got)
	}
}

// The caller's client must come back unchanged: silently disabling redirects on
// a shared client would break every other request made through it.
func TestDoNoRedirectDoesNotMutateTheCallersClient(t *testing.T) {
	h := newHub(t)
	h.tags["o/r"] = "v1.0.0"

	c := &http.Client{Timeout: 5 * time.Second}
	if _, err := LatestTag(ctxT(t), c, "o/r"); err != nil {
		t.Fatalf("LatestTag: %v", err)
	}
	if c.CheckRedirect != nil {
		t.Error("the caller's client had its CheckRedirect replaced")
	}
}
