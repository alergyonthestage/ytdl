package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/alergyonthestage/ytdl/internal/config"
	"github.com/alergyonthestage/ytdl/internal/term"
)

// Value sources, as shown in the right-hand column of `ytdl config`.
const (
	srcDefault = "predefinito"
	srcFile    = "file di configurazione"
	srcEnv     = "ambiente: YTDL_OUT_DIR"
)

// ConfigView is what `ytdl config` prints. It carries the LAYERS, not just the
// resolved settings, because the useful part of the command is not the values —
// those are visible in the GUI — but WHERE each one was decided. "I changed the
// folder and nothing happened" is answered by the source column: the environment
// was overriding the file all along.
type ConfigView struct {
	Path     string          // resolved config file path; "" when $HOME is unresolvable
	Exists   bool            // whether that file is actually there
	Settings config.Settings // the resolved values
	File     config.Partial  // what the config file said (nil field = it said nothing)
	Env      config.Env      // the environment layer
	Home     string          // for ~-contraction
	Width    int             // terminal columns; <= 0 = do not clip
}

// configRow is one printed setting.
type configRow struct {
	label  string
	value  string
	source string
}

// labelCols is the width of the setting-name column. Fixed rather than computed
// so the four groups line up with each other, not each within itself.
const configLabelCols = 22

// RenderConfig formats `ytdl config`: the effective settings, grouped the same
// way the GUI groups them, each with the layer that decided it, plus how to
// change them. It is deliberately read-only (ADR-0013) — a `config set` would
// duplicate config.Save's validation surface for a task the GUI and a text
// editor already serve.
func RenderConfig(v ConfigView) string {
	var b strings.Builder
	b.WriteString("IMPOSTAZIONI ytdl\n")
	b.WriteString(configFileLine(v))
	b.WriteString("\n")

	groups := []struct {
		title string
		rows  []configRow
	}{
		{"DOWNLOAD", downloadRows(v)},
		{"NOTIFICHE", notifyRows(v)},
		{"NOMI E METADATI", namingRows(v)},
		{"LOG E MANUTENZIONE", maintenanceRows(v)},
	}
	for _, g := range groups {
		fmt.Fprintf(&b, "  %s\n", g.title)
		for _, r := range g.rows {
			fmt.Fprintf(&b, "    %s%s  (%s)\n", term.Pad(r.label, configLabelCols), r.value, r.source)
		}
	}
	b.WriteString("\nModifica:  ytdl gui   ·   oppure apri il file qui sopra in un editor\n")
	return clipTo(b.String(), v.Width)
}

// configFileLine names the config file, and says plainly when there isn't one —
// "the file I keep telling you to edit does not exist yet" is the single most
// confusing thing about a config command.
func configFileLine(v ConfigView) string {
	switch {
	case v.Path == "":
		return "  file: (non determinabile: $HOME non è impostata)\n"
	case !v.Exists:
		return fmt.Sprintf("  file: %s  (non ancora creato: valgono i predefiniti)\n", contractHome(v.Path, v.Home))
	default:
		return fmt.Sprintf("  file: %s\n", contractHome(v.Path, v.Home))
	}
}

func downloadRows(v ConfigView) []configRow {
	s := v.Settings
	outSource := srcDefault
	switch {
	case v.Env.OutDir != "":
		outSource = srcEnv
	case v.File.OutputDir != nil:
		outSource = srcFile
	}
	return []configRow{
		{"cartella", contractHome(s.OutputDir, v.Home), outSource},
		{"formato", s.Format, sourceOf(v.File.Format != nil)},
		{"qualità audio", s.AudioQuality, sourceOf(v.File.AudioQuality != nil)},
		{"playlist intera", yesNo(s.PlaylistDefault), sourceOf(v.File.PlaylistDefault != nil)},
		{"download paralleli", concurrencyText(s.Concurrency), sourceOf(v.File.Concurrency != nil)},
		{"apri al termine", yesNo(s.OpenFolderOnDone), sourceOf(v.File.OpenFolderOnDone != nil)},
	}
}

func notifyRows(v ConfigView) []configRow {
	s := v.Settings
	return []configRow{
		{"notifiche", yesNo(s.Notify), sourceOf(v.File.Notify != nil)},
		{"quando", s.NotifyOn, sourceOf(v.File.NotifyOn != nil)},
		{"anche in primo piano", yesNo(s.NotifyForeground), sourceOf(v.File.NotifyForeground != nil)},
		{"suono", yesNo(s.NotifySound), sourceOf(v.File.NotifySound != nil)},
	}
}

func namingRows(v ConfigView) []configRow {
	s := v.Settings
	return []configRow{
		{"nome file", s.NameTemplate, sourceOf(v.File.NameTemplate != nil)},
		{"copertina", yesNo(s.EmbedThumbnail), sourceOf(v.File.EmbedThumbnail != nil)},
		{"metadati", yesNo(s.EmbedMetadata), sourceOf(v.File.EmbedMetadata != nil)},
		{"rimuovi [..]", s.StripBrackets, sourceOf(v.File.StripBrackets != nil)},
		{"rimuovi (tag)", s.StripTags, sourceOf(v.File.StripTags != nil)},
	}
}

func maintenanceRows(v ConfigView) []configRow {
	s := v.Settings
	return []configRow{
		{"cartella log", contractHome(s.LogDir, v.Home), sourceOf(v.File.LogDir != nil)},
		{"conservazione", retentionText(s.LogRetentionDays), sourceOf(v.File.LogRetentionDays != nil)},
		{"log accanto all'audio", yesNo(s.BreadcrumbOnFailure), sourceOf(v.File.BreadcrumbOnFailure != nil)},
		{"timeout per job", timeoutText(s.JobTimeout), sourceOf(v.File.JobTimeout != nil)},
	}
}

func sourceOf(fromFile bool) string {
	if fromFile {
		return srcFile
	}
	return srcDefault
}

func yesNo(b bool) string {
	if b {
		return "sì"
	}
	return "no"
}

// concurrencyText, retentionText and timeoutText spell out what 0 means for each
// key, since the same digit means "unlimited", "keep forever" and "no timeout"
// in three different places.
func concurrencyText(n int) string {
	if n == config.ConcurrencyUnlimited {
		return "senza limite"
	}
	return strconv.Itoa(n)
}

func retentionText(days int) string {
	switch {
	case days <= 0:
		return "per sempre"
	case days == 1:
		return "1 giorno"
	default:
		return fmt.Sprintf("%d giorni", days)
	}
}

func timeoutText(seconds int) string {
	if seconds <= 0 {
		return "nessuno"
	}
	return fmt.Sprintf("%d s", seconds)
}
