package update

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/alergyonthestage/ytdl/internal/buildinfo"
)

// RefreshBudget bounds a whole check round: two redirect reads and one small GET,
// with room for one of them to be slow before the round is abandoned. Abandoning
// it costs nothing — the cache simply keeps the verdict it had.
const RefreshBudget = 30 * time.Second

// Check runs one complete round: both remote sources, plus the local facts that
// need no network.
//
// The returned Verdict is USABLE whatever the error says. A source that did not
// answer leaves its field empty, which Known() reports and every surface renders
// as "non verificato" — the error is there for a caller that wants to log why,
// not a reason to discard the round. The local facts are present either way.
func Check(ctx context.Context, c *http.Client, stateDir string, now time.Time) (Verdict, error) {
	v := Verdict{CheckedAt: now, Installed: ReadInstalled(stateDir)}
	var errs []error

	if tag, err := LatestTag(ctx, c, Slug()); err == nil {
		v.LatestYtdl = tag
	} else {
		errs = append(errs, err)
	}
	if pin, err := FetchPin(ctx, c, Slug(), Branch()); err == nil {
		v.Pin = pin
	} else {
		errs = append(errs, err)
	}
	return v, errors.Join(errs...)
}

// RefreshAsync runs one check round in the background when checking is enabled
// and the cache has expired. It never blocks the caller, never delays exit, and
// writes nothing when the process dies first: a CLI invocation that finishes
// before the probe does simply takes the goroutine down with it, which is the
// intended behaviour rather than a leak (ADR-0016 §5). Every surface reads the
// CACHE — a file read — so no probe is ever on a path a user is waiting on.
//
// enabled is the resolved update_check key. It governs the AUTOMATIC probe only:
// a user who turns it off keeps the manual check and `ytdl --update`, because
// consent is about the machine phoning home on its own, not about the user's
// right to ask (ADR-0016 §6).
func RefreshAsync(stateDir string, enabled bool, now time.Time) {
	if !shouldRefresh(stateDir, enabled, now) {
		return
	}
	go refreshOnce(stateDir, now)
}

// shouldRefresh decides whether a round is worth running at all. It is separate
// from the goroutine so the decision — which is the part with rules in it — can
// be asserted without racing one.
func shouldRefresh(stateDir string, enabled bool, now time.Time) bool {
	if !enabled || stateDir == "" {
		return false
	}
	// A local build has no tag to compare against and is never reported stale.
	if buildinfo.Version == DevVersion {
		return false
	}
	cached, ok := Load(stateDir)
	return !ok || !cached.Fresh(now, DefaultTTL)
}

// refreshOnce is RefreshAsync's body, split out so tests can run it
// deterministically instead of racing a goroutine.
func refreshOnce(stateDir string, now time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), RefreshBudget)
	defer cancel()

	v, err := Check(ctx, nil, stateDir, now)
	if err != nil && !v.Known() {
		// A round where a source stayed silent must not overwrite a complete one:
		// that would destroy "the last known result and its date", which is exactly
		// what the not-verified state is required to carry (ADR-0016 §8). With
		// nothing cached there is nothing to lose, so the partial round is kept —
		// it still carries the local facts, which are always shown.
		if _, ok := Load(stateDir); ok {
			return
		}
	}
	_ = Save(stateDir, v)
}
