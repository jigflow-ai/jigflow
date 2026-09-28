// Package cli is the shell around the workflow engine: it parses commands,
// loads the Playbook, reads and writes the Store, and reports results.
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

	"github.com/jigflow-ai/jigflow/internal/engine"
	"github.com/jigflow-ai/jigflow/internal/playbook"
	"github.com/jigflow-ai/jigflow/internal/store"
)

// Version is the binary's version, overridable at build time with -ldflags.
var Version = "dev"

// Exit codes.
const (
	exitOK      = 0
	exitRefused = 1 // the command was understood but refused or failed
	exitUsage   = 2 // the command line was malformed
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
}

// Run executes one command in the project rooted at dir and returns the
// process exit code. getenv reads the process environment.
func Run(args []string, dir string, getenv func(string) string, stdin *os.File, stdout, stderr io.Writer) int {
	e := &env{dir: dir, actor: engine.Actor{Session: getenv(SessionEnv)}, stdin: stdin, stdout: stdout, stderr: stderr}
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
	"next":      cmdNext,
	"propose":   cmdPropose,
	"query":     cmdQuery,
	"approve":   cmdApprove,
	"reject":    cmdReject,
	"check":     cmdCheck,
	"migrate":   cmdMigrate,
	"simulate":  cmdSimulate,
	"publish":   cmdPublish,
}

const usage = `Usage: jfl <command> [arguments]

Commands:
  create <Type> --title <title> [--status <status>] [--link <link>=<id>]...
                          create an Artifact in one of the Type's initial Statuses,
                          with Links to other Artifacts
  move <id> <status>      move an Artifact through a declared Transition,
                          running its Gates before and its Actions after;
                          a Human Transition asks a person to confirm it in
                          an interactive terminal and is refused to agents;
                          a body edited outside jfl is re-validated, and a
                          Status or frontmatter changed outside jfl is refused;
                          an agent session's move Claims the Artifact, and is
                          refused on one another session claims; entering a
                          Status with no Binding, or a final one, releases it
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
                          one unit; creations may Link to each other by ref
  query [--type <Type>] [--status <status>]
                          list the Artifacts, with their Status, Claim and
                          Links, optionally only those of one Type or in one
                          Status
  approve <proposal>      apply every change in a pending Proposal, or none;
                          only a person may, confirming it in an interactive
                          terminal, and its items may then make Human
                          Transitions and create into Statuses with a Binding
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
  publish <adapter>       publish the Playbook's Skills, the Guidelines they
                          name and a router Skill built from its Bindings for
                          a coding agent: claude-code, as Claude Code Skills in
                          .claude/skills, or agents-md, as a section of
                          AGENTS.md and Skills in .agents/skills; publishing
                          again replaces and removes only what jfl published
  mcp                     serve the agent-safe commands as an MCP server over
                          stdio, as an agent session: next, move (never a
                          Human Transition), propose, query, and create into
                          an Inbox; never approve or reject. The session is
                          JFL_SESSION, or a fresh one for the server's life
  version                 print the version

Environment:
  JFL_SESSION             the agent session's id, set by Adapters; without it
                          the command is a person's
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
func (e *env) load() (*engine.Playbook, *store.File, error) {
	pb, err := playbook.Load(e.dir)
	if err != nil {
		return nil, nil, err
	}
	st := store.NewFile(e.dir)
	all, err := st.List()
	if err != nil {
		return nil, nil, err
	}
	if err := engine.CheckOrphans(pb, all); err != nil {
		return nil, nil, fmt.Errorf("%w\n%s", err, migrateHint)
	}
	return pb, st, nil
}

func cmdCreate(e *env, args []string) error {
	if len(args) == 0 || args[0] == "" || args[0][0] == '-' {
		return fmt.Errorf("%w: jfl create <Type> --title <title> [--status <status>] [--link <link>=<id>]...", errUsage)
	}
	typeName := args[0]
	fs := flag.NewFlagSet("create", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	title := fs.String("title", "", "title of the new Artifact")
	status := fs.String("status", "", "starting Status (default: the Type's first initial Status)")
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
	a, err := engine.Create(pb, e.actor, typeName, *title, *status, links, existing)
	if err != nil {
		return err
	}
	if err := st.Save(a); err != nil {
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
		if err := e.confirmHuman(a, to); err != nil {
			return err
		}
	}
	for _, g := range tr.Gates {
		if out, err := e.shell(g.Cmd, a.ID, a.Status, to); err != nil {
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
	if err := e.unfocus(pb, moved); err != nil {
		return err
	}
	fmt.Fprintf(e.stdout, "%s: %s → %s\n", moved.ID, a.Status, moved.Status)
	// The Transition has happened; a failing Action is reported, stops the
	// Actions after it, and makes the command fail, but doesn't undo the move.
	for _, act := range tr.Actions {
		out, err := e.shell(act.Cmd, a.ID, a.Status, to)
		if err != nil {
			return fmt.Errorf("%s: moved to %q, but Action %q failed (%s: %v)%s", moved.ID, moved.Status, act.Name, act.Cmd, err, indent(out))
		}
		fmt.Fprintf(e.stdout, "Action %q succeeded%s\n", act.Name, indent(out))
	}
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
		line := fmt.Sprintf("%s %s %q: %s", a.ID, a.Type, a.Title, a.Status)
		if a.Claim != "" {
			line += ", claimed by agent session " + a.Claim
		}
		for _, name := range slices.Sorted(maps.Keys(a.Links)) {
			line += fmt.Sprintf(", %s: %s", name, strings.Join(a.Links[name], " "))
		}
		fmt.Fprintln(e.stdout, line)
	}
	if n == 0 {
		fmt.Fprintln(e.stdout, "no Artifacts")
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
	return store.NewSessions(e.dir).SetFocus(e.actor.Session, focus)
}

// pick says which Skill to run on which Artifact.
func (e *env) pick(c engine.Candidate) {
	fmt.Fprintf(e.stdout, "run /%s on %s %q\n", c.Skill, c.Artifact.ID, c.Artifact.Title)
}

// unfocus takes an Artifact that is no longer agent work, handed to a
// person or finished, out of every session's Focus.
func (e *env) unfocus(pb *engine.Playbook, a engine.Artifact) error {
	if pb.AgentWork(a) {
		return nil
	}
	return store.NewSessions(e.dir).Unfocus(a.ID)
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
