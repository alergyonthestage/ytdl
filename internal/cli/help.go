package cli

import (
	"fmt"
	"sort"
	"strings"
)

// ShortUsage is what `ytdl` with no arguments and `ytdl -h` print: the top tier
// of the information hierarchy and nothing else (ux-principles.md §2). It answers
// "what do I type" in one screen — the five things people actually run, the four
// options they actually pass, the recovery line, and the quoting warning that
// causes the most reported breakage. Everything else is one `ytdl help
// <argomento>` away and nothing has been deleted: `ytdl help tutto` still prints
// the full reference.
const ShortUsage = `ytdl — scarica musica da YouTube / YT Music con nomi e tag puliti.

  ytdl "<url>"            scarica una traccia
  ytdl -b "<url>"         scaricala in background (la mette in coda)
  ytdl queue              a che punto sono i download
  ytdl history            cosa ho scaricato · da qui puoi aprire o riscaricare
  ytdl gui                interfaccia grafica (download, storico, impostazioni)

OPZIONI PIÙ USATE
  -p                      scarica l'INTERA playlist, non la sola traccia
  -f FORMATO              mp3|flac|m4a|opus|wav     (default: mp3)
  -o CARTELLA             dove salvare              (default: ~/Music/ytdl)
  -n                      anteprima dei nomi, senza scaricare

SE SMETTE DI FUNZIONARE
  ytdl --update           YouTube cambia qualcosa ogni pochi mesi:
                          è quasi sempre questo.

METTI SEMPRE L'URL TRA VIRGOLETTE: contiene & che la shell interpreterebbe.

Altro:  ytdl help  (argomenti)  ·  ytdl <comando> --help  ·  ytdl help tutto
`

// Topic is one help page. Listed topics appear in the `ytdl help` index; the
// per-command pages do not (they are reached with `ytdl <comando> --help`, and
// listing nine more names would rebuild the wall this restructuring removed) but
// are still addressable as `ytdl help <comando>`, so a user who guesses that way
// is not told "unknown topic" about a command that plainly exists.
type Topic struct {
	Name   string // the word the user types
	Title  string // one line, shown in the index
	Body   string // the page
	Listed bool   // whether the index shows it
}

// topics is the registry, in index order. Order is deliberate: the two things
// people look up most (all the flags, the queue) come first.
var topics = []Topic{
	{Name: "opzioni", Title: "tutti i flag di download", Listed: true, Body: topicOptions},
	{Name: "coda", Title: "queue · status · cancel · retry", Listed: true, Body: topicQueue},
	{Name: "storico", Title: "history · open · again", Listed: true, Body: topicHistory},
	{Name: "impostazioni", Title: "file di configurazione e chiavi", Listed: true, Body: topicSettings},
	{Name: "gui", Title: "l'interfaccia grafica", Listed: true, Body: topicGUI},
	{Name: "titoli", Title: "come vengono puliti titolo e tag", Listed: true, Body: topicTitles},
	{Name: "problemi", Title: "errori comuni e cosa fare", Listed: true, Body: topicProblems},
	{Name: "tutto", Title: "il testo completo di riferimento", Listed: true, Body: Usage},

	// Per-command pages.
	{Name: "queue", Body: helpQueue},
	{Name: "status", Body: helpStatus},
	{Name: "history", Body: helpHistory},
	{Name: "cancel", Body: helpCancel},
	{Name: "retry", Body: helpRetry},
	{Name: "open", Body: helpOpen},
	{Name: "again", Body: helpAgain},
	{Name: "config", Body: helpConfig},
}

// LookupTopic returns the page for name, or false if there is none.
func LookupTopic(name string) (Topic, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, t := range topics {
		if t.Name == name {
			return t, true
		}
	}
	return Topic{}, false
}

// TopicIndex is what `ytdl help` prints with no argument: the list of pages.
func TopicIndex() string {
	var b strings.Builder
	b.WriteString("ARGOMENTI — ytdl help <argomento>\n")
	for _, t := range topics {
		if !t.Listed {
			continue
		}
		fmt.Fprintf(&b, "  %-14s %s\n", t.Name, t.Title)
	}
	b.WriteString("\n  ytdl <comando> --help   dettaglio di un singolo comando\n")
	b.WriteString("  ytdl -h                 la schermata breve\n")
	return b.String()
}

// HelpText renders whichever help the parse asked for.
func HelpText(p *Parsed) string {
	switch {
	case p.HelpTopic != "":
		if t, ok := LookupTopic(p.HelpTopic); ok {
			return t.Body
		}
		// Unreachable in practice: Parse rejects an unknown topic so it can
		// suggest a near one. Falling back to the index beats printing nothing.
		return TopicIndex()
	case p.HelpCommand:
		return TopicIndex()
	default:
		return ShortUsage
	}
}

// nearestTopic returns the closest help page name to tok, or "" if nothing is
// close. It reuses the Cycle 4 edit-distance rule, so `ytdl help stroico`
// proposes `storico` exactly as `ytdl queu` proposes `queue`.
func nearestTopic(tok string) string {
	tok = strings.ToLower(strings.TrimSpace(tok))
	names := make([]string, 0, len(topics))
	for _, t := range topics {
		names = append(names, t.Name)
	}
	sort.Strings(names) // deterministic pick among equally-distant names
	best, bestDist := "", 0
	for _, n := range names {
		d := levenshtein(tok, n)
		if best == "" || d < bestDist {
			best, bestDist = n, d
		}
	}
	if best != "" && bestDist <= 2 && bestDist < len(best) {
		return best
	}
	return ""
}

// ---- topic bodies --------------------------------------------------------

const topicOptions = `OPZIONI — ytdl [opzioni] URL

  -o, --output DIR    Cartella di destinazione   (default: $YTDL_OUT_DIR o ~/Music/ytdl)
  -p, --playlist      Scarica l'INTERA playlist        (default: solo la traccia)
  -f, --format FMT    Formato audio: mp3|flac|m4a|opus|wav   (default: mp3)
  -n, --dry-run       Mostra i nomi file risultanti SENZA scaricare
  -s, --silent        Nessun output (per lanciarlo in background con &)
  -b, --background    Accoda ed esegui in background sotto il limite di concorrenza
  -v, --verbose       Mostra tutto l'output di yt-dlp (per debug)
  -h, --help          La schermata breve
  -V, --version       Versione di ytdl e di yt-dlp
      --update        Aggiorna ytdl e yt-dlp all'ultima versione

  --                  Il token successivo è l'URL, qualunque cosa sia
                      (serve solo se l'URL sembra un comando)

PRECEDENZA
  flag  >  $YTDL_OUT_DIR  >  file di configurazione  >  predefiniti
  Vedi da dove viene ogni valore con:  ytdl config
`

const topicQueue = `CODA — download in background

  ytdl -b "<url>"                      accoda ed esegui in background
  ytdl queue [--watch]                 cosa è in attesa e in corso (--watch aggiorna sul posto)
  ytdl status                          stato del daemon + riepilogo recente
  ytdl cancel [<n> | <id> | --all]     annulla un download in corso o in attesa
  ytdl retry  [<n> | <id> | --all]     rimette in coda un download FALLITO

  Senza argomenti, cancel e retry stampano la lista numerata da cui leggere <n>.
  <n> è l'indice del momento; <id> è il prefisso dell'id, stabile — negli script
  usa quello.

COME FUNZIONA
  I download in background li esegue un daemon che parte da solo quando serve e
  si ferma da solo quando la coda è vuota. Non c'è niente da avviare a mano.
  Quanti download in parallelo: chiave "concurrency" (vedi: ytdl help impostazioni).

RIPROVA vs RISCARICA
  retry  rimette in coda un job FALLITO che è ancora nella coda.
  again  ricrea un download partendo dallo STORICO (vedi: ytdl help storico).
`

const topicHistory = `STORICO — cosa ho scaricato

  ytdl history [--failed] [--limit N] [--search TESTO] [--ids]
                                       elenco numerato, dal più recente
  ytdl open  <n | id> [--folder]       apre l'audio (--folder: lo mostra nella cartella)
  ytdl again <n | id>                  lo riscarica (nuovo job in coda)

  Ogni riga dice com'è andata, quando, cosa, in che formato e — se è andata bene
  — DOVE è finito il file; se è andata male, PERCHÉ.

  --search cerca nel titolo, nel link e nel nome del file salvato.
  <n> è l'indice della lista che hai appena stampato; <id> è il prefisso dell'id
  del record, stabile e valido su tutta la finestra di conservazione.

  Lo storico copre sia i download in primo piano sia quelli in background, e
  dura quanto dice "log_retention_days" (vedi: ytdl help impostazioni).
`

const topicSettings = `IMPOSTAZIONI

  ytdl config              tutte le impostazioni e da dove viene ogni valore
  ytdl config --path       solo il percorso del file (per gli script)
  ytdl gui                 modificale con l'interfaccia grafica

  Il file è ~/.config/ytdl/config, una riga "chiave = valore" per impostazione.
  Le righe che iniziano con # sono commenti. Una chiave sconosciuta o un valore
  non valido vengono ignorati con un avviso: ytdl non si rifiuta di partire.

CHIAVI
  output_dir             cartella di destinazione
  format                 mp3|flac|m4a|opus|wav
  audio_quality          0-9 (0 = massima)
  playlist_default       true|false — scaricare sempre l'intera playlist
  concurrency            quanti download in parallelo (o "unlimited")
  open_folder_on_done    true|false — a fine download mostra il file nella cartella
                         (solo per i download in primo piano)
  name_template          template del nome file (yt-dlp)
  strip_brackets         regex: cosa togliere dal titolo, forma [..]
  strip_tags             regex: cosa togliere dal titolo, forma (..)
  embed_thumbnail        true|false — copertina nel file
  embed_metadata         true|false — tag artista/traccia
  notify                 true|false — notifica a fine download
  notify_on              failure|success|both
  notify_foreground      true|false — notifica anche in primo piano
  notify_sound           true|false
  log_dir                dove tenere storico e log
  log_retention_days     per quanti giorni (0 = per sempre)
  breadcrumb_on_failure  true|false — lascia un .log accanto all'audio mancante
  job_timeout            secondi oltre i quali un download in coda viene fermato
                         (0 = nessun limite)

  $YTDL_OUT_DIR ha la precedenza sul file; i flag hanno la precedenza su tutto.
`

const topicGUI = `INTERFACCIA GRAFICA

  ytdl gui                 apre l'interfaccia nel browser

  Tre schermate: Download (nuovo download + coda in tempo reale), Cronologia
  (cosa hai scaricato, con apri / riscarica) e Impostazioni.

  Gira solo sul tuo computer: è in ascolto su 127.0.0.1, non è raggiungibile
  dalla rete, e ogni richiesta richiede un token creato al momento dell'avvio.
  Si chiude da sola quando chiudi la scheda e la coda è vuota.
`

const topicTitles = `TITOLI E TAG — cosa fa ytdl al nome del file

  • Se ci sono metadati strutturati (YT Music / canali "- Topic") usa SEMPRE
    quelli: artist e track nativi hanno la precedenza sul titolo del video.
  • Altrimenti ricava "Artista - Traccia" splittando il titolo su " - ".
  • In entrambi i casi rimuove [..] e (Original Mix)/(Extended)/(Official Video)
    e simili, MANTENENDO le informazioni utili come (Qualcuno Remix) e (feat. …).

  Il risultato è sia il nome del file sia i tag scritti dentro l'audio.
  Per vedere che nomi verrebbero fuori senza scaricare niente:  ytdl -n "<url>"
  Le regole si cambiano con name_template, strip_brackets e strip_tags
  (vedi: ytdl help impostazioni).
`

const topicProblems = `PROBLEMI COMUNI

  I download hanno smesso di funzionare, tutti insieme
      ytdl --update
      YouTube cambia qualcosa ogni pochi mesi e yt-dlp va aggiornato. È quasi
      sempre questo.

  "Sign in to confirm you're not a bot"
      YouTube ha chiesto una verifica anti-bot. Capita soprattutto con -n
      (l'anteprima legge i soli metadati). Il download vero di solito funziona
      lo stesso: riprova senza -n.

  Ho lanciato con & o con -s / -b e non so com'è andata
      ytdl queue      cosa sta girando adesso
      ytdl history    com'è finita, e perché se è fallita
      Accanto all'audio mancante viene lasciato anche un file .log col dettaglio.

  Ha scaricato solo una traccia invece della playlist
      Serve -p. Senza, un link di playlist scarica il singolo brano.

  Il comando è stato interpretato male, o l'URL è stato troncato
      METTI L'URL TRA VIRGOLETTE: contiene & (…&list=…&index=2) che la shell
      interpreta come "esegui in background".

  "Formato non valido"
      I formati sono mp3, flac, m4a, opus, wav. Nient'altro.

  Ho cambiato la cartella e continua a salvare altrove
      ytdl config     mostra da dove viene ogni valore: quasi sempre è
                      $YTDL_OUT_DIR che ha la precedenza sul file.
`

// ---- per-command pages ---------------------------------------------------

const helpQueue = `ytdl queue [--watch]

  Mostra i download in corso e in attesa. Solo il presente: quello che è già
  finito sta nello storico (ytdl history).

  --watch, -w    aggiorna sul posto finché la coda non si svuota (solo su
                 terminale; se l'output è rediretto stampa una sola istantanea)
`

const helpStatus = `ytdl status

  Riepilogo in una schermata: se il daemon è attivo, quanti download sono in
  attesa e in corso, e come sono andati quelli recenti (nella finestra di
  conservazione dello storico).

  "daemon inattivo" è normale: parte da solo quando c'è da lavorare.
`

const helpHistory = `ytdl history [--failed] [--limit N] [--search TESTO] [--ids]

  Elenco numerato dei download, dal più recente. Ogni riga: esito, quando,
  titolo, formato e — se è andata bene — dove è finito il file; se è andata
  male, il motivo.

  --failed        solo i download non riusciti
  --limit N       quante righe (default: 20; 0 = nessun limite)
  --search TESTO  cerca nel titolo, nel link e nel nome del file salvato
  --ids           mostra anche l'id di ogni record

INDICE E ID
  Senza filtri, il numero fra [ ] si passa direttamente a  ytdl open <n>  e
  ytdl again <n>.

  Con --failed, --search o un --limit diverso la lista si accorcia, ma quei due
  comandi continuano a contare sulla lista COMPLETA. In quel caso ytdl stampa
  l'id su ogni riga e ti dice di usare quello: l'id è stabile e vale sempre.
`

const helpCancel = `ytdl cancel [<n> | <id> | --all]

  Ferma un download in corso o in attesa. Senza argomenti stampa la lista
  numerata da cui leggere <n>.

  <n>      indice nella lista di adesso
  <id>     prefisso dell'id del job — stabile, da preferire negli script
  --all    annulla tutto quello che è in corso o in attesa

  Un download già finito non si annulla: non c'è più niente da fermare.
`

const helpRetry = `ytdl retry [<n> | <id> | --all]

  Rimette in coda un download FALLITO che è ancora nella coda, con le stesse
  impostazioni con cui era partito. Senza argomenti stampa la lista numerata.

  <n>      indice nella lista di adesso
  <id>     prefisso dell'id del job — stabile, da preferire negli script
  --all    riprova tutti i falliti

  Per rifare un download partendo dallo STORICO (non dalla coda) serve invece:
  ytdl again — vedi  ytdl help storico.
`

const helpOpen = `ytdl open <n | id> [--folder]

  Apre il file audio di un download dello storico. Senza argomenti stampa lo
  storico numerato da cui leggere <n>.

  <n>        indice nella lista di  ytdl history  SENZA filtri
  <id>       prefisso dell'id del record — stabile, da preferire negli script
  --folder   invece di aprirlo, lo mostra nella cartella

  Se hai filtrato la lista (--failed, --search) usa l'id: una lista filtrata lo
  stampa su ogni riga, e  ytdl history --ids  lo mostra sempre.

  Apre solo file audio prodotti da ytdl e ancora al loro posto. Se il file è
  stato spostato o cancellato te lo dice: riscaricalo con  ytdl again <n>.
`

const helpAgain = `ytdl again <n | id>

  Riscarica un record dello storico: crea un NUOVO download in coda. Senza
  argomenti stampa lo storico numerato da cui leggere <n>.

  <n>    indice nella lista di  ytdl history  SENZA filtri
  <id>   prefisso dell'id del record — stabile, da preferire negli script

  Se hai filtrato la lista (--failed, --search) usa l'id: una lista filtrata lo
  stampa su ogni riga, e  ytdl history --ids  lo mostra sempre.

  Riprende dal record il formato e se era una playlist intera; la cartella di
  destinazione è invece quella configurata ADESSO.

  Se lo stesso link è già in attesa o in corso non lo accoda una seconda volta.
`

const helpConfig = `ytdl config [--path]

  Stampa tutte le impostazioni con il valore in vigore e, per ognuna, da dove
  viene: predefinito, file di configurazione o variabile d'ambiente.

  --path    stampa solo il percorso del file di configurazione

  È di sola lettura: per cambiare le impostazioni usa  ytdl gui  oppure apri il
  file in un editor. L'elenco delle chiavi è in  ytdl help impostazioni.
`
