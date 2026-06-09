package datastore

import (
	"sync"
	"time"

	"github.com/cyverse/go-irodsclient/fs"
	"github.com/cyverse/go-irodsclient/irods/types"
)

// pool caches one proxied iRODS connection per impersonated user, reusing it
// across that user's (possibly concurrent) requests and closing it once it has
// been idle and unreferenced for idleTTL. The number of cached connections is
// bounded by maxConns: when exceeded, the least-recently-used unreferenced
// connection is evicted. A connection that fails with a transport error is
// discarded so the next request reconnects.
//
// Entries are keyed by the validated downstream username, which is the security
// principal: a caller can only ever obtain a connection whose iRODS client user
// is their own authenticated identity, so the cache cannot leak one user's
// connection to another.
type pool struct {
	host      string
	port      int
	proxyUser string
	password  string
	zone      string
	idleTTL   time.Duration
	maxConns  int // 0 means unbounded

	mu      sync.Mutex
	conns   map[string]*pooledConn
	stop    chan struct{}
	stopped bool

	// now, connectFn, and releaseFn are injectable for testing.
	now       func() time.Time
	connectFn func(username string) (*fs.FileSystem, error)
	releaseFn func(*fs.FileSystem)
}

type pooledConn struct {
	user     string
	fs       *fs.FileSystem
	refs     int
	lastUsed time.Time
	// detached marks an entry removed from the map (evicted or discarded). Its
	// underlying connection is closed once the last in-flight ref is released.
	detached bool
}

// conn is a leased connection handle. Callers use fs, then must call release.
// classify calls discard when an operation fails with a transport error.
type conn struct {
	fs   *fs.FileSystem
	pc   *pooledConn
	pool *pool
	once sync.Once
}

func (c *conn) release() {
	c.once.Do(func() { c.pool.release(c.pc) })
}

func (c *conn) discard() {
	c.pool.discard(c.pc)
}

func newPool(cfg Config, idleTTL time.Duration, maxConns int) *pool {
	p := &pool{
		host:      cfg.Host,
		port:      cfg.Port,
		proxyUser: cfg.User,
		password:  cfg.Password,
		zone:      cfg.Zone,
		idleTTL:   idleTTL,
		maxConns:  maxConns,
		conns:     map[string]*pooledConn{},
		stop:      make(chan struct{}),
		now:       time.Now,
	}
	p.connectFn = p.connect
	p.releaseFn = releaseFS
	go p.janitor()
	return p
}

func releaseFS(f *fs.FileSystem) {
	if f != nil {
		f.Release()
	}
}

// connect opens a new iRODS connection that authenticates as the proxy user but
// acts as the given client user.
func (p *pool) connect(username string) (*fs.FileSystem, error) {
	account, err := types.CreateIRODSProxyAccount(
		p.host, p.port,
		username, p.zone, // client user / zone
		p.proxyUser, p.zone, // proxy user / zone
		types.AuthSchemeNative, p.password, "",
	)
	if err != nil {
		return nil, err
	}
	return fs.NewFileSystemWithDefault(account, "formation")
}

// acquire returns a leased FileSystem impersonating username. The caller must
// invoke conn.release when done. The connection is created on first use and
// shared thereafter.
func (p *pool) acquire(username string) (*conn, error) {
	p.mu.Lock()
	if pc, ok := p.conns[username]; ok {
		pc.refs++
		pc.lastUsed = p.now()
		p.mu.Unlock()
		return &conn{fs: pc.fs, pc: pc, pool: p}, nil
	}
	p.mu.Unlock()

	// Create outside the lock (it performs network I/O). A concurrent acquire
	// for the same user may create a duplicate; the loser is released below.
	fsys, err := p.connectFn(username)
	if err != nil {
		return nil, err
	}

	p.mu.Lock()
	if pc, ok := p.conns[username]; ok {
		pc.refs++
		pc.lastUsed = p.now()
		p.mu.Unlock()
		p.releaseFn(fsys)
		return &conn{fs: pc.fs, pc: pc, pool: p}, nil
	}
	if p.stopped {
		// Pool was closed while we were connecting; hand back a detached entry
		// so release closes it rather than caching it.
		p.mu.Unlock()
		pc := &pooledConn{user: username, fs: fsys, refs: 1, detached: true}
		return &conn{fs: fsys, pc: pc, pool: p}, nil
	}
	pc := &pooledConn{user: username, fs: fsys, refs: 1, lastUsed: p.now()}
	p.conns[username] = pc
	evicted := p.evictOverCapLocked()
	p.mu.Unlock()
	for _, f := range evicted {
		p.releaseFn(f)
	}
	return &conn{fs: fsys, pc: pc, pool: p}, nil
}

// release returns a ref. When the last ref of a detached entry is returned, its
// connection is closed.
func (p *pool) release(pc *pooledConn) {
	p.mu.Lock()
	pc.refs--
	pc.lastUsed = p.now()
	closeNow := pc.detached && pc.refs <= 0
	p.mu.Unlock()
	if closeNow {
		p.releaseFn(pc.fs)
	}
}

// discard removes a connection from the cache so subsequent acquires reconnect.
// The underlying connection is closed once its last in-flight ref is released
// (closing it now could race concurrent callers sharing the same FileSystem).
func (p *pool) discard(pc *pooledConn) {
	p.mu.Lock()
	if pc.detached {
		p.mu.Unlock()
		return
	}
	pc.detached = true
	if cur, ok := p.conns[pc.user]; ok && cur == pc {
		delete(p.conns, pc.user)
	}
	closeNow := pc.refs <= 0
	p.mu.Unlock()
	if closeNow {
		p.releaseFn(pc.fs)
	}
}

// evictOverCapLocked detaches least-recently-used unreferenced entries until the
// cache is within maxConns, returning their connections to close after the lock
// is released. Referenced entries are never evicted, so the cache can briefly
// exceed the cap when every entry is in active use. Caller must hold p.mu.
func (p *pool) evictOverCapLocked() []*fs.FileSystem {
	if p.maxConns <= 0 || len(p.conns) <= p.maxConns {
		return nil
	}
	var closing []*fs.FileSystem
	for len(p.conns) > p.maxConns {
		var victim *pooledConn
		for _, pc := range p.conns {
			if pc.refs != 0 {
				continue
			}
			if victim == nil || pc.lastUsed.Before(victim.lastUsed) {
				victim = pc
			}
		}
		if victim == nil {
			break
		}
		victim.detached = true
		delete(p.conns, victim.user)
		closing = append(closing, victim.fs)
	}
	return closing
}

func (p *pool) janitor() {
	interval := p.idleTTL / 2
	if interval <= 0 {
		interval = time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-p.stop:
			return
		case <-ticker.C:
			p.evictIdle()
		}
	}
}

// evictIdle closes and removes connections that are unreferenced and have been
// idle for at least idleTTL.
func (p *pool) evictIdle() {
	now := p.now()
	var closing []*fs.FileSystem
	p.mu.Lock()
	for user, c := range p.conns {
		if c.refs == 0 && now.Sub(c.lastUsed) >= p.idleTTL {
			c.detached = true
			delete(p.conns, user)
			closing = append(closing, c.fs)
		}
	}
	p.mu.Unlock()
	for _, f := range closing {
		p.releaseFn(f)
	}
}

// close stops the janitor and releases all cached connections.
func (p *pool) close() {
	var closing []*fs.FileSystem
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return
	}
	p.stopped = true
	close(p.stop)
	for user, c := range p.conns {
		c.detached = true
		delete(p.conns, user)
		closing = append(closing, c.fs)
	}
	p.mu.Unlock()
	for _, f := range closing {
		p.releaseFn(f)
	}
}
