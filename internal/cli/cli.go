// Package cli is the shell around the workflow engine: it parses commands,
// loads the Playbook, reads and writes the Stores, and reports results.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/jigflow-ai/jigflow/internal/engine"
	"github.com/jigflow-ai/jigflow/internal/playbook"
	"github.com/jigflow-ai/jigflow/internal/store"
)

// Version is the binary's version, overridable at build time with -ldflags.
var Version = "dev"

// Exit codes.
const (
	exitOK        = 0
	exitRefused   = 1 // the command was understood but refused or failed
	exitUsage     = 2 // the command line was malformed
	exitConnector = 3 // a Connector failed: a tracker problem, not a refusal
)

// errUsage marks errors in how a command was invoked.
var errUsage = errors.New("usage")

// SessionEnv is the environment variable through which an Adapter gives an
// agent session its session id. A command run without it is a person's.
const SessionEnv = "JFL_SESSION"

type env struct {
	dir            string
	actor          engine.Actor
	stdin          *os.File
	stdout, stderr io.Writer
	getenv         func(string) string
	now            func() time.Time // the CLI's clock, which the Ledger records
	led            *store.Ledger
	// clicked is set when the command is a person's click in the
	// Dashboard, which confirms a Human Transition or an approval as a
	// terminal's y would (ADR 0003).
	clicked bool
}

// Run executes one command in the project rooted at dir and returns the
// process exit code. getenv reads the process environment.
func Run(args []string, dir string, getenv func(string) string, stdin *os.File, stdout, stderr io.Writer) int {
	e := &env{dir: dir, actor: engine.Actor{Session: getenv(SessionEnv)}, stdin: stdin, stdout: stdout, stderr: stderr, getenv: getenv}
	now, err := clock(getenv)
	if err != nil {
		fmt.Fprintf(stderr, "jfl: %v\n", err)
		return exitUsage
	}
	e.now = now
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return exitUsage
	}
	cmd, ok := commands[args[0]]
	if !ok {
		fmt.Fprintf(stderr, "jfl: unknown command %q\n\n%s", args[0], usage)
		return exitUsage
	}
	if err := cmd(e, args[1:]); err != nil {
		// A Connector failing is a problem with the tracker, which the
		// person must tell apart from the workflow refusing the command.
		if _, ok := errors.AsType[*store.ConnectorError](err); ok {
			fmt.Fprintf(stderr, "jfl %s: tracker problem, not a workflow refusal: %v\n", args[0], err)
			return exitConnector
		}
		fmt.Fprintf(stderr, "jfl %s: %v\n", args[0], err)
		if errors.Is(err, errUsage) {
			return exitUsage
		}
		return exitRefused
	}
	return exitOK
}

var commands = map[string]func(*env, []string) error{
	"version":   cmdVersion,
	"--version": cmdVersion,
	"create":    cmdCreate,
	"move":      cmdMove,
	"comment":   cmdComment,
	"next":      cmdNext,
	"propose":   cmdPropose,
	"query":     cmdQuery,
	"show":      cmdShow,
	"approve":   cmdApprove,
	"reject":    cmdReject,
	"check":     cmdCheck,
	"migrate":   cmdMigrate,
	"simulate":  cmdSimulate,
	"publish":   cmdPublish,
	"ledger":    cmdLedger,
	"hook":      cmdHook,
	"ui":        cmdUI,
	"init":      cmdInit,
}

const usage = `Usage: jfl <command> [arguments]

Commands:
  init [--playbook larapilot|pocock|own] [--adapter <adapter>]
       [--setting <connector>[.<Type>].<setting>=<value>]... [--label <Type>.<status>=<label>]...
                          set the project up, asking at the terminal what the
                          flags don't say: the Playbook to use, with no
                          default (Larapilot-style, Pocock, or your own, which
                          the playbook-author Skill builds when this build has
                          it); the settings a tracker's Connector needs and the
                          label of each Status it keeps; and the Adapter to
                          publish through. It proposes, as one Proposal, the
                          commands of the Gates tests and lint and a starter
                          Guideline, conventions, from the toolchain it
                          detects. Running it again keeps what is set up and
                          asks only for what is missing
  create <Type> --title <title> [--status <status>] [--field <field>=<value>]...
         [--link <link>=<id>]...
                          create an Artifact in one of the Type's initial Statuses,
                          with values for its fields and Links to other
                          Artifacts, in the Type's Store: a file, or an item
                          of the tracker its Connector reaches, which gives
                          it its id; an agent's carries the AI-generated marker.
                          Every Playbook has the built-in Persona Type: its
                          title is the Persona's name and its body describes
                          it; anyone may create one into proposed, and only a
                          person moves it to active or retired
  move <id> <status>      move an Artifact through a declared Transition,
                          running its Gates before and its Actions after;
                          a Human Transition asks a person to confirm it in
                          an interactive terminal and is refused to agents,
                          and one the Playbook marks human: dashboard is made
                          only in the Dashboard (jfl ui);
                          a body edited outside jfl is re-validated, and a
                          Status or frontmatter changed outside jfl is refused;
                          an agent session's move Claims the Artifact, and is
                          refused on one another session claims; entering a
                          Status with no Binding, or a final one, releases it
  comment <id> <text>     add a comment to an Artifact: in the tracker, where
                          an agent's ends with the AI-generated marker, or at
                          the end of its file's body
  next [--autopilot]      say which Skill to run on which Artifact, preferring
                          what the session claims and skipping what others
                          claim, and make the pick the session's Focus; in an
                          interactive terminal, show the alternatives too;
                          --autopilot is one step of an agent session's
                          autopilot, which stops, saying why and what waits
                          for a person, when nothing is left for an agent,
                          its last move was refused, or the Skill it handed
                          out didn't move the Artifact on
  propose <file>          put forward the creations and Transitions in a
                          Proposal file for a person to approve or reject as
                          one unit; creations may give the Type's fields
                          values and Link to each other by ref;
                          an item may change the Playbook instead, giving a
                          Gate its command or adding a Guideline
  query [--type <Type>] [--status <status>]
                          list the Artifacts, with their Status, Claim and
                          Links, optionally only those of one Type or in one
                          Status
  show <id>               print an Artifact as query lists it, then its body
                          and the comments its tracker keeps, so Skills read
                          Artifacts kept in a tracker through jfl too
  approve <proposal>      apply every change in a pending Proposal, or none;
                          only a person may, confirming it in an interactive
                          terminal or in the Dashboard, and its items may then
                          make Human Transitions and create into Statuses
                          with a Binding; one making a Transition the Playbook
                          requires the Dashboard for is approved only there
  reject <proposal>       drop a pending Proposal, changing nothing; only a
                          person may
  check                   validate the Playbook, merged over the Base Playbook
                          it extends, listing every problem, or the Artifacts
                          it leaves in an undeclared Status; every other
                          command refuses to run while there are any
  migrate                 apply the Playbook Migrations: move every Artifact in
                          a Status its Artifact Type no longer declares to the
                          Status a Migration maps it to, all of them or none;
                          only a person may
  simulate <Type>         walk a pretend Artifact of the Type through the
                          Playbook: print each path from an initial Status to
                          a final one, visiting no Status twice, with the
                          Skill, Readiness, Guards, Gates, Actions and Human
                          Transitions along it; writes no state and runs no
                          Gates or Actions
  publish <adapter>       publish the Playbook's Skills, the Guidelines and
                          Personas they name, every active Persona and a
                          router Skill built from its Bindings, which also
                          names the Skills only a person starts, for a coding
                          agent: claude-code, as Claude Code Skills in
                          .claude/skills, with the hooks that read its token
                          usage in .claude/settings.json, or agents-md, as a
                          section of AGENTS.md, Skills in .agents/skills and
                          Personas in .agents/personas; publishing again
                          replaces and removes only what jfl published.
                          Active Personas are those of the Persona Library
                          ($XDG_CONFIG_HOME/jigflow/personas, or
                          ~/.config/jigflow/personas) and of the Playbook,
                          and the active Persona Artifacts; the project's,
                          active or retired, override the Library's by name
  ledger                  sum the Ledger: the time each Artifact spent in each
                          Status, so far in the one it is in, and the agent
                          session time and tokens charged to it while it was
                          in Focus; the time every Artifact of a Type spent in
                          each Status; and agent time and tokens with nothing
                          in Focus, which are unattributed. Every create,
                          Transition, approved Proposal and migration, and
                          every change of a session's Focus, adds an entry of
                          its own to the committed .jigflow/ledger, timed by
                          jfl's clock; tokens come only from the agent's own
                          records, and agents without any record time only
  hook claude-code        run by the Claude Code hooks jfl publish sets up,
                          never by an agent session: at SessionStart, give the
                          session's commands JFL_SESSION; after each turn, and
                          at the end of a sub-agent or the session, add the
                          token usage Claude Code's transcripts record to the
                          Ledger
  mcp                     serve the agent-safe commands as an MCP server over
                          stdio, as an agent session: next, move (never a
                          Human Transition), propose, query, and create into
                          an Inbox; never approve or reject. The session is
                          JFL_SESSION, or a fresh one for the server's life
  ui [--addr <host:port>] serve the Dashboard on this machine, at 127.0.0.1:7457
                          unless --addr names another loopback address, until
                          interrupted: the Artifacts of each Type with their
                          Status, Claim and Links and who they wait on, the
                          human queue and pending Proposals first, the Ledger
                          summed, and each Type's Status machine drawn. The
                          browser that opens the link it prints may approve
                          or reject Proposals, editing their creations first,
                          and make Human Transitions; a Dashboard an agent
                          session starts is only to look at. It never edits
                          an Artifact's body
  version                 print the version

Environment:
  JFL_SESSION             the agent session's id, set by Adapters; without it
                          the command is a person's
  XDG_CONFIG_HOME         where the user's Persona Library is, under
                          jigflow/personas (default ~/.config)
  JFL_POCOCK_GIT, JFL_POCOCK_REF
                          the git repository and ref jfl init --playbook pocock
                          extends, for a fork or a mirror (default
                          https://github.com/jigflow-ai/jigflow-playbook-pocock
                          at v0.2.0)

Exit status:
  0 done, 1 refused or failed, 2 malformed command line, 3 a Connector failed:
  a problem reaching or using the tracker, not a workflow refusal
`

func cmdVersion(e *env, _ []string) error {
	fmt.Fprintf(e.stdout, "jigflow %s\n", Version)
	return nil
}

func cmdCheck(e *env, args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("%w: jfl check takes no arguments", errUsage)
	}
	pb, _, err := e.load()
	if err != nil {
		return err
	}
	fmt.Fprintf(e.stdout, "Playbook %q: no problems\n", pb.Name)
	return nil
}

// load loads the Playbook and the Store it applies to. A Playbook that
// would leave Artifacts in an undeclared Status doesn't load (ADR 0010).
func (e *env) load() (*engine.Playbook, store.Store, error) {
	pb, err := playbook.Load(e.dir)
	if err != nil {
		return nil, nil, err
	}
	// Only files can leave Artifacts in an undeclared Status, since a
	// tracker's item is always in one of its Artifact Type's Statuses, so
	// loading reads no tracker.
	files, err := store.NewFile(e.dir).List()
	if err != nil {
		return nil, nil, err
	}
	files = slices.DeleteFunc(files, func(a engine.Artifact) bool {
		t := pb.Type(a.Type)
		return t != nil && t.Store != ""
	})
	st := store.Open(e.dir, pb)
	if err := engine.CheckOrphans(pb, files); err != nil {
		return nil, nil, fmt.Errorf("%w\n%s", err, migrateHint)
	}
	return pb, st, nil
}

func cmdCreate(e *env, args []string) error {
	if len(args) == 0 || args[0] == "" || args[0][0] == '-' {
		return fmt.Errorf("%w: jfl create <Type> --title <title> [--status <status>] [--field <field>=<value>]... [--link <link>=<id>]...", errUsage)
	}
	typeName := args[0]
	fs := flag.NewFlagSet("create", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	title := fs.String("title", "", "title of the new Artifact")
	status := fs.String("status", "", "starting Status (default: the Type's first initial Status)")
	fields := fieldFlag{}
	fs.Var(fields, "field", "a field's value, as <field>=<value>; repeatable")
	links := linkFlag{}
	fs.Var(links, "link", "a Link to another Artifact, as <link>=<id>; repeatable")
	if err := fs.Parse(args[1:]); err != nil {
		return fmt.Errorf("%w: %v", errUsage, err)
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("%w: unexpected argument %q", errUsage, fs.Arg(0))
	}

	pb, st, err := e.load()
	if err != nil {
		return err
	}
	existing, err := st.List()
	if err != nil {
		return err
	}
	a, err := engine.Create(pb, e.actor, typeName, *title, *status, fields, links, existing)
	if err != nil {
		return err
	}
	if a, err = st.Create(a, e.actor); err != nil {
		return err
	}
	if err := e.recordStatus(a, ""); err != nil {
		return err
	}
	fmt.Fprintf(e.stdout, "created %s %q in %s\n", a.ID, a.Title, a.Status)
	return nil
}

func cmdMove(e *env, args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("%w: jfl move <id> <status>", errUsage)
	}
	err := e.move(args[0], args[1])
	if e.actor.Agent() {
		// An agent session on autopilot remembers its last move's refusal,
		// which stops autopilot unless a later move succeeds.
		if rerr := e.refused(args[0], args[1], err); rerr != nil {
			return errors.Join(err, rerr)
		}
	}
	return err
}

func (e *env) move(id, to string) error {
	pb, st, err := e.load()
	if err != nil {
		return err
	}
	a, err := st.Get(id)
	if err != nil {
		return err
	}
	// Re-validate edits made outside the CLI before anything else: a body
	// edit is allowed, a frontmatter edit refuses the Transition (ADR 0002).
	bodyEdited, err := st.Verify(id)
	if err != nil {
		return err
	}
	if bodyEdited {
		fmt.Fprintf(e.stdout, "%s: body edited outside jfl, re-validated\n", id)
	}
	all, err := st.List()
	if err != nil {
		return err
	}
	moved, tr, err := engine.Move(pb, e.actor, a, to, all)
	if err != nil {
		return err
	}
	if tr.Human {
		if err := e.confirmHuman(a, tr); err != nil {
			return err
		}
	}
	for _, g := range tr.Gates {
		if g.Cmd == "" {
			return fmt.Errorf("%s: %q → %q refused: %s", a.ID, a.Status, to, noCommand(g))
		}
		if out, err := e.shell(g.Cmd, a, a.Status, to); err != nil {
			return fmt.Errorf("%s: %q → %q refused: Gate %q failed (%s: %v)%s", a.ID, a.Status, to, g.Name, g.Cmd, err, indent(out))
		}
	}
	// A Gate, or anyone while the move waited, may edit the body, which the
	// move keeps, but not the frontmatter.
	if _, err := st.Verify(id); err != nil {
		return err
	}
	if err := st.Save(moved); err != nil {
		return err
	}
	if err := e.recordStatus(moved, a.Status); err != nil {
		return err
	}
	if err := e.unfocus(pb, moved); err != nil {
		return err
	}
	fmt.Fprintf(e.stdout, "%s: %s → %s\n", moved.ID, a.Status, moved.Status)
	// The Transition has happened; a failing Action is reported, stops the
	// Actions after it, and makes the command fail, but doesn't undo the move.
	for _, act := range tr.Actions {
		out, err := e.shell(act.Cmd, a, a.Status, to)
		if err != nil {
			return fmt.Errorf("%s: moved to %q, but Action %q failed (%s: %v)%s", moved.ID, moved.Status, act.Name, act.Cmd, err, indent(out))
		}
		fmt.Fprintf(e.stdout, "Action %q succeeded%s\n", act.Name, indent(out))
	}
	return nil
}

func cmdComment(e *env, args []string) error {
	if len(args) != 2 || strings.TrimSpace(args[1]) == "" {
		return fmt.Errorf("%w: jfl comment <id> <text>", errUsage)
	}
	_, st, err := e.load()
	if err != nil {
		return err
	}
	if err := st.Comment(args[0], args[1], e.actor); err != nil {
		return err
	}
	fmt.Fprintf(e.stdout, "commented on %s\n", args[0])
	return nil
}

func cmdNext(e *env, args []string) error {
	fs := flag.NewFlagSet("next", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	auto := fs.Bool("autopilot", false, "one step of autopilot")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("%w: %v", errUsage, err)
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("%w: jfl next [--autopilot]", errUsage)
	}
	if *auto && !e.actor.Agent() {
		return fmt.Errorf("autopilot is for agent sessions, and %s isn't set", SessionEnv)
	}
	pb, st, err := e.load()
	if err != nil {
		return err
	}
	artifacts, err := st.List()
	if err != nil {
		return err
	}
	proposals, err := store.NewProposals(e.dir).List()
	if err != nil {
		return err
	}
	res := engine.Next(pb, e.actor, artifacts, proposals)
	if *auto {
		return e.autopilot(res, proposals)
	}
	if err := e.focus(res); err != nil {
		return err
	}
	if len(res.Candidates) == 0 {
		fmt.Fprintln(e.stdout, "nothing for an agent to do")
	} else {
		e.pick(res.Candidates[0])
		// In an interactive terminal the other candidates are shown too, so
		// the person can pick one when they know better.
		if alts := res.Candidates[1:]; e.interactive() && len(alts) > 0 {
			fmt.Fprintln(e.stdout, "Alternatives:")
			for _, c := range alts {
				fmt.Fprint(e.stdout, "  ")
				e.pick(c)
			}
		}
	}
	if len(res.Skipped) > 0 {
		fmt.Fprintln(e.stdout, "Skipped:")
		for _, s := range res.Skipped {
			fmt.Fprintf(e.stdout, "  %s: %s\n", s.Artifact.ID, s.Reason)
		}
	}
	return nil
}

func cmdQuery(e *env, args []string) error {
	fs := flag.NewFlagSet("query", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	typeName := fs.String("type", "", "only Artifacts of this Artifact Type")
	status := fs.String("status", "", "only Artifacts in this Status")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("%w: %v", errUsage, err)
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("%w: jfl query [--type <Type>] [--status <status>]", errUsage)
	}
	pb, st, err := e.load()
	if err != nil {
		return err
	}
	if *typeName != "" && pb.Type(*typeName) == nil {
		var names []string
		for _, t := range pb.Types {
			names = append(names, t.Name)
		}
		return fmt.Errorf("unknown Artifact Type %q. Declared Types: %s", *typeName, strings.Join(names, ", "))
	}
	all, err := st.List()
	if err != nil {
		return err
	}
	n := 0
	for _, a := range all {
		if (*typeName != "" && a.Type != *typeName) || (*status != "" && a.Status != *status) {
			continue
		}
		n++
		fmt.Fprintln(e.stdout, summary(a))
	}
	if n == 0 {
		fmt.Fprintln(e.stdout, "no Artifacts")
	}
	return nil
}

// summary is the Artifact in a line: its id, Type, title and Status, then
// its fields, Claim and Links.
func summary(a engine.Artifact) string {
	line := fmt.Sprintf("%s %s %q: %s", a.ID, a.Type, a.Title, a.Status)
	for _, name := range slices.Sorted(maps.Keys(a.Fields)) {
		line += fmt.Sprintf(", %s: %s", name, a.Fields[name])
	}
	if a.Claim != "" {
		line += ", claimed by agent session " + a.Claim
	}
	for _, name := range slices.Sorted(maps.Keys(a.Links)) {
		line += fmt.Sprintf(", %s: %s", name, strings.Join(a.Links[name], " "))
	}
	return line
}

// cmdShow prints an Artifact as query lists it, then its body and the
// comments its tracker keeps apart from it, so a Skill reads an Artifact
// kept in a tracker through jfl as it reads one in files.
func cmdShow(e *env, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("%w: jfl show <id>", errUsage)
	}
	_, st, err := e.load()
	if err != nil {
		return err
	}
	a, err := st.Get(args[0])
	if err != nil {
		return err
	}
	text, err := st.Text(a.ID)
	if err != nil {
		return err
	}
	fmt.Fprintln(e.stdout, summary(a))
	if body := strings.TrimSpace(text.Body); body != "" {
		fmt.Fprintf(e.stdout, "\n%s\n", body)
	}
	for i, c := range text.Comments {
		fmt.Fprintf(e.stdout, "\n## Comment %d\n\n%s\n", i+1, strings.TrimSpace(c))
	}
	return nil
}

// focus makes the pick of next an agent session's Focus; with no pick it
// has none.
func (e *env) focus(res engine.NextResult) error {
	if !e.actor.Agent() {
		return nil
	}
	focus := ""
	if len(res.Candidates) > 0 {
		focus = res.Candidates[0].Artifact.ID
	}
	return e.setFocus(e.actor.Session, focus)
}

// pick says which Skill to run on which Artifact.
func (e *env) pick(c engine.Candidate) {
	fmt.Fprintf(e.stdout, "run /%s on %s %q\n", c.Skill, c.Artifact.ID, c.Artifact.Title)
}

// unfocus takes an Artifact that is no longer agent work, handed to a
// person or finished, out of every session's Focus, and adds each change to
// the Ledger.
func (e *env) unfocus(pb *engine.Playbook, a engine.Artifact) error {
	if pb.AgentWork(a) {
		return nil
	}
	sessions, err := store.NewSessions(e.dir).Unfocus(a.ID)
	if err != nil {
		return err
	}
	for _, s := range sessions {
		if err := e.ledger().RecordFocus(engine.FocusChange{At: e.now(), Session: s}); err != nil {
			return err
		}
	}
	return nil
}

// linkFlag collects repeated --link <link>=<id> flags.
type linkFlag map[string][]string

func (l linkFlag) String() string { return "" }

func (l linkFlag) Set(v string) error {
	name, id, ok := strings.Cut(v, "=")
	if !ok || name == "" || id == "" {
		return fmt.Errorf("--link wants <link>=<id>, got %q", v)
	}
	l[name] = append(l[name], id)
	return nil
}

// fieldFlag collects repeated --field <field>=<value> flags.
type fieldFlag map[string]string

func (f fieldFlag) String() string { return "" }

func (f fieldFlag) Set(v string) error {
	name, value, ok := strings.Cut(v, "=")
	if !ok || name == "" || value == "" {
		return fmt.Errorf("--field wants <field>=<value>, got %q", v)
	}
	f[name] = value
	return nil
}
