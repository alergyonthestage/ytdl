# Guida all'uso

Come usare `ytdl` per scaricare musica. Se non l'hai ancora installato, parti
dalla [guida all'installazione](guida-installazione.md).

## Il primo download

Apri il Terminale (⌘ + barra spaziatrice → `Terminale`), poi scrivi `ytdl`, uno
spazio, e incolla l'indirizzo del video **tra virgolette**:

```
ytdl "https://youtu.be/dQw4w9WgXcQ"
```

Premi Invio. Vedrai il titolo del brano e una barra di avanzamento. Alla fine ti
viene detto dove è stato salvato il file.

### ⚠️ Le virgolette sono obbligatorie

Gli indirizzi di YouTube contengono il carattere `&`, che per il Terminale
significa «esegui in background». Senza virgolette il comando viene troncato e
scarica la cosa sbagliata, o niente.

Sbagliato: `ytdl https://youtube.com/watch?v=XXX&list=YYY`
Giusto: `ytdl "https://youtube.com/watch?v=XXX&list=YYY"`

Prendi l'abitudine di metterle sempre, anche quando sembrano superflue.

## Dove finisce la musica

Nella cartella **Musica → ytdl** della tua cartella utente.

I file escono già con il nome giusto (`Artista - Titolo.mp3`) e con artista,
titolo e copertina incorporati, pronti per essere trascinati nella tua libreria
musicale.

Per salvare altrove **solo per questa volta**:

```
ytdl -o ~/Desktop "https://youtu.be/XXXX"
```

Per cambiare la cartella **per sempre**, vedi [più avanti](#cambiare-la-cartella-di-destinazione).

## Le cose che userai davvero

### Scaricare una playlist intera

Di default viene scaricato solo il brano indicato, anche se l'indirizzo fa parte
di una playlist. Per scaricare tutta la playlist aggiungi `-p`:

```
ytdl -p "https://youtube.com/playlist?list=YYYY"
```

### Scegliere il formato

Il formato predefinito è `mp3`, che va bene ovunque. Se vuoi qualità senza
perdita usa `flac`:

```
ytdl -f flac "https://youtu.be/XXXX"
```

Formati disponibili: `mp3`, `flac`, `m4a`, `opus`, `wav`.

### Vedere i nomi prima di scaricare

Utile con le playlist, per controllare che i titoli vengano puliti bene:

```
ytdl -n "https://youtube.com/playlist?list=YYYY"
```

Non scarica niente, mostra solo i nomi che verrebbero usati.

### Scaricare in background

Per playlist lunghe: il comando torna subito e il download continua per conto
suo, anche se chiudi il Terminale.

```
ytdl -b "https://youtube.com/playlist?list=YYYY"
```

Attenzione: in questa modalità non vedi né avanzamento né errori. Se un brano
fallisce, al posto del file audio trovi un file `.log` nella cartella di
destinazione, con la spiegazione. Per controllare, guarda se ci sono file `.log`
nella cartella.

### Combinare le opzioni

Le opzioni si sommano liberamente:

```
ytdl -p -f flac -o ~/Desktop "https://youtube.com/playlist?list=YYYY"
```

Playlist intera, in FLAC, sul Desktop.

## Cambiare la cartella di destinazione

Per cambiarla stabilmente, incolla nel Terminale (sostituendo il percorso con il
tuo):

```
echo 'export YTDL_OUT_DIR="/Users/tuonome/Musica/Scaricati"' >> ~/.zprofile
```

Poi chiudi e riapri il Terminale.

Un modo semplice per ottenere il percorso corretto: trascina la cartella dal
Finder dentro la finestra del Terminale, e il percorso viene scritto da solo.

## Quando qualcosa non funziona

### I download hanno smesso di funzionare

È la situazione più comune, e quasi sempre non è colpa tua: YouTube cambia
qualcosa e lo strumento che scarica va aggiornato. Succede ogni pochi mesi.

```
ytdl --update
```

Prova **sempre** questo prima di ogni altra cosa.

### «Nessun file scaricato»

Nella quasi totalità dei casi mancano le virgolette attorno all'indirizzo.
Ricontrolla il comando.

### «Opzione sconosciuta»

Hai scritto male un'opzione, oppure hai messo l'indirizzo senza virgolette e il
Terminale ha interpretato un pezzo dell'URL come opzione.

### Il nome del file è sbagliato

`ytdl` prova a ricavare artista e titolo dai dati del brano, e quando non ci sono
li deduce dal titolo del video. Se il video ha un titolo scritto in modo insolito,
il risultato può essere impreciso. Puoi sempre rinominare il file a mano.

## Tutte le opzioni

Per l'elenco completo, sempre aggiornato:

```
ytdl --help
```

| Opzione | Cosa fa |
|---|---|
| `-o CARTELLA` | Salva in una cartella diversa, solo per questa volta |
| `-p` | Scarica l'intera playlist |
| `-f FORMATO` | Formato audio: `mp3`, `flac`, `m4a`, `opus`, `wav` |
| `-n` | Mostra i nomi senza scaricare |
| `-b` | Scarica in background e restituisce subito il controllo |
| `-v` | Mostra tutti i dettagli tecnici (utile per capire un errore) |
| `--update` | Aggiorna ytdl e i suoi componenti |
| `--version` | Mostra le versioni installate |
| `--help` | Elenco completo delle opzioni |
