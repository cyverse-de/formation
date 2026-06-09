package datastore

import (
	"testing"
	"time"

	"github.com/cyverse/go-irodsclient/fs"
)

// newTestPool builds a pool whose connect/release are stubbed so tests never
// touch a real iRODS server and the janitor goroutine is not started.
func newTestPool(t *testing.T, idleTTL time.Duration) (*pool, *int) {
	t.Helper()
	return newTestPoolCap(t, idleTTL, 0)
}

func newTestPoolCap(t *testing.T, idleTTL time.Duration, maxConns int) (*pool, *int) {
	t.Helper()
	connects := 0
	p := &pool{
		conns:     map[string]*pooledConn{},
		stop:      make(chan struct{}),
		idleTTL:   idleTTL,
		maxConns:  maxConns,
		now:       time.Now,
		connectFn: func(string) (*fs.FileSystem, error) { connects++; return nil, nil },
		releaseFn: func(*fs.FileSystem) {},
	}
	return p, &connects
}

func TestPoolReusesPerUserAndIsolates(t *testing.T) {
	p, connects := newTestPool(t, time.Hour)

	// Two acquires for alice reuse one connection.
	c1, err := p.acquire("alice")
	if err != nil {
		t.Fatal(err)
	}
	c2, err := p.acquire("alice")
	if err != nil {
		t.Fatal(err)
	}
	if *connects != 1 {
		t.Fatalf("alice connected %d times, want 1", *connects)
	}
	if got := p.refs("alice"); got != 2 {
		t.Fatalf("alice refs = %d, want 2", got)
	}

	// A different user gets a distinct connection.
	cBob, err := p.acquire("bob")
	if err != nil {
		t.Fatal(err)
	}
	if *connects != 2 {
		t.Fatalf("connect count = %d, want 2 (alice + bob)", *connects)
	}
	if _, ok := p.conns["alice"]; !ok {
		t.Error("alice entry missing")
	}
	if _, ok := p.conns["bob"]; !ok {
		t.Error("bob entry missing")
	}

	c1.release()
	c2.release()
	cBob.release()
	if got := p.refs("alice"); got != 0 {
		t.Fatalf("alice refs after release = %d, want 0", got)
	}
}

func TestPoolEvictsIdleUnreferenced(t *testing.T) {
	p, _ := newTestPool(t, 10*time.Minute)
	now := time.Unix(1000, 0)
	p.now = func() time.Time { return now }

	c, err := p.acquire("alice")
	if err != nil {
		t.Fatal(err)
	}

	// Still referenced: not evicted even when "idle".
	now = now.Add(time.Hour)
	p.evictIdle()
	if _, ok := p.conns["alice"]; !ok {
		t.Fatal("referenced connection was evicted")
	}

	// Released but not yet idle long enough: kept.
	c.release()
	now = now.Add(time.Minute)
	p.evictIdle()
	if _, ok := p.conns["alice"]; !ok {
		t.Fatal("connection evicted before idle TTL elapsed")
	}

	// Released and idle past TTL: evicted.
	now = now.Add(11 * time.Minute)
	p.evictIdle()
	if _, ok := p.conns["alice"]; ok {
		t.Fatal("idle unreferenced connection was not evicted")
	}
}

func TestPoolEvictsLRUOverCap(t *testing.T) {
	p, _ := newTestPoolCap(t, time.Hour, 2)
	now := time.Unix(1000, 0)
	p.now = func() time.Time { return now }
	var closed []*fs.FileSystem
	p.releaseFn = func(f *fs.FileSystem) { closed = append(closed, f) }

	// Acquire and release three users in order; alice is least-recently-used.
	for _, u := range []string{"alice", "bob", "carol"} {
		c, err := p.acquire(u)
		if err != nil {
			t.Fatal(err)
		}
		c.release()
		now = now.Add(time.Minute)
	}

	if len(p.conns) != 2 {
		t.Fatalf("cached %d connections, want 2 (cap)", len(p.conns))
	}
	if _, ok := p.conns["alice"]; ok {
		t.Error("alice (LRU) should have been evicted")
	}
	if _, ok := p.conns["carol"]; !ok {
		t.Error("carol (MRU) should be retained")
	}
	if len(closed) != 1 {
		t.Fatalf("closed %d connections, want 1", len(closed))
	}
}

func TestPoolDoesNotEvictReferencedOverCap(t *testing.T) {
	p, _ := newTestPoolCap(t, time.Hour, 1)

	// Both held: the cap is temporarily exceeded rather than evicting a
	// connection that is still in use.
	cA, err := p.acquire("alice")
	if err != nil {
		t.Fatal(err)
	}
	cB, err := p.acquire("bob")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.conns) != 2 {
		t.Fatalf("cached %d connections, want 2 (both referenced)", len(p.conns))
	}

	// Once alice is released, the next over-cap insert can evict her.
	cA.release()
	cC, err := p.acquire("carol")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.conns["alice"]; ok {
		t.Error("released alice should have been evicted to honor the cap")
	}
	if _, ok := p.conns["bob"]; !ok {
		t.Error("referenced bob must be retained")
	}
	cB.release()
	cC.release()
}

func TestPoolDiscardReconnectsAndDefersClose(t *testing.T) {
	p, connects := newTestPool(t, time.Hour)
	closed := 0
	p.releaseFn = func(*fs.FileSystem) { closed++ }

	// Two concurrent leases share one connection.
	c1, err := p.acquire("alice")
	if err != nil {
		t.Fatal(err)
	}
	c2, err := p.acquire("alice")
	if err != nil {
		t.Fatal(err)
	}
	if *connects != 1 {
		t.Fatalf("connect count = %d, want 1", *connects)
	}

	// One lease hits a transport error and discards: the entry leaves the map
	// but the live connection is not closed while c2 still holds it.
	c1.discard()
	if _, ok := p.conns["alice"]; ok {
		t.Fatal("discarded connection should be removed from the cache")
	}
	if closed != 0 {
		t.Fatalf("connection closed while still referenced (closed=%d)", closed)
	}

	// The next acquire reconnects rather than reusing the dead connection.
	c3, err := p.acquire("alice")
	if err != nil {
		t.Fatal(err)
	}
	if *connects != 2 {
		t.Fatalf("connect count = %d, want 2 (reconnect after discard)", *connects)
	}

	// Releasing both holders of the discarded connection closes it exactly once.
	c1.release()
	if closed != 0 {
		t.Fatalf("closed too early after first release (closed=%d)", closed)
	}
	c2.release()
	if closed != 1 {
		t.Fatalf("discarded connection closed %d times, want 1", closed)
	}
	c3.release()
}

func (p *pool) refs(user string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if c, ok := p.conns[user]; ok {
		return c.refs
	}
	return -1
}
