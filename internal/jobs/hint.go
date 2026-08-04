package jobs

import "strings"

// A failure told the user WHAT went wrong and never what to do about it: a
// record's reason is yt-dlp's last stderr line, in English, capped — while
// ux-principles.md §5 requires the next step (G8).
//
// The hint is derived at RENDER TIME from that stored line. Nothing is added to
// the record, so records written before this existed get their hint too, and
// there is no migration. It lives here, next to the other actions both channels
// perform on a record, because a failure must read the same in the GUI and in
// the terminal (§3).
//
// Two rules govern the catalogue:
//
//   - Only remedies that EXIST TODAY. An age- or bot-restricted video has no
//     remedy until `cookies_from_browser` ships (G25, Cycle 9), so it matches
//     the deliberately empty first entry and the row shows the raw reason alone.
//     Naming a step the tool cannot perform would be the same defect one layer
//     up.
//   - The list is ORDERED, most specific first: a 429 also says "Unable to
//     download webpage", and the answer is not "check your network".
type failurePattern struct {
	needles []string
	hint    string
}

var failurePatterns = []failurePattern{
	{
		// The known gap, kept explicit so it cannot be swallowed by a later
		// pattern and so Cycle 9 knows exactly where its remedy goes.
		needles: []string{"sign in to confirm", "confirm your age", "age-restricted", "age restricted", "cookies"},
		hint:    "",
	},
	{
		needles: []string{"http error 429", "too many requests", "rate-limit", "rate limit"},
		hint:    "YouTube sta limitando le richieste: aspetta qualche minuto e riprova. Se succede spesso, riduci i download in parallelo nelle impostazioni.",
	},
	{
		needles: []string{
			"video unavailable", "video is unavailable", "video is no longer available",
			"private video", "video is private", "has been removed", "been terminated",
		},
		hint: "Il video non è più disponibile su YouTube: controlla il link o cerca un'altra versione.",
	},
	{
		needles: []string{"no space left", "disk quota exceeded"},
		hint:    "Spazio esaurito sul disco: libera spazio e riprova.",
	},
	{
		needles: []string{"permission denied", "read-only file system", "operation not permitted"},
		hint:    "Cartella di destinazione non scrivibile: scegline un'altra nelle impostazioni.",
	},
	{
		// BELOW disk and permissions, and matched on the "not found" wording
		// rather than on a bare "postprocessing": yt-dlp reports a full disk and
		// an unwritable folder as postprocessing errors too, and sending someone
		// with a full disk to reinstall dependencies onto that same disk is a
		// dead end. CheckDeps also refuses to start without ffmpeg, so a
		// postprocessing failure that reaches a record is rarely a missing one.
		needles: []string{"ffmpeg not found", "ffprobe and ffmpeg", "ffmpeg-location"},
		hint:    "Manca ffmpeg o è incompleto: reinstalla le dipendenze con  ytdl --update.",
	},
	{
		needles: []string{"unsupported url", "is not a valid url"},
		hint:    "Link non riconosciuto: controlla di aver incollato l'indirizzo di un video o di una playlist.",
	},
	{
		// Before the network entry, which owns "unable to download webpage": a
		// 404 says that too, and sending someone to check their Wi-Fi over a
		// link that will stay dead for ever is the same dead end as a 429 would
		// have been.
		needles: []string{"http error 404", "http error 410"},
		hint:    "Il link non porta a niente: controlla di averlo copiato per intero.",
	},
	{
		// A 403 on YouTube is almost always a signature yt-dlp can no longer
		// produce, which is the same remedy as the extractor failures below.
		needles: []string{"http error 403"},
		hint:    "Quasi sempre è yt-dlp non aggiornato: lancia  ytdl --update  e riprova.",
	},
	{
		needles: []string{
			"unable to extract", "nsig extraction", "signature extraction",
			"player response", "please report this issue", "yt-dlp/yt-dlp/issues",
		},
		hint: "Quasi sempre è yt-dlp non aggiornato: lancia  ytdl --update  e riprova.",
	},
	{
		// Last: these needles are the most generic ones in the table.
		needles: []string{
			"temporary failure in name resolution", "name or service not known",
			"failed to resolve", "connection refused", "connection reset",
			"network is unreachable", "urlopen error", "timed out",
			"unable to download webpage",
		},
		hint: "Connessione assente o instabile: controlla la rete e riprova.",
	},
}

// FailureHint returns the next step for a stored failure reason, or "" when
// ytdl has nothing honest to suggest — which is a valid answer: the row then
// shows the reason alone, as it always did.
func FailureHint(reason string) string {
	r := strings.ToLower(reason)
	if r == "" {
		return ""
	}
	for _, p := range failurePatterns {
		for _, n := range p.needles {
			if strings.Contains(r, n) {
				return p.hint
			}
		}
	}
	return ""
}
