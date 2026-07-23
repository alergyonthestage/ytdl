package notify

import (
	"runtime"
	"strings"
	"testing"
)

func TestOsascriptBuilds(t *testing.T) {
	got := osascript(Notification{Title: "ytdl", Body: "Brano scaricato", Sound: false})
	want := `display notification "Brano scaricato" with title "ytdl"`
	if got != want {
		t.Errorf("osascript = %q, want %q", got, want)
	}
	if s := osascript(Notification{Title: "ytdl", Body: "x", Sound: true}); !strings.HasSuffix(s, ` sound name "Glass"`) {
		t.Errorf("sound clause missing: %q", s)
	}
}

func TestEscapeAS(t *testing.T) {
	// A body with quotes, a backslash and a newline must not break the one-line
	// -e script: quotes/backslash escaped, newline flattened to a space.
	got := osascript(Notification{Title: "ytdl", Body: "he said \"hi\"\npath\\x"})
	if strings.Contains(got, "\n") {
		t.Errorf("newline not flattened: %q", got)
	}
	if !strings.Contains(got, `he said \"hi\" path\\x`) {
		t.Errorf("escaping wrong: %q", got)
	}

	// Every recognised line separator (CR, LF, U+2028, U+2029) is flattened, so
	// none can terminate the single-line AppleScript literal.
	sep := osascript(Notification{Title: "ytdl", Body: "a\rb\nc\u2028d\u2029e"})
	for _, bad := range []string{"\r", "\n", "\u2028", "\u2029"} {
		if strings.Contains(sep, bad) {
			t.Errorf("separator %q not flattened: %q", bad, sep)
		}
	}
	if !strings.Contains(sep, "a b c d e") {
		t.Errorf("separators not turned into spaces: %q", sep)
	}
}

func TestDefaultPlatform(t *testing.T) {
	got := Default()
	if runtime.GOOS == "darwin" {
		if _, ok := got.(OsaNotifier); !ok {
			t.Errorf("Default() on darwin = %T, want OsaNotifier", got)
		}
	} else {
		if _, ok := got.(NopNotifier); !ok {
			t.Errorf("Default() off darwin = %T, want NopNotifier", got)
		}
	}
}

func TestNopNeverPanics(t *testing.T) {
	NopNotifier{}.Notify(Notification{Body: "anything", Sound: true})
}
