package update

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/alergyonthestage/ytdl/internal/buildinfo"
)

const (
	// DefaultSlug and DefaultBranch are the repository ytdl probes and updates
	// from. $YTDL_REPO / $YTDL_BRANCH override them exactly as run.Update() reads
	// them, so a fork checks and installs from its own repository rather than
	// upstream's.
	DefaultSlug   = "alergyonthestage/ytdl"
	DefaultBranch = "main"

	// YtDlpSlug is upstream's repository. Unlike ytdl's own slug it is deliberately
	// NOT configurable: a fork of ytdl still drives the real yt-dlp.
	YtDlpSlug = "yt-dlp/yt-dlp"

	// DepsFile is the pin, at the repository root beside install.sh and fetched
	// from the same raw.githubusercontent.com path the installer comes from.
	DepsFile = "deps.conf"

	// LatestPolicy is the deps.conf value meaning "whatever upstream's newest
	// release is". It is resolved to a concrete tag before it leaves this package,
	// so no caller ever has to know which policy is in force (design §2).
	LatestPolicy = "latest"

	// ProbeTimeout bounds one HTTP exchange. A probe is never on a path a user is
	// waiting on, but it must not hold a daemon's goroutine open indefinitely
	// either.
	ProbeTimeout = 10 * time.Second

	// maxDepsBytes caps the pin download. deps.conf is a handful of lines; a
	// response larger than this is not the file this is asking for.
	maxDepsBytes = 64 * 1024
)

// The outbound hosts this package knows, as package vars so tests can point them
// at an httptest.Server. They are the only two, and nothing else in ytdl makes an
// outbound request — install.sh remains the single thing that downloads and
// verifies (ADR-0005).
var (
	githubBase = "https://github.com"
	rawBase    = "https://raw.githubusercontent.com"
)

// Slug is the repository ytdl checks itself against.
func Slug() string { return envOr("YTDL_REPO", DefaultSlug) }

// Branch is the branch deps.conf and install.sh are fetched from.
func Branch() string { return envOr("YTDL_BRANCH", DefaultBranch) }

// UserAgent identifies ytdl to github.com. An explicit one is politeness, and it
// makes the traffic attributable if it ever needs to be.
func UserAgent() string { return "ytdl/" + buildinfo.Version }

// LatestTag returns a repository's newest release tag by reading the redirect
// github.com answers to releases/latest. It deliberately does NOT follow it: the
// redirect target IS the answer, which is why this needs no API, no token and no
// rate-limit budget — api.github.com's 60 unauthenticated requests per hour are
// counted per IP, which is fragile behind shared NAT and buys nothing this needs
// (ADR-0016 §1).
func LatestTag(ctx context.Context, c *http.Client, slug string) (string, error) {
	if slug == "" {
		return "", errors.New("update: empty repository slug")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, githubBase+"/"+slug+"/releases/latest", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", UserAgent())

	resp, err := doNoRedirect(c, req)
	if err != nil {
		return "", fmt.Errorf("update: probing %s: %w", slug, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 300 || resp.StatusCode >= 400 {
		return "", fmt.Errorf("update: %s answered %s, not a redirect", slug, resp.Status)
	}
	loc := resp.Header.Get("Location")
	if loc == "" {
		return "", fmt.Errorf("update: %s redirected without a Location", slug)
	}
	return tagFromLocation(loc)
}

// tagFromLocation extracts the tag from the Location github.com answers with.
// A Location that is not a .../releases/tag/<tag> URL is an ERROR rather than a
// guess: an unexpected shape means the answer is not the one this probe is built
// on, and inventing a version out of it is exactly how a surface ends up stating
// something untrue (ADR-0016 §8).
func tagFromLocation(loc string) (string, error) {
	u, err := url.Parse(loc)
	if err != nil {
		return "", fmt.Errorf("update: unreadable Location %q: %w", loc, err)
	}
	const marker = "/releases/tag/"
	i := strings.Index(u.Path, marker)
	if i < 0 {
		return "", fmt.Errorf("update: Location %q is not a release-tag URL", loc)
	}
	tag := u.Path[i+len(marker):]
	if !validVersion(tag) {
		return "", fmt.Errorf("update: Location %q carries no usable tag", loc)
	}
	return tag, nil
}

// FetchPin reads deps.conf from the branch the installer comes from and returns
// it RESOLVED: a yt_dlp_version of "latest" is turned into a concrete tag by the
// same redirect read LatestTag uses, so no caller has to know which policy is in
// force and nothing downstream ever compares against a placeholder.
//
// It fails closed. An unreachable, unparseable or incomplete deps.conf is an
// error — never a fallback to "latest", because a silent fallback is
// indistinguishable from the policy currently BEING "latest", and the day the
// maintainer pins a rollback that equivalence would quietly ignore it (design §2).
func FetchPin(ctx context.Context, c *http.Client, slug, branch string) (Pin, error) {
	if slug == "" || branch == "" {
		return Pin{}, errors.New("update: empty repository slug or branch")
	}
	kv, err := fetchDeps(ctx, c, slug, branch)
	if err != nil {
		return Pin{}, err
	}

	pin := Pin{YtDlp: kv[depsYtDlpVersion], FFmpeg: kv[depsFFmpegBuild]}
	if pin.YtDlp == "" {
		return Pin{}, fmt.Errorf("update: %s declares no %s", DepsFile, depsYtDlpVersion)
	}
	if !validVersion(pin.FFmpeg) {
		return Pin{}, fmt.Errorf("update: %s declares no usable %s", DepsFile, depsFFmpegBuild)
	}

	if pin.YtDlp == LatestPolicy {
		tag, err := LatestTag(ctx, c, YtDlpSlug)
		if err != nil {
			return Pin{}, fmt.Errorf("update: resolving the %q policy: %w", LatestPolicy, err)
		}
		pin.YtDlp = tag
	}
	if !validVersion(pin.YtDlp) {
		return Pin{}, fmt.Errorf("update: %s declares no usable %s", DepsFile, depsYtDlpVersion)
	}
	return pin, nil
}

// deps.conf's keys. The Go probe reads only the first two; the rest are the
// checksums install.sh needs.
//
// The probe is deliberately TOLERANT of a key it does not recognise, while
// install.sh rejects one. The asymmetry is not an inconsistency — the two read
// the file under different conditions. install.sh is fetched from the same commit
// as deps.conf, so a key it does not know is genuinely a typo and aborting costs
// nothing. A deployed ytdl binary, on the other hand, is arbitrarily older than
// the deps.conf it reads: if a key added later made the pin unreadable, every
// existing installation would fall to "non verificato" and stop seeing the very
// update that would teach it the new key. That is the exact opposite of the
// property the pin exists for — one commit reaching every installation within a
// day — so the probe fails only on what it actually needs and cannot get.
const (
	depsYtDlpVersion = "yt_dlp_version"
	depsFFmpegBuild  = "ffmpeg_build"
)

var depsKeys = map[string]bool{
	depsYtDlpVersion:              true,
	depsFFmpegBuild:               true,
	"ffmpeg_sha256_arm64_ffmpeg":  true,
	"ffmpeg_sha256_arm64_ffprobe": true,
	"ffmpeg_sha256_amd64_ffmpeg":  true,
	"ffmpeg_sha256_amd64_ffprobe": true,
}

// fetchDeps downloads and parses deps.conf.
func fetchDeps(ctx context.Context, c *http.Client, slug, branch string) (map[string]string, error) {
	u := rawBase + "/" + slug + "/" + branch + "/" + DepsFile
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", UserAgent())

	resp, err := clientOrDefault(c).Do(req)
	if err != nil {
		return nil, fmt.Errorf("update: fetching %s: %w", DepsFile, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("update: fetching %s: %s", DepsFile, resp.Status)
	}
	kv, err := parseKeyValue(io.LimitReader(resp.Body, maxDepsBytes), depsKeys, false)
	if err != nil {
		return nil, fmt.Errorf("update: %s: %w", DepsFile, err)
	}
	return kv, nil
}

// doNoRedirect performs req with redirects DISABLED, because the redirect target
// is the answer rather than something to follow. The client is COPIED rather than
// mutated: it belongs to the caller, and quietly changing its CheckRedirect would
// stop it following redirects everywhere else it is used.
func doNoRedirect(c *http.Client, req *http.Request) (*http.Response, error) {
	noRedirect := *clientOrDefault(c)
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return noRedirect.Do(req)
}

// clientOrDefault supplies a bounded client when the caller passes none.
func clientOrDefault(c *http.Client) *http.Client {
	if c != nil {
		return c
	}
	return &http.Client{Timeout: ProbeTimeout}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
