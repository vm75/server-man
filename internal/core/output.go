package core

import "sync"

// Tail retains the newest bytes without blocking command output readers.
type Tail struct {
	mu        sync.Mutex
	data      []byte
	limit     int
	truncated bool
}

func NewTail(limit int) *Tail { return &Tail{limit: max(1, limit)} }

func (t *Tail) Write(data []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := len(data)
	if n >= t.limit {
		previous := len(t.data)
		t.data = append(t.data[:0], data[n-t.limit:]...)
		t.truncated = t.truncated || n > t.limit || previous > 0
	} else {
		if excess := len(t.data) + n - t.limit; excess > 0 {
			copy(t.data, t.data[excess:])
			t.data = t.data[:len(t.data)-excess]
			t.truncated = true
		}
		t.data = append(t.data, data...)
	}
	return n, nil
}

func (t *Tail) Snapshot() (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.data), t.truncated
}
