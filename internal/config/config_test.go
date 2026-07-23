package config

import (
	"testing"
)

func sp(s string) *string { return &s }
func bp(b bool) *bool     { return &b }

// pinHome makes Defaults()/Resolve() deterministic.
func pinHome(t *testing.T) string {
	t.Helper()
	t.Setenv("HOME", "/pinned-home")
	return "/pinned-home"
}

func TestResolveEmptyEqualsDefaults(t *testing.T) {
	pinHome(t)
	got, warns := Resolve(Partial{}, Partial{}, Partial{}, Env{})
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	if got != Defaults() {
		t.Errorf("Resolve(empty) = %+v\nwant Defaults() = %+v", got, Defaults())
	}
}

func TestPrecedencePerField(t *testing.T) {
	pinHome(t)

	// output_dir resolves independently through flag > env > session > file > default.
	t.Run("flag wins over env, session, file", func(t *testing.T) {
		file := Partial{OutputDir: sp("/from-file")}
		session := Partial{OutputDir: sp("/from-session")}
		flags := Partial{OutputDir: sp("/from-flag")}
		env := Env{OutDir: "/from-env"}
		got, _ := Resolve(flags, session, file, env)
		if got.OutputDir != "/from-flag" {
			t.Errorf("OutputDir = %q, want /from-flag", got.OutputDir)
		}
	})

	t.Run("env wins over session and file", func(t *testing.T) {
		file := Partial{OutputDir: sp("/from-file")}
		session := Partial{OutputDir: sp("/from-session")}
		env := Env{OutDir: "/from-env"}
		got, _ := Resolve(Partial{}, session, file, env)
		if got.OutputDir != "/from-env" {
			t.Errorf("OutputDir = %q, want /from-env", got.OutputDir)
		}
	})

	t.Run("session wins over file", func(t *testing.T) {
		file := Partial{OutputDir: sp("/from-file")}
		session := Partial{OutputDir: sp("/from-session")}
		got, _ := Resolve(Partial{}, session, file, Env{})
		if got.OutputDir != "/from-session" {
			t.Errorf("OutputDir = %q, want /from-session", got.OutputDir)
		}
	})

	t.Run("file wins over default", func(t *testing.T) {
		file := Partial{OutputDir: sp("/from-file")}
		got, _ := Resolve(Partial{}, Partial{}, file, Env{})
		if got.OutputDir != "/from-file" {
			t.Errorf("OutputDir = %q, want /from-file", got.OutputDir)
		}
	})

	// Independence: -o on the command line wins for output_dir only; format still
	// falls through env(n/a) -> file -> default separately.
	t.Run("fields resolve independently", func(t *testing.T) {
		file := Partial{Format: sp("flac")}
		flags := Partial{OutputDir: sp("/from-flag")}
		got, _ := Resolve(flags, Partial{}, file, Env{})
		if got.OutputDir != "/from-flag" {
			t.Errorf("OutputDir = %q, want /from-flag", got.OutputDir)
		}
		if got.Format != "flac" {
			t.Errorf("Format = %q, want flac (from file)", got.Format)
		}
	})
}

func TestEnvOnlyOutDir(t *testing.T) {
	pinHome(t)
	// Only YTDL_OUT_DIR is honoured; the env layer never touches format etc.
	got, _ := Resolve(Partial{}, Partial{}, Partial{}, Env{OutDir: "/env-out"})
	if got.OutputDir != "/env-out" {
		t.Errorf("OutputDir = %q, want /env-out", got.OutputDir)
	}
	if got.Format != DefaultFormat {
		t.Errorf("Format = %q, want default %q", got.Format, DefaultFormat)
	}
}

func TestSessionLayerNoOp(t *testing.T) {
	pinHome(t)
	// An empty session Partial (the Cycle 1 state) changes nothing.
	got, _ := Resolve(Partial{}, Partial{}, Partial{}, Env{})
	if got != Defaults() {
		t.Errorf("empty session altered the result: %+v", got)
	}
}

func TestTrailingSlashStripped(t *testing.T) {
	pinHome(t)
	// Mirrors the Bash `OUTPUT_DIR="${OUTPUT_DIR%/}"` (line 169): one trailing slash.
	cases := map[string]string{
		"/tmp/out/":  "/tmp/out",
		"/tmp/out":   "/tmp/out",
		"/tmp/out//": "/tmp/out/", // only ONE trailing slash removed, like %/
	}
	for in, want := range cases {
		got, _ := Resolve(Partial{OutputDir: sp(in)}, Partial{}, Partial{}, Env{})
		if got.OutputDir != want {
			t.Errorf("OutputDir(%q) = %q, want %q", in, got.OutputDir, want)
		}
	}
}

func TestValidFormat(t *testing.T) {
	for _, f := range []string{"mp3", "flac", "m4a", "opus", "wav"} {
		if !ValidFormat(f) {
			t.Errorf("ValidFormat(%q) = false, want true", f)
		}
	}
	for _, f := range []string{"bogus", "MP3", "", " mp3"} {
		if ValidFormat(f) {
			t.Errorf("ValidFormat(%q) = true, want false", f)
		}
	}
}

func TestBackendDefaults(t *testing.T) {
	t.Setenv("HOME", "/pinned-home")
	t.Setenv("XDG_STATE_HOME", "") // force the $HOME-based default
	d := Defaults()
	if d.LogDir != "/pinned-home/.local/state/ytdl/logs" {
		t.Errorf("LogDir = %q, want /pinned-home/.local/state/ytdl/logs", d.LogDir)
	}
	if d.LogRetentionDays != DefaultLogRetentionDays {
		t.Errorf("LogRetentionDays = %d, want %d", d.LogRetentionDays, DefaultLogRetentionDays)
	}
	if !d.BreadcrumbOnFailure {
		t.Error("BreadcrumbOnFailure default = false, want true (opt-out)")
	}
	if !d.Notify {
		t.Error("Notify default = false, want true")
	}
	if d.NotifyOn != DefaultNotifyOn {
		t.Errorf("NotifyOn = %q, want %q", d.NotifyOn, DefaultNotifyOn)
	}
	if d.NotifyForeground {
		t.Error("NotifyForeground default = true, want false (silent/background only)")
	}
	if !d.NotifySound {
		t.Error("NotifySound default = false, want true")
	}
}

func TestStatePathXDGWins(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/xdg/state")
	if got := StatePath(); got != "/xdg/state/ytdl" {
		t.Errorf("StatePath() = %q, want /xdg/state/ytdl", got)
	}
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "/home/u")
	if got := StatePath(); got != "/home/u/.local/state/ytdl" {
		t.Errorf("StatePath() = %q, want /home/u/.local/state/ytdl", got)
	}
}

func TestNotifyOnValidationFallsThrough(t *testing.T) {
	t.Setenv("HOME", "/pinned-home")
	// An invalid notify_on in the file falls through to the default.
	file := Partial{NotifyOn: nil}
	got, warns := Resolve(Partial{}, Partial{}, file, Env{})
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	if got.NotifyOn != DefaultNotifyOn {
		t.Errorf("NotifyOn = %q, want default %q", got.NotifyOn, DefaultNotifyOn)
	}
}

func TestBoolAndEmbedPrecedence(t *testing.T) {
	pinHome(t)
	file := Partial{EmbedThumbnail: bp(false), PlaylistDefault: bp(true)}
	got, _ := Resolve(Partial{}, Partial{}, file, Env{})
	if got.EmbedThumbnail {
		t.Errorf("EmbedThumbnail = true, want false from file")
	}
	if !got.PlaylistDefault {
		t.Errorf("PlaylistDefault = false, want true from file")
	}
	// A flag overrides the file.
	got, _ = Resolve(Partial{EmbedThumbnail: bp(true)}, Partial{}, file, Env{})
	if !got.EmbedThumbnail {
		t.Errorf("EmbedThumbnail = false, want true from flag")
	}
}
