package queue

import (
	"errors"
	"os"
	"slices"
	"testing"
	"time"
)

func TestCancelMarkerLifecycle(t *testing.T) {
	s := Open(t.TempDir())
	id := mustEnqueue(t, s, job("https://youtu.be/A", time.Unix(1, 0)))

	// No markers yet.
	if ids, err := s.CancelRequests(); err != nil || len(ids) != 0 {
		t.Fatalf("CancelRequests (empty) = %v, %v", ids, err)
	}

	// Request is idempotent and shows up.
	if err := s.RequestCancel(id); err != nil {
		t.Fatalf("RequestCancel: %v", err)
	}
	if err := s.RequestCancel(id); err != nil {
		t.Fatalf("RequestCancel (repeat): %v", err)
	}
	ids, err := s.CancelRequests()
	if err != nil {
		t.Fatalf("CancelRequests: %v", err)
	}
	if !slices.Equal(ids, []string{id}) {
		t.Fatalf("CancelRequests = %v, want [%s]", ids, id)
	}

	// Clear removes it; clearing again is a no-op.
	if err := s.ClearCancel(id); err != nil {
		t.Fatalf("ClearCancel: %v", err)
	}
	if err := s.ClearCancel(id); err != nil {
		t.Fatalf("ClearCancel (repeat) should be a no-op: %v", err)
	}
	if ids, _ := s.CancelRequests(); len(ids) != 0 {
		t.Fatalf("marker survived ClearCancel: %v", ids)
	}
}

func TestCancelPending(t *testing.T) {
	s := Open(t.TempDir())
	id := mustEnqueue(t, s, job("https://youtu.be/A", time.Unix(1, 0)))

	// A pending job is deleted outright.
	was, err := s.CancelPending(id)
	if err != nil || !was {
		t.Fatalf("CancelPending(pending) = %v, %v; want true, nil", was, err)
	}
	if snap, _ := s.List(); len(snap.Pending) != 0 {
		t.Fatalf("job still pending after CancelPending: %+v", snap.Pending)
	}

	// A second call (nothing there) reports not-pending, not an error.
	was, err = s.CancelPending(id)
	if err != nil || was {
		t.Fatalf("CancelPending(gone) = %v, %v; want false, nil", was, err)
	}
}

func TestCancelPendingNotPendingOnceRunning(t *testing.T) {
	s := Open(t.TempDir())
	id := mustEnqueue(t, s, job("https://youtu.be/A", time.Unix(1, 0)))
	if _, err := s.ClaimNext(); err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}
	// Now running: CancelPending must NOT delete it (the daemon owns it; a running
	// cancel goes through the marker instead).
	was, err := s.CancelPending(id)
	if err != nil || was {
		t.Fatalf("CancelPending(running) = %v, %v; want false, nil", was, err)
	}
	if snap, _ := s.List(); len(snap.Running) != 1 {
		t.Fatalf("running job was disturbed: %+v", snap.Running)
	}
}

func TestRetry(t *testing.T) {
	s := Open(t.TempDir())
	id := mustEnqueue(t, s, job("https://youtu.be/A", time.Unix(1, 0)))
	if _, err := s.ClaimNext(); err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}
	if err := s.MarkFailed(id); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}

	if err := s.Retry(id); err != nil {
		t.Fatalf("Retry: %v", err)
	}
	snap, _ := s.List()
	if len(snap.Pending) != 1 || len(snap.Failed) != 0 {
		t.Fatalf("after Retry: pending=%d failed=%d, want 1/0", len(snap.Pending), len(snap.Failed))
	}
	if snap.Pending[0].ID != id {
		t.Fatalf("retried job id = %s, want %s", snap.Pending[0].ID, id)
	}

	// Retrying something not in failed/ is ErrNotExist.
	if err := s.Retry("missing"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Retry(missing) = %v, want ErrNotExist", err)
	}
}
