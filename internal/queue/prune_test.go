package queue

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPruneTerminalByAge(t *testing.T) {
	sp := Open(t.TempDir())
	if err := sp.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	old := time.Now().AddDate(0, 0, -40) // older than 30d
	recent := time.Now().Add(-time.Hour) // within 30d

	write := func(state State, name string, mod time.Time) string {
		p := filepath.Join(sp.dir(state), name+".json")
		if err := os.WriteFile(p, []byte(`{"url":"u"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mod, mod); err != nil {
			t.Fatal(err)
		}
		return p
	}
	oldDone := write(Done, "old-done", old)
	newDone := write(Done, "new-done", recent)
	oldFail := write(Failed, "old-fail", old)
	oldPending := write(Pending, "old-pending", old) // live state, never terminal-pruned

	if err := sp.PruneTerminal(30); err != nil {
		t.Fatal(err)
	}

	gone := func(p string) {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("expected %s pruned", filepath.Base(p))
		}
	}
	kept := func(p string) {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected %s kept: %v", filepath.Base(p), err)
		}
	}
	gone(oldDone)
	gone(oldFail)
	kept(newDone)
	kept(oldPending)
}

func TestPruneTerminalZeroKeepsAll(t *testing.T) {
	sp := Open(t.TempDir())
	if err := sp.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(sp.dir(Done), "x.json")
	if err := os.WriteFile(p, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	ancient := time.Now().AddDate(0, 0, -400)
	os.Chtimes(p, ancient, ancient)

	if err := sp.PruneTerminal(0); err != nil { // 0 = keep forever
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Error("retention 0 must keep everything")
	}
}
