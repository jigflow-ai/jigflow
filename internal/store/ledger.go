package store

import (
	"cmp"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/jigflow-ai/jigflow/internal/engine"
	"go.yaml.in/yaml/v3"
)

// LedgerDir is the project-relative directory of the Ledger. It is
// committed, whichever Store keeps the Artifacts, so the whole team shares
// one record. Each entry is a file of its own, named by when it was written
// and by the process that wrote it:
//
//	.jigflow/ledger/<time>-<writer>-<n>.yaml
//
// Entries are only ever added, never changed, so parallel sessions, on one
// machine or on branches merged later, never write the same file and never
// conflict.
const LedgerDir = ".jigflow/ledger"

// Ledger is the Ledger of one project, which one process writes to.
type Ledger struct {
	dir    string
	writer string // this process's, random, so no two processes name an entry alike
	n      int    // entries written so far, keeping one process's entries in order
}

// NewLedger returns the Ledger of the project rooted at root.
func NewLedger(root string) *Ledger {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return &Ledger{dir: filepath.Join(root, LedgerDir), writer: hex.EncodeToString(b)}
}

// ledgerEntry is the on-disk form of an entry: a Status change, a Focus
// change, or the usage one collection read from an agent's records.
type ledgerEntry struct {
	At time.Time `yaml:"at"`
	// A Status change.
	Artifact string `yaml:"artifact,omitempty"`
	Type     string `yaml:"type,omitempty"`
	Title    string `yaml:"title,omitempty"`
	From     string `yaml:"from,omitempty"`
	To       string `yaml:"to,omitempty"`
	// A Focus change.
	Session string `yaml:"session,omitempty"`
	Focus   string `yaml:"focus,omitempty"`
	// Usage, with Session.
	Agent string       `yaml:"agent,omitempty"`
	Usage []usageEntry `yaml:"usage,omitempty"`
}

// usageEntry is the on-disk form of one message's usage.
type usageEntry struct {
	Message    string    `yaml:"message"`
	At         time.Time `yaml:"at"`
	Input      int64     `yaml:"input,omitempty"`
	Output     int64     `yaml:"output,omitempty"`
	CacheRead  int64     `yaml:"cache_read,omitempty"`
	CacheWrite int64     `yaml:"cache_write,omitempty"`
}

// RecordStatus adds a Status change to the Ledger.
func (l *Ledger) RecordStatus(c engine.StatusChange) error {
	return l.write(ledgerEntry{At: c.At, Artifact: c.Artifact, Type: c.Type, Title: c.Title, From: c.From, To: c.To})
}

// RecordFocus adds a Focus change to the Ledger.
func (l *Ledger) RecordFocus(f engine.FocusChange) error {
	return l.write(ledgerEntry{At: f.At, Session: f.Session, Focus: f.Focus})
}

// RecordUsage adds the usage of an agent session's messages, read at the
// time at from its agent's records, to the Ledger, as one entry. Every
// message must be of the one session and agent.
func (l *Ledger) RecordUsage(at time.Time, us []engine.Usage) error {
	if len(us) == 0 {
		return nil
	}
	e := ledgerEntry{At: at, Session: us[0].Session, Agent: us[0].Agent}
	for _, u := range us {
		if u.Session != e.Session || u.Agent != e.Agent {
			return fmt.Errorf("one Ledger entry records the usage of one session: %s of %s, then %s of %s", e.Session, e.Agent, u.Session, u.Agent)
		}
		e.Usage = append(e.Usage, usageEntry{Message: u.Message, At: u.At.UTC(), Input: u.Input, Output: u.Output, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite})
	}
	return l.write(e)
}

func (l *Ledger) write(e ledgerEntry) error {
	if err := os.MkdirAll(l.dir, 0o755); err != nil {
		return err
	}
	l.n++
	e.At = e.At.UTC()
	name := fmt.Sprintf("%s-%s-%04d.yaml", e.At.Format("20060102T150405.000000000Z"), l.writer, l.n)
	data, err := yaml.Marshal(e)
	if err != nil {
		return err
	}
	// O_EXCL: an entry, once written, is never overwritten.
	f, err := os.OpenFile(filepath.Join(l.dir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Read returns every entry in the Ledger, each kind in the order they
// happened.
func (l *Ledger) Read() (engine.Ledger, error) {
	var out engine.Ledger
	dirents, err := os.ReadDir(l.dir)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	type named struct {
		name string
		ledgerEntry
	}
	var entries []named
	for _, d := range dirents {
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".yaml") {
			continue
		}
		path := filepath.Join(l.dir, d.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return out, err
		}
		var e ledgerEntry
		if err := yaml.Unmarshal(data, &e); err != nil {
			return out, fmt.Errorf("%s: %w", path, err)
		}
		entries = append(entries, named{d.Name(), e})
	}
	// By time, then by name, which keeps one process's entries written at
	// the same time in the order it wrote them.
	slices.SortFunc(entries, func(x, y named) int {
		return cmp.Or(x.At.Compare(y.At), cmp.Compare(x.name, y.name))
	})
	for _, e := range entries {
		switch {
		case len(e.Usage) > 0:
			for _, u := range e.Usage {
				out.Usages = append(out.Usages, engine.Usage{At: u.At, Session: e.Session, Agent: e.Agent, Message: u.Message,
					Tokens: engine.Tokens{Input: u.Input, Output: u.Output, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite}})
			}
		case e.Artifact != "":
			out.Statuses = append(out.Statuses, engine.StatusChange{At: e.At, Artifact: e.Artifact, Type: e.Type, Title: e.Title, From: e.From, To: e.To})
		case e.Session != "":
			out.Focuses = append(out.Focuses, engine.FocusChange{At: e.At, Session: e.Session, Focus: e.Focus})
		}
	}
	return out, nil
}
