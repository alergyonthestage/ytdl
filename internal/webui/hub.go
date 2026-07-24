package webui

import "sync"

// sseMsg is one server-sent event: a named event type and its already-encoded
// JSON payload.
type sseMsg struct {
	event string
	data  []byte
}

// hubClient is one connected browser's mailbox. The SSE handler goroutine owns
// the reading end; broadcast() is the only writer. The channel is buffered and
// broadcast drops on overflow, so one slow/stalled client never blocks the
// daemon's job goroutines that publish progress.
//
// The channel is deliberately NEVER closed: broadcast sends outside the hub lock,
// so closing on disconnect would race with an in-flight send and panic with "send
// on closed channel" — inside os/exec's stderr-copy goroutine, which no recover
// covers, killing the whole daemon and orphaning every sibling download. A
// removed client is unreachable (it is out of the map) and its channel is simply
// garbage-collected; the reader exits on the request context instead.
type hubClient struct {
	ch chan sseMsg
}

// hub is the SSE fan-out: a registry of connected clients plus the client count
// that feeds the daemon's "GUI connected" exit clause (ADR-0008). It holds no
// per-job progress state — progress is forwarded live, and the queue snapshot
// (polled by each client) is the source of truth for what is running — so it
// cannot grow unbounded.
type hub struct {
	mu      sync.Mutex
	clients map[*hubClient]struct{}
}

func newHub() *hub { return &hub{clients: map[*hubClient]struct{}{}} }

func (h *hub) clientCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

func (h *hub) addClient() *hubClient {
	c := &hubClient{ch: make(chan sseMsg, 64)}
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
	return c
}

func (h *hub) removeClient(c *hubClient) {
	h.mu.Lock()
	delete(h.clients, c) // never close(c.ch) — see the hubClient doc comment
	h.mu.Unlock()
}

// broadcast delivers msg to every client, dropping it for any client whose
// buffer is full (a stalled reader) — progress is coalesced by later updates, so
// a dropped frame is harmless and must never block a publisher.
func (h *hub) broadcast(msg sseMsg) {
	h.mu.Lock()
	targets := make([]*hubClient, 0, len(h.clients))
	for c := range h.clients {
		targets = append(targets, c)
	}
	h.mu.Unlock()
	for _, c := range targets {
		select {
		case c.ch <- msg:
		default:
		}
	}
}
