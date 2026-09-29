package cli

import (
	"errors"
	"fmt"
	"hash/fnv"
	"io/fs"
	"maps"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jigflow-ai/jigflow/internal/engine"
	"github.com/jigflow-ai/jigflow/internal/playbook"
	"github.com/jigflow-ai/jigflow/internal/store"
)

// The Dashboard's pages update themselves (ADR 0025): each listens to a
// server-sent event stream on which jfl ui says only that the project
// changed, and reloads. While at least one page listens, jfl ui looks over
// what the pages show for changes; while none does, it looks at nothing.

// filesEvery is how often jfl ui looks over .jigflow/ while a page listens.
const filesEvery = time.Second

// trackerEvery is how often jfl ui asks the tracker Stores while a page
// listens: each ask counts against the tracker's rate limit, and changes
// made through jfl land in .jigflow/ and show at once (ADR 0025).
const trackerEvery = 30 * time.Second

// trackerEveryEnv names the environment variable that sets trackerEvery, as
// a Go duration. It is empty in a shipped jfl: only the tests' build sets
// it (-ldflags -X), so that they needn't wait 30 seconds.
var trackerEveryEnv string

// trackerInterval is how often jfl ui asks the tracker Stores: every
// trackerEvery, or as often as trackerEveryEnv sets in a test build.
func trackerInterval(getenv func(string) string) (time.Duration, error) {
	if trackerEveryEnv == "" || getenv(trackerEveryEnv) == "" {
		return trackerEvery, nil
	}
	d, err := time.ParseDuration(getenv(trackerEveryEnv))
	if err == nil && d <= 0 {
		err = errors.New("not a positive duration")
	}
	if err != nil {
		return 0, fmt.Errorf("%s: %w", trackerEveryEnv, err)
	}
	return d, nil
}

// look is one thing the Dashboard's pages show that can change: fingerprint
// says what it is like now, so that a different fingerprint means it
// changed, and due how long to wait before looking at it again. The
// project's files are one; the tracker Stores, asked less often, another.
type look struct {
	fingerprint func() string
	due         func() time.Duration
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
	// Unlocked: looking may take a while, as asking a tracker does.
	seen := c.see()
	c.mu.Lock()
	defer c.mu.Unlock()
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

// watch looks again whenever l is due, from what it saw last, and tells
// every listener when it sees a change, until stop is closed.
func (c *changes) watch(l look, last string, stop <-chan struct{}) {
	for {
		wait := time.NewTimer(l.due())
		select {
		case <-stop:
			wait.Stop()
			return
		case <-wait.C:
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
// Ledger, and the Mockup folder the Playbook declares, wherever it is: the
// name, size and modification time of each file, read with the standard
// library only.
func projectFiles(dir string) look {
	root := filepath.Join(dir, playbook.Dir)
	cache := filepath.Join(root, "cache")
	return look{due: func() time.Duration { return filesEvery }, fingerprint: func() string {
		var b strings.Builder
		walkFiles(&b, root, cache)
		// The Mockup folder, where the Playbook declares one outside .jigflow/.
		if pb, err := playbook.Load(dir); err == nil && pb.Mockups != "" && !strings.HasPrefix(pb.Mockups+"/", playbook.Dir+"/") {
			walkFiles(&b, filepath.Join(dir, filepath.FromSlash(pb.Mockups)), "")
		}
		return b.String()
	}}
}

// walkFiles writes to b what each file under root is like, skipping the
// directory skip.
func walkFiles(b *strings.Builder, root, skip string) {
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			fmt.Fprintf(b, "%s: %v\n", path, err)
			return nil
		}
		if d.IsDir() {
			if path == skip {
				// Checkouts of git Base Playbooks, fetched once per
				// commit the Playbook pins, which the lock file names.
				return fs.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			fmt.Fprintf(b, "%s: %v\n", path, err)
			return nil
		}
		fmt.Fprintf(b, "%s %d %d\n", path, info.Size(), info.ModTime().UnixNano())
		return nil
	})
}

// trackers asks the tracker Stores of the project's Playbook, through their
// Connectors, what they list: what each item is like, as jfl reads it, and
// so the Status, labels and new items a person changed in the tracker. Each
// ask counts against the tracker's rate limit, so they are asked at most
// once every so often, whether for a page being shown, a page starting to
// listen or the watch, and are otherwise seen as last asked.
func trackers(dir string, every time.Duration) look {
	t := &trackerAsks{dir: dir, every: every, kept: map[string]string{}}
	return look{fingerprint: t.fingerprint, due: t.due}
}

// trackerAsks is what the tracker Stores listed when last asked.
type trackerAsks struct {
	dir   string
	every time.Duration

	mu    sync.Mutex // one ask at a time
	asked time.Time  // when last asked; zero before
	seen  string     // what they listed then
	// kept is what each Connector listed when last it answered, which
	// stands for what it lists while it can't be reached: a tracker that
	// can't be reached is no change.
	kept map[string]string
}

func (t *trackerAsks) fingerprint() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.asked.IsZero() && time.Since(t.asked) < t.every {
		return t.seen
	}
	t.asked = time.Now()
	pb, err := playbook.Load(t.dir)
	if err != nil {
		// The pages say why the Playbook doesn't load, and the project's
		// files the change that mends it.
		return t.seen
	}
	var b strings.Builder
	for _, name := range slices.Sorted(maps.Keys(pb.Connectors)) {
		if kept, err := store.NewConnector(t.dir, pb, pb.Connectors[name]).List(); err == nil {
			slices.SortFunc(kept, func(x, y engine.Artifact) int { return strings.Compare(x.ID, y.ID) })
			var items strings.Builder
			for _, a := range kept {
				// fmt prints maps sorted by key; quoted, a title's line
				// break can't run into the next item.
				fmt.Fprintf(&items, "%q\n", fmt.Sprintf("%+v", a))
			}
			t.kept[name] = items.String()
		}
		fmt.Fprintf(&b, "%s %d:%s", name, len(t.kept[name]), t.kept[name])
	}
	t.seen = b.String()
	return t.seen
}

// due is how long until the trackers may be asked again.
func (t *trackerAsks) due() time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	return max(0, t.every-time.Since(t.asked))
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
