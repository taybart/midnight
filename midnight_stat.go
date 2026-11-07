package lua

// midnight: `require` looks for each module on package.path, then the compiled
// file cache checks whether it changed: a few os.Stat calls per require. A
// server that makes a state per request (and requires the same modules in
// each) would stat the same files thousands of times a second, so results are
// kept for a moment: a changed file is still seen within StatCacheTTL.

import (
	"os"
	"sync"
	"time"
)

// StatCacheTTL is how long a file's stat result is reused (0, the default,
// turns it off).
var StatCacheTTL time.Duration

const statCacheMax = 4096

type statEntry struct {
	info os.FileInfo
	err  error
	at   time.Time
}

var statCache struct {
	sync.Mutex
	m map[string]statEntry
}

// statFile is os.Stat, cached for StatCacheTTL.
func statFile(path string) (os.FileInfo, error) {
	if StatCacheTTL <= 0 {
		return os.Stat(path)
	}
	now := time.Now()
	statCache.Lock()
	e, ok := statCache.m[path]
	statCache.Unlock()
	if ok && now.Sub(e.at) < StatCacheTTL {
		return e.info, e.err
	}
	info, err := os.Stat(path)
	statCache.Lock()
	if statCache.m == nil || len(statCache.m) >= statCacheMax {
		statCache.m = map[string]statEntry{}
	}
	statCache.m[path] = statEntry{info, err, now}
	statCache.Unlock()
	return info, err
}
