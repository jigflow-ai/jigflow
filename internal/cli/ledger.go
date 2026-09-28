package cli

import (
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/jigflow-ai/jigflow/internal/engine"
	"github.com/jigflow-ai/jigflow/internal/store"
)

// clockEnv names the environment variable the CLI reads its clock from, as
// an RFC 3339 timestamp. It is empty, and the clock the real one, in a
// shipped jfl: only the tests' build sets it (-ldflags -X), so no agent can
// set the time the Ledger records (ADR 0007).
var clockEnv string

// clock returns the CLI's clock: the real one, or the time clockEnv sets
// in a test build.
func clock(getenv func(string) string) (func() time.Time, error) {
	if clockEnv == "" || getenv(clockEnv) == "" {
		return func() time.Time { return time.Now().UTC() }, nil
	}
	t, err := time.Parse(time.RFC3339, getenv(clockEnv))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", clockEnv, err)
	}
	return func() time.Time { return t }, nil
}

// recordStatus adds to the Ledger that a entered its Status, leaving from,
// or none when it was created.
func (e *env) recordStatus(a engine.Artifact, from string) error {
	return e.ledger().RecordStatus(engine.StatusChange{At: e.now(), Artifact: a.ID, Type: a.Type, Title: a.Title, From: from, To: a.Status})
}

// setFocus makes id the agent session's Focus, none when it is empty, and
// adds the change to the Ledger when there is one.
func (e *env) setFocus(session, id string) error {
	was, err := store.NewSessions(e.dir).SetFocus(session, id)
	if err != nil || was == id {
		return err
	}
	return e.ledger().RecordFocus(engine.FocusChange{At: e.now(), Session: session, Focus: id})
}

// ledger is the project's Ledger, which this command writes to.
func (e *env) ledger() *store.Ledger {
	if e.led == nil {
		e.led = store.NewLedger(e.dir)
	}
	return e.led
}

func cmdLedger(e *env, args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("%w: jfl ledger takes no arguments", errUsage)
	}
	pb, _, err := e.load()
	if err != nil {
		return err
	}
	l, err := e.ledger().Read()
	if err != nil {
		return err
	}
	sum := engine.Summarise(pb, l, e.now())
	if len(sum.Artifacts) == 0 && sum.Unattributed == 0 {
		fmt.Fprintln(e.stdout, "the Ledger is empty")
		return nil
	}
	w := tabwriter.NewWriter(e.stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "Time per Artifact:")
	for _, a := range sum.Artifacts {
		head := a.ID
		if a.Type != "" {
			head += " " + a.Type
		}
		if a.Title != "" {
			head += fmt.Sprintf(" %q", a.Title)
		}
		fmt.Fprintf(w, "  %s\n", head)
		for _, s := range a.Statuses {
			so := ""
			if s.Now {
				so = " so far"
			}
			fmt.Fprintf(w, "    %s\t%s%s\n", s.Status, duration(s.Time), so)
		}
		if a.Agent > 0 {
			fmt.Fprintf(w, "    agent time\t%s\n", duration(a.Agent))
		}
	}
	if len(sum.Statuses) > 0 {
		fmt.Fprintln(w, "Time per Status:")
		typ := ""
		for _, s := range sum.Statuses {
			if s.Type != typ {
				typ = s.Type
				fmt.Fprintf(w, "  %s\n", typ)
			}
			now := ""
			if s.Now > 0 {
				now = fmt.Sprintf(", %d there now", s.Now)
			}
			fmt.Fprintf(w, "    %s\t%s over %s%s\n", s.Status, duration(s.Time), plural(s.Artifacts, "Artifact"), now)
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}
	fmt.Fprintf(e.stdout, "Agent time %s: %s\n", engine.Unattributed, duration(sum.Unattributed))
	return nil
}

// duration writes d to the minute, as 1h30m, 2h or 45m, or to the second
// when under a minute.
func duration(d time.Duration) string {
	if d < time.Minute {
		return d.Round(time.Second).String()
	}
	d = d.Round(time.Minute)
	h, m := int(d/time.Hour), int((d%time.Hour)/time.Minute)
	var b strings.Builder
	if h > 0 {
		fmt.Fprintf(&b, "%dh", h)
	}
	if m > 0 || h == 0 {
		fmt.Fprintf(&b, "%dm", m)
	}
	return b.String()
}
