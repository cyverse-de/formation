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
	connects := 0
	p := &pool{
		conns:     map[string]*pooledConn{},
		stop:      make(chan struct{}),
		idleTTL:   idleTTL,
		now:       time.Now,
		connectFn: func(string) (*fs.FileSystem, error) { connects++; return nil, nil },
		releaseFn: func(*fs.FileSystem) {},
	}
	return p, &connects
}

func TestPoolReusesPerUserAndIsolates(t *testing.T) {
	p, connects := newTestPool(t, time.Hour)

	// Two acquires for alice reuse one connection.
	_, rel1, err := p.acquire("alice")
	if err != nil {
		t.Fatal(err)
	}
	_, rel2, err := p.acquire("alice")
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
	_, relBob, err := p.acquire("bob")
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

	rel1()
	rel2()
	relBob()
	if got := p.refs("alice"); got != 0 {
		t.Fatalf("alice refs after release = %d, want 0", got)
	}
}

func TestPoolEvictsIdleUnreferenced(t *testing.T) {
	p, _ := newTestPool(t, 10*time.Minute)
	now := time.Unix(1000, 0)
	p.now = func() time.Time { return now }

	_, release, err := p.acquire("alice")
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
	release()
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

func (p *pool) refs(user string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if c, ok := p.conns[user]; ok {
		return c.refs
	}
	return -1
}
