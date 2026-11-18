package pages

import (
	"fmt"
	"maps"
	"net/http"
	"strings"
	"sync"
	"time"

	"log"
)

// Cache keeps a page's rendered responses for ttl, so a page that's the same
// for every visitor is rendered once, not on every load. Responses are kept
// by path, query, and the request headers listed in vary (ex.
// "Accept-Language"): it's a promise that the page only depends on those
// (pages can't tell: a page can read cookies, Go functions, the time, ...).
//
//   - Only 200s are kept, and not ones that set cookies.
//   - Interactive pages (with actions) are never kept: their html has the id of
//     the visitor's own state. (A warning is logged, once.)
//   - Responses get Cache-Control: public, max-age=<ttl> (unless the page
//     sets its own), so a CDN or proxy can cache them too.
//   - At most 1000 responses / 64MB per page, the oldest are dropped first.
//
// Router middleware still runs for cached responses (ex. auth), it's outside
// of the page. For compression, use the router's middleware too.
func Cache(ttl time.Duration, vary ...string) Option {
	return func(h *handler) {
		h.cache = &responseCache{ttl: ttl, vary: vary, entries: map[string]cachedResponse{}}
	}
}

const (
	cacheEntries = 1000
	cacheBytes   = 64 << 20
)

type responseCache struct {
	ttl  time.Duration
	vary []string

	mu      sync.Mutex
	entries map[string]cachedResponse
	bytes   int // of the entries' bodies
	warned  sync.Once
}

type cachedResponse struct {
	res     Response
	expires time.Time
}

// key is the request's path, query (sorted), and vary headers.
func (c *responseCache) key(r *http.Request) string {
	var b strings.Builder
	b.WriteString(r.URL.Path)
	b.WriteByte('?')
	b.WriteString(r.URL.Query().Encode())
	for _, name := range c.vary {
		b.WriteString("\n" + strings.ToLower(name) + ": " + strings.Join(r.Header.Values(name), ", "))
	}
	return b.String()
}

func (c *responseCache) get(key string) (Response, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok {
		return Response{}, false
	}
	if !time.Now().Before(e.expires) {
		c.remove(key)
		return Response{}, false
	}
	res := e.res
	res.Headers = maps.Clone(e.res.Headers) // (Body is never written to)
	return res, true
}

// store keeps res if it can be, adding Cache-Control to it.
func (c *responseCache) store(r *http.Request, key string, res *Response, interactive bool) {
	if interactive {
		c.warned.Do(func() {
			log.Printf("pages: %s: not cached, it's an interactive page (its html is for one visitor)", r.URL.Path)
		})
		return
	}
	if res.Status != http.StatusOK || len(res.Cookies) > 0 || len(res.Body) > cacheBytes {
		return
	}
	res.SetDefaultHeader("Cache-Control", fmt.Sprintf("public, max-age=%d", int(c.ttl.Seconds())))

	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	if _, ok := c.entries[key]; ok {
		c.remove(key)
	}
	for k, e := range c.entries {
		if !now.Before(e.expires) {
			c.remove(k)
		}
	}
	// the oldest expire first (they all have the same ttl)
	for len(c.entries) >= cacheEntries || c.bytes+len(res.Body) > cacheBytes {
		oldest, first := "", true
		for k, e := range c.entries {
			if first || e.expires.Before(c.entries[oldest].expires) {
				oldest, first = k, false
			}
		}
		c.remove(oldest)
	}
	stored := *res
	stored.Headers = maps.Clone(res.Headers)
	c.entries[key] = cachedResponse{res: stored, expires: now.Add(c.ttl)}
	c.bytes += len(stored.Body)
}

func (c *responseCache) remove(key string) {
	c.bytes -= len(c.entries[key].res.Body)
	delete(c.entries, key)
}
