package cli

import "strings"

// looksLikeURL reports whether tok is plausibly a media URL rather than a mistyped
// subcommand. yt-dlp accepts scheme-less inputs (youtube.com/…, verified against
// 2026.07.04, which warns and retries over https) and search prefixes (ytsearch:…),
// so requiring an "http" prefix would wrongly reject them. Instead we treat any
// token bearing a URL structural marker — a path/scheme slash, a dotted host, or a
// scheme/search colon — as a URL, and only a BARE WORD (none of `/ . :`) as a
// possible command. A bare word is never a valid yt-dlp URL (no host, no scheme),
// so intercepting it loses nothing real and turns yt-dlp's opaque "not a valid
// URL" into a helpful "unknown command" (Cycle 4). `ytdl -- <tok>` bypasses this
// check for the rare case a user really means a bare token as the URL.
func looksLikeURL(tok string) bool {
	return strings.ContainsAny(tok, "/.:")
}

// knownCommands are the subcommands a mistyped bare word might be aiming at, for
// the "did you mean" hint. `help` is included because Cycle 5 made it a
// positional command; version and update remain flags, so a bare word could not
// be aiming at them.
var knownCommands = []string{"queue", "status", "history", "gui", "cancel", "retry", "open", "again", "config", "help"}

// nearestCommand returns the closest known subcommand to tok, or "" if nothing is
// close enough. Comparison is case-insensitive ("Queue" is as much a typo as
// "queu"). A suggestion is offered only within a small edit distance (≤2) that is
// also strictly less than the command's length, so a short unrelated token (e.g.
// "xy") never spuriously "matches" a short command like "gui".
func nearestCommand(tok string) string {
	tok = strings.ToLower(tok)
	best, bestDist := "", 0
	for _, c := range knownCommands {
		d := levenshtein(tok, c)
		if best == "" || d < bestDist {
			best, bestDist = c, d
		}
	}
	if best != "" && bestDist <= 2 && bestDist < len(best) {
		return best
	}
	return ""
}

// levenshtein is the classic edit distance (insert/delete/substitute cost 1),
// computed over runes with a single rolling row. Inputs here are short command
// names, so the quadratic cost is irrelevant.
func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min3(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(rb)]
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}
