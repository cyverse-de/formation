package datastore

import (
	"sync"
	"time"

	"github.com/cyverse/go-irodsclient/fs"
	"github.com/cyverse/go-irodsclient/irods/types"
)

// pool caches one proxied iRODS connection per impersonated user, reusing it
// across that user's (possibly concurrent) requests and closing it once it has
// been idle and unreferenced for idleTTL.
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
	fs       *fs.FileSystem
	refs     int
	lastUsed time.Time
}

func newPool(cfg Config, idleTTL time.Duration) *pool {
	p := &pool{
		host:      cfg.Host,
		port:      cfg.Port,
		proxyUser: cfg.User,
		password:  cfg.Password,
		zone:      cfg.Zone,
		idleTTL:   idleTTL,
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

// acquire returns a FileSystem impersonating username and a release function the
// caller must invoke when done. The connection is created on first use and
// shared thereafter.
func (p *pool) acquire(username string) (*fs.FileSystem, func(), error) {
	p.mu.Lock()
	if c, ok := p.conns[username]; ok {
		c.refs++
		c.lastUsed = p.now()
		p.mu.Unlock()
		return c.fs, p.releaser(username), nil
	}
	p.mu.Unlock()

	// Create outside the lock (it performs network I/O). A concurrent acquire
	// for the same user may create a duplicate; the loser is released below.
	fsys, err := p.connectFn(username)
	if err != nil {
		return nil, nil, err
	}

	p.mu.Lock()
	if c, ok := p.conns[username]; ok {
		c.refs++
		c.lastUsed = p.now()
		p.mu.Unlock()
		p.releaseFn(fsys)
		return c.fs, p.releaser(username), nil
	}
	if p.stopped {
		// Pool was closed while we were connecting; don't cache.
		p.mu.Unlock()
		return fsys, func() { p.releaseFn(fsys) }, nil
	}
	p.conns[username] = &pooledConn{fs: fsys, refs: 1, lastUsed: p.now()}
	p.mu.Unlock()
	return fsys, p.releaser(username), nil
}

func (p *pool) releaser(username string) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			p.mu.Lock()
			if c, ok := p.conns[username]; ok {
				c.refs--
				c.lastUsed = p.now()
			}
			p.mu.Unlock()
		})
	}
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
	p.mu.Lock()
	defer p.mu.Unlock()
	for user, c := range p.conns {
		if c.refs == 0 && now.Sub(c.lastUsed) >= p.idleTTL {
			p.releaseFn(c.fs)
			delete(p.conns, user)
		}
	}
}

// close stops the janitor and releases all cached connections.
func (p *pool) close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped {
		return
	}
	p.stopped = true
	close(p.stop)
	for user, c := range p.conns {
		p.releaseFn(c.fs)
		delete(p.conns, user)
	}
}
