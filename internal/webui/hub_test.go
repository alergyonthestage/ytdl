package webui

import (
	"sync"
	"testing"
)

// Regression: a client disconnecting while progress is being published must never
// panic. broadcast sends outside the hub lock, so closing the client channel on
// removal used to race into "send on closed channel" — raised from os/exec's
// stderr-copy goroutine, which no recover covers, taking the whole daemon down
// (and orphaning every sibling download) when a user simply closed the tab.
func TestHubAddRemoveDuringBroadcast(t *testing.T) {
	h := newHub()
	var wg sync.WaitGroup
	stop := make(chan struct{})

	wg.Add(1)
	go func() { // publisher, like a download worker
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				h.broadcast(sseMsg{event: "progress", data: []byte(`{}`)})
			}
		}
	}()

	for i := 0; i < 200; i++ { // tabs opening and closing
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := h.addClient()
			h.removeClient(c)
		}()
	}
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.clientCount()
		}()
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	close(stop)
	<-done

	if n := h.clientCount(); n != 0 {
		t.Errorf("clientCount = %d, want 0 after every client left", n)
	}
}

// A client whose buffer is full must be skipped, never block the publisher.
func TestHubBroadcastDropsForStalledClient(t *testing.T) {
	h := newHub()
	c := h.addClient()
	for i := 0; i < cap(c.ch)+10; i++ { // overflow the mailbox
		h.broadcast(sseMsg{event: "progress", data: []byte(`{}`)})
	}
	if len(c.ch) != cap(c.ch) {
		t.Errorf("mailbox = %d, want it capped at %d", len(c.ch), cap(c.ch))
	}
}
