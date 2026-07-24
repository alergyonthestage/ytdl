package cli

// Usage is the help text, verbatim from the Bash ytdl (lines 47-95).
const Usage = `ytdl — scarica musica da YouTube / YT Music con yt-dlp, naming e tag puliti.

USO
  ytdl [opzioni] URL

OPZIONI
  -o, --output DIR    Cartella di destinazione   (default: $YTDL_OUT_DIR o ~/Music/ytdl)
  -p, --playlist      Scarica l'INTERA playlist        (default: solo la traccia)
  -f, --format FMT    Formato audio: mp3|flac|m4a|opus|wav   (default: mp3)
  -n, --dry-run       Mostra i nomi file risultanti SENZA scaricare
  -s, --silent        Nessun output (per lanciarlo in background con &)
  -b, --background    Accoda ed esegui in background sotto il limite di concorrenza
  -v, --verbose       Mostra tutto l'output di yt-dlp (per debug)
  -h, --help          Questo messaggio
  -V, --version       Mostra la versione di ytdl e yt-dlp
      --update        Aggiorna ytdl e yt-dlp all'ultima versione

CODA
  ytdl queue [--watch]                 Download in attesa e in corso (--watch aggiorna sul posto)
  ytdl status                          Stato del daemon + riepilogo recente
  ytdl history [--failed] [--limit N]  Storico dei download (anche in primo piano)
  ytdl cancel [<n> | <id> | --all]     Annulla un download in corso o in attesa
  ytdl retry  [<n> | <id> | --all]     Rimette in coda un download fallito
      (senza argomenti: lista numerata. <n> = indice del momento, <id> = prefisso
       dell'id, stabile — preferiscilo negli script)

INTERFACCIA GRAFICA
  ytdl gui                             Apre l'interfaccia web nel browser

AGGIORNAMENTI
  yt-dlp smette di funzionare quando YouTube cambia qualcosa: succede ogni
  pochi mesi. Se i download iniziano a fallire, la prima cosa da provare è:
       ytdl --update

ERRORI IN -s / -b
  Senza output non vedi i fallimenti: se un download fallisce viene lasciato un
  file .log (col dettaglio) nella cartella di output, accanto all'audio mancante.
  Coda:  ytdl queue  ·  storico completo:  ytdl history  ·  riepilogo:  ytdl status.

NB: METTI SEMPRE L'URL TRA VIRGOLETTE. Contiene & che altrimenti la shell
    interpreta come "esegui in background" (es. ...&list=...&index=2).

TIP: per fissare la cartella di output per la sessione corrente:
       export YTDL_OUT_DIR="/percorso/output"
     (il flag -o ha comunque la precedenza)

ESEMPI
  ytdl "https://youtu.be/XXXX"                        # singola traccia
  ytdl -p "https://youtube.com/playlist?list=YYYY"    # playlist intera
  ytdl -f flac -o ~/Desktop "https://youtu.be/XXXX"   # FLAC sul Desktop
  ytdl -n "https://youtu.be/XXXX"                     # anteprima dei nomi
  ytdl -b "https://youtu.be/XXXX"                     # accoda in background

COSA FA AL TITOLO
  • Se ci sono metadati strutturati (YT Music / Topic) usa SEMPRE quelli:
    artist e track nativi hanno la precedenza sul titolo del video.
  • Altrimenti ricava "Artista - Traccia" splittando il titolo su " - ".
  • In entrambi i casi rimuove [..] e (Original Mix)/(Extended)/ecc.,
    mantenendo le info utili tipo (Qualcuno Remix) e (feat. …).
`

// Parse-error messages. MsgNoURL and MsgUnknownOption keep the Bash wording;
// the missing-argument and too-many-arguments messages are clean replacements
// for the Bash ${2:?...} artifact and the C3 fix (a deliberate, documented
// divergence — see design-cycle1-core.md §8).
const (
	MsgNoURL            = "✗ Nessun URL fornito."
	MsgUnknownOption    = "✗ Opzione sconosciuta: %s" // %s = the offending token
	MsgMissingOutputDir = "✗ Manca la cartella (argomento di -o)."
	MsgMissingFormat    = "✗ Manca il formato (argomento di -f)."
	MsgTooManyArguments = "✗ Troppi argomenti: accetto un solo URL (ho già %q)." // %q = first URL
	MsgInvalidFormat    = "✗ Formato non valido: %s (ammessi: %s)."              // %s fmt, %s list
	MsgMissingLimit     = "✗ Manca il numero (argomento di --limit)."
	MsgInvalidLimit     = "✗ Limite non valido: %s (serve un intero non negativo)." // %s = the offending token
	MsgTooManyTargets   = "✗ Un solo indice per volta (oppure --all)."
	MsgTargetAndAll     = "✗ Indica un indice OPPURE --all, non entrambi."
	MsgTargetNotFound   = "✗ Non trovato: %s (lancia il comando senza argomenti per vedere la lista)." // %s = the token
	MsgTargetAmbiguous  = "✗ %s è ambiguo: corrisponde a più di un job. Usa più caratteri dell'id."    // %s = the token
)
