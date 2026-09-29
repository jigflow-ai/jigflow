package cli

import (
	"fmt"
	"hash/fnv"
	"io/fs"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jigflow-ai/jigflow/internal/playbook"
)

// The Dashboard's pages update themselves (ADR 0025): each listens to a
// server-sent event stream on which jfl ui says only that the project
// changed, and reloads. While at least one page listens, jfl ui looks over
// what the pages show for changes; while none does, it looks at nothing.

// filesEvery is how often jfl ui looks over .jigflow/ while a page listens.
const filesEvery = time.Second

// look is one thing the Dashboard's pages show that can change, and how
// often to look at it: fingerprint says what it is like now, so that a
// different fingerprint means it changed. The project's files are one; a
// tracker Store, asked less often, can be another.
type look struct {
	every       time.Duration
	fingerprint func() string
}

// changes tells the pages listening when what they show changed.
type changes struct {
	looks []look

	mu        sync.Mutex
	listeners map[chan struct{}]bool
	stop      chan struct{} // closed when the last listener leaves
}

func newChanges(looks ...look) *changes {
	return &changes{looks: looks, listeners: map[chan struct{}]bool{}}
}

// version says what everything the pages show is like now, so that a page
// can say, when it starts listening, what it showed.
func (c *changes) version() string {
	return versionOf(c.see())
}

// see is what each look sees now.
func (c *changes) see() []string {
	seen := make([]string, len(c.looks))
	for i, l := range c.looks {
		seen[i] = l.fingerprint()
	}
	return seen
}

func versionOf(seen []string) string {
	h := fnv.New64a()
	for _, s := range seen {
		fmt.Fprintf(h, "%d:%s", len(s), s)
	}
	return fmt.Sprintf("%016x", h.Sum64())
}

// listen returns a channel on which a listener is told that something
// changed since the version it showed, or since it started listening
// when it says none, and the function that stops it.
// Changes made meanwhile are told once: the channel holds one signal.
func (c *changes) listen(since string) (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	c.mu.Lock()
	defer c.mu.Unlock()
	seen := c.see()
	if since != "" && versionOf(seen) != since {
		// Changed between the page being shown and its listening.
		ch <- struct{}{}
	}
	if len(c.listeners) == 0 {
		// What each look sees now is where changes are told from.
		c.stop = make(chan struct{})
		for i, l := range c.looks {
			go c.watch(l, seen[i], c.stop)
		}
	}
	c.listeners[ch] = true
	return ch, func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if !c.listeners[ch] {
			return
		}
		delete(c.listeners, ch)
		if len(c.listeners) == 0 {
			close(c.stop)
		}
	}
}

// watch looks again every l.every, from what it saw last, and tells every
// listener when it sees a change, until stop is closed.
func (c *changes) watch(l look, last string, stop <-chan struct{}) {
	tick := time.NewTicker(l.every)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
		}
		now := l.fingerprint()
		if now == last {
			continue
		}
		last = now
		c.mu.Lock()
		select {
		case <-stop:
			// Stopped meanwhile: listeners that came since are told
			// from what the watch that started for them sees.
			c.mu.Unlock()
			return
		default:
		}
		for ch := range c.listeners {
			select {
			case ch <- struct{}{}:
			default: // already told, and not yet passed on
			}
		}
		c.mu.Unlock()
	}
}

// projectFiles looks over the project's .jigflow/ folder, where jfl writes
// the Playbook, the Artifacts kept in files, Proposals, Sessions and the
// Ledger: the name, size and modification time of each file, read with the
// standard library only.
func projectFiles(dir string) look {
	root := filepath.Join(dir, playbook.Dir)
	cache := filepath.Join(root, "cache")
	return look{every: filesEvery, fingerprint: func() string {
		var b strings.Builder
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				fmt.Fprintf(&b, "%s: %v\n", path, err)
				return nil
			}
			if d.IsDir() {
				if path == cache {
					// Checkouts of git Base Playbooks, fetched once per
					// commit the Playbook pins, which the lock file names.
					return fs.SkipDir
				}
				return nil
			}
			info, err := d.Info()
			if err != nil {
				fmt.Fprintf(&b, "%s: %v\n", path, err)
				return nil
			}
			fmt.Fprintf(&b, "%s %d %d\n", path, info.Size(), info.ModTime().UnixNano())
			return nil
		})
		return b.String()
	}}
}

// stream is the change stream a page's script listens to, saying the
// version it showed. It carries no content, only that something changed,
// so it needs no key (ADR 0017), and it ends when the page goes away or
// jfl ui stops.
func (d *dashboard) stream(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	// The stream lasts as long as the page: no write deadline ends it.
	_ = rc.SetWriteDeadline(time.Time{})
	changed, stop := d.changes.listen(r.URL.Query().Get("since"))
	defer stop()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if err := rc.Flush(); err != nil {
		return
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case <-d.closing:
			return
		case <-changed:
		}
		if _, err := fmt.Fprint(w, "data: changed\n\n"); err != nil {
			return
		}
		if err := rc.Flush(); err != nil {
			return
		}
	}
}
