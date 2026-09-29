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

// The Dashboard's pages update themselves (ADR 0025): each asks jfl ui,
// every 2 seconds while it is visible, whether the project changed since
// the version it shows (ADR 0029). jfl ui works the version out when asked,
// at most once a second however many pages ask, and does nothing between
// requests: with no page asking, it looks at nothing.

// filesEvery is how often, at most, jfl ui looks over the project's files
// and git, however many pages ask.
const filesEvery = time.Second

// trackerEvery is how often, at most, jfl ui asks the tracker Stores: each
// ask counts against the tracker's rate limit, and changes made through
// jfl land in .jigflow/ and show at once (ADR 0025).
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

// look is one thing the Dashboard's pages show that can change: what it is
// like now, so that a different fingerprint means it changed. The project's
// files, git and the tracker Stores, asked less often, are each one.
type look func() string

// changes says what everything the pages show is like, as a version a page
// keeps and asks about.
type changes struct {
	looks []look
	every time.Duration // how often, at most, the looks are looked at

	mu     sync.Mutex // one look at a time, shared by every request
	looked time.Time  // when last looked; zero before
	last   string     // the version seen then
}

func newChanges(looks ...look) *changes {
	return &changes{looks: looks, every: filesEvery}
}

// version says what everything the pages show is like now, looking again
// only when it last looked a while ago: requests meanwhile, from however
// many pages, share what it saw.
func (c *changes) version() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.looked.IsZero() && time.Since(c.looked) < c.every {
		return c.last
	}
	h := fnv.New64a()
	for _, l := range c.looks {
		s := l()
		fmt.Fprintf(h, "%d:%s", len(s), s)
	}
	c.looked, c.last = time.Now(), fmt.Sprintf("%016x", h.Sum64())
	return c.last
}

// projectFiles looks over the project's .jigflow/ folder, where jfl writes
// the Playbook, the Artifacts kept in files, Proposals, Sessions and the
// Ledger, and the Mockup folder the Playbook declares, wherever it is: the
// name, size and modification time of each file, read with the standard
// library only.
func projectFiles(dir string) look {
	root := filepath.Join(dir, playbook.Dir)
	cache := filepath.Join(root, "cache")
	return func() string {
		var b strings.Builder
		walkFiles(&b, root, cache)
		// The Mockup folder, where the Playbook declares one outside .jigflow/.
		if pb, err := playbook.Load(dir); err == nil && pb.Mockups != "" && !strings.HasPrefix(pb.Mockups+"/", playbook.Dir+"/") {
			walkFiles(&b, filepath.Join(dir, filepath.FromSlash(pb.Mockups)), "")
		}
		return b.String()
	}
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
// once every so often, whether for a page being shown or a page asking
// whether the project changed, and are otherwise seen as last asked.
func trackers(dir string, every time.Duration) look {
	t := &trackerAsks{dir: dir, every: every, kept: map[string]string{}}
	return t.fingerprint
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

// ask answers a page asking whether the project changed since the version
// it shows: at once, 204 when nothing did and 200 when something did. It
// carries no content, only whether something changed, so it needs no key
// (ADR 0017). A page shown before ADR 0029 asks for an event stream: it is
// told once that the project changed, and so loads again into one that
// asks, and the stream closes.
func (d *dashboard) ask(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: changed\n\n")
		return
	}
	if r.URL.Query().Get("since") == d.changes.version() {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.WriteHeader(http.StatusOK)
}
