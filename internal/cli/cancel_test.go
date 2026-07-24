package cli

import (
	"strings"
	"testing"

	"github.com/alergyonthestage/ytdl/internal/queue"
)

func TestParseCancelRetry(t *testing.T) {
	for _, kw := range []string{"cancel", "retry"} {
		t.Run(kw+"/no-args lists", func(t *testing.T) {
			p, err := Parse([]string{kw})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if p.Target != "" || p.All {
				t.Errorf("empty invocation should carry no target/all: %+v", p)
			}
		})
		t.Run(kw+"/index", func(t *testing.T) {
			p, err := Parse([]string{kw, "2"})
			if err != nil || p.Target != "2" || p.All {
				t.Errorf("got %+v, err %v", p, err)
			}
		})
		t.Run(kw+"/--all", func(t *testing.T) {
			p, err := Parse([]string{kw, "--all"})
			if err != nil || !p.All || p.Target != "" {
				t.Errorf("got %+v, err %v", p, err)
			}
		})
		t.Run(kw+"/id-prefix", func(t *testing.T) {
			p, err := Parse([]string{kw, "1700000000-abc"})
			if err != nil || p.Target != "1700000000-abc" {
				t.Errorf("got %+v, err %v", p, err)
			}
		})
		t.Run(kw+"/target+all rejected", func(t *testing.T) {
			if _, err := Parse([]string{kw, "2", "--all"}); err == nil {
				t.Error("target AND --all should be an error")
			}
		})
		t.Run(kw+"/two targets rejected", func(t *testing.T) {
			if _, err := Parse([]string{kw, "1", "2"}); err == nil {
				t.Error("two positional targets should be an error")
			}
		})
		t.Run(kw+"/unknown flag rejected", func(t *testing.T) {
			if _, err := Parse([]string{kw, "-x"}); err == nil {
				t.Error("unknown flag should be an error")
			}
		})
	}
	if p, _ := Parse([]string{"cancel", "1"}); p.Action != ActionCancel {
		t.Errorf("cancel action = %v", p.Action)
	}
	if p, _ := Parse([]string{"retry", "1"}); p.Action != ActionRetry {
		t.Errorf("retry action = %v", p.Action)
	}
}

func TestLiveOrderedRunningFirst(t *testing.T) {
	snap := queue.Snapshot{
		Running: []queue.Entry{{ID: "r"}},
		Pending: []queue.Entry{{ID: "p1"}, {ID: "p2"}},
	}
	o := LiveOrdered(snap)
	if len(o) != 3 || o[0].ID != "r" || o[1].ID != "p1" || o[2].ID != "p2" {
		t.Errorf("LiveOrdered = %v, want running-then-pending", o)
	}
}

func TestRenderCancelList(t *testing.T) {
	snap := queue.Snapshot{
		Running: []queue.Entry{{ID: "r1", State: queue.Running, Job: queue.Job{URL: "https://a"}}},
		Pending: []queue.Entry{{ID: "p1", State: queue.Pending, Job: queue.Job{URL: "https://b"}}},
	}
	out := RenderCancelList(snap)
	for _, want := range []string{"[1]", "in corso", "[2]", "in attesa", "ytdl cancel --all"} {
		if !strings.Contains(out, want) {
			t.Errorf("RenderCancelList missing %q:\n%s", want, out)
		}
	}
	if empty := RenderCancelList(queue.Snapshot{}); !strings.Contains(empty, "niente da annullare") {
		t.Errorf("empty cancel list should say so:\n%s", empty)
	}
}

func TestRenderRetryList(t *testing.T) {
	failed := []queue.Entry{{ID: "f1", State: queue.Failed, Job: queue.Job{URL: "https://youtu.be/ZZZ"}}}
	out := RenderRetryList(failed)
	for _, want := range []string{"[1]", "youtu.be/ZZZ", "ytdl retry --all"} { // scheme trimmed by the shortener
		if !strings.Contains(out, want) {
			t.Errorf("RenderRetryList missing %q:\n%s", want, out)
		}
	}
	if empty := RenderRetryList(nil); !strings.Contains(empty, "nessun download fallito") {
		t.Errorf("empty retry list should say so:\n%s", empty)
	}
}
