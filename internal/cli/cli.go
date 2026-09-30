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

	"github.com/jigflow-ai/jigflow/internal/adapter"
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
	// form, when set, asks the person for a Confirmation in a form the
	// agent's client shows only to them, and returns their choice (ADR
	// 0024). jfl mcp sets it only for a client that can show a form: on a
	// person's command, on an agent's propose, which asks the person at
	// once about the Proposal it puts forward, and on an agent's move,
	// which asks the person to make a Human Transition.
	form func(message string, choices ...string) (string, error)
	// tool is set when the command is a tool call of jfl mcp: an agent
	// refused there is already using jfl's tools, so a refusal names none.
	tool bool
}

// Run executes one command in the project rooted at dir and returns the
// process exit code. getenv reads the process environment.
func Run(args []string, dir string, getenv func(string) string, stdin *os.File, stdout, stderr io.Writer) int {
	e := &env{dir: dir, actor: engine.Actor{Session: getenv(SessionEnv)}, stdin: stdin, stdout: stdout, stderr: stderr, getenv: getenv}
	return e.run(args)
}

// run executes one command as e and returns the process exit code.
func (e *env) run(args []string) int {
	now, err := clock(e.getenv)
	if err != nil {
		fmt.Fprintf(e.stderr, "jfl: %v\n", err)
		return exitUsage
	}
	e.now = now
	if len(args) == 0 {
		fmt.Fprint(e.stderr, usage)
		return exitUsage
	}
	cmd, ok := commands[args[0]]
	if !ok {
		fmt.Fprintf(e.stderr, "jfl: unknown command %q\n\n%s", args[0], usage)
		return exitUsage
	}
	if err := cmd(e, args[1:]); err != nil {
		// A Connector failing is a problem with the tracker, which the
		// person must tell apart from the workflow refusing the command.
		if _, ok := errors.AsType[*store.ConnectorError](err); ok {
			fmt.Fprintf(e.stderr, "jfl %s: tracker problem, not a workflow refusal: %v\n", args[0], err)
			return exitConnector
		}
		fmt.Fprintf(e.stderr, "jfl %s: %v\n", args[0], err)
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
	"install":   cmdInstall,
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
                          the playbook-author Skill builds with you); the settings a tracker's Connector needs and the
                          label of each Status it keeps; and the Adapter to
                          publish through. It proposes, as one Proposal, the
                          commands of the Gates tests and lint and a starter
                          Guideline, conventions, from the toolchain it
                          detects. Running it again keeps what is set up and
                          asks only for what is missing. An agent session may
                          run it too, with every answer as a flag: there it
                          never asks, and an answer missing fails, naming the
                          flag and writing nothing; the Proposal it puts
                          forward is the session's, for a person to approve.
                          It warns about hooks that can answer the
                          Confirmation form, as check does
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
                          an interactive terminal, or in the form the agent's
                          client shows them through jfl mcp, and is otherwise
                          refused to agents, naming the MCP tool that asks
                          the person, and one the Playbook marks
                          human: dashboard is made only in the Dashboard
                          (jfl ui);
                          a body edited outside jfl is re-validated, and a
                          Status or frontmatter changed outside jfl is refused;
                          an agent session's move Claims the Artifact, and is
                          refused on one another session claims; entering a
                          Status with no Binding, or a final one, releases it
  comment <id> [--persona <name>] <text>
                          add a comment to an Artifact: in the tracker, where
                          an agent's ends with the AI-generated marker, or at
                          the end of its file's body; the Dashboard runs it for
                          a person from the Artifact's page; --persona
                          attributes the comment to a Persona usable in the
                          project, as publish resolves them, naming it in a
                          file's comment heading after its author or in a
                          tracker comment's lead line **As <name>:**, and
                          refuses any other name, listing the usable ones and
                          adding nothing
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
                          Gate its command or removing the one the project's
                          Playbook file gives it, setting the Mockup folder
                          or removing the project's, which is refused while
                          the folder holds a Mockup, moving a git Base
                          Playbook to another ref, whose commit approving
                          it pins in playbook.lock, changing a Connector's
                          command, args, marker, settings or an Artifact
                          Type's settings, or the label or state a Status or
                          field value is mapped to, copying the Base
                          Playbook's declaration of it first, or removing
                          the project's declaration, saying which Artifacts
                          its Store will no longer see and, per remapped
                          value, how many carry the old label or state and
                          which, which approving relabels, adding a Guideline, or
                          declaring an Artifact Type or writing a Skill,
                          either replacing the one of that name; a Proposal whose
                          Playbook would fail check is refused. It says
                          where the Proposal waits for a person
  query [--type <Type>] [--status <status>]
                          list the Artifacts, with their Status, Claim and
                          Links, optionally only those of one Type or in one
                          Status
  show <id>               print an Artifact as query lists it, then its body
                          and the comments its tracker keeps, so Skills read
                          Artifacts kept in a tracker through jfl too; given
                          a Proposal's id, print the Proposal, a pending
                          one's items with the value of the Playbook file
                          each replaces now
  approve <proposal>      apply every change in a pending Proposal, or none;
                          only a person may, confirming it in an interactive
                          terminal, in the Dashboard, or in the form the
                          agent's client shows them through jfl mcp, and its
                          items may then make Human Transitions and create
                          into Statuses with a Binding; one making a
                          Transition the Playbook requires the Dashboard for
                          is approved only there; one changing a Connector
                          says first which Artifacts its Store will no
                          longer see, and which carry a label or state it
                          remaps, which it relabels through the Connector,
                          putting back those relabelled and the Playbook
                          file when one fails, a tracker problem; an agent
                          session is refused, and told
                          the MCP tool that asks the person
  reject <proposal>       drop a pending Proposal, changing nothing; only a
                          person may, and an agent session is told the MCP
                          tool that asks them
  check [--proposal <proposal>]
                          validate the Playbook, merged over the Base Playbook
                          it extends, listing every problem, or the Artifacts
                          it leaves in an undeclared Status; every other
                          command refuses to run while there are any.
                          --proposal validates it as that pending Proposal
                          would make it, writing nothing, and says the
                          commit a git Base Playbook's ref resolves to. It
                          says where the Playbook keeps Mockups, when it
                          declares a folder for them. It warns, still succeeding, about each
                          Claude Code Elicitation or ElicitationResult hook
                          in .claude/settings.json,
                          .claude/settings.local.json or the user's
                          settings whose matcher matches jfl's MCP server:
                          it can answer the Confirmation form in the
                          person's place
  migrate                 apply the Playbook Migrations: move every Artifact in
                          a Status its Artifact Type no longer declares to the
                          Status a Migration maps it to, all of them or none;
                          only a person may
  simulate <Type> [--proposal <proposal>] [--source]
                          walk a pretend Artifact of the Type through the
                          Playbook: print each path from an initial Status to
                          a final one, visiting no Status twice, with the
                          Skill, Readiness, Guards, Gates, Actions and Human
                          Transitions along it; writes no state and runs no
                          Gates or Actions. --proposal walks it through the
                          Playbook as that pending Proposal would make it;
                          --source first prints the file declaring the Type
  publish [--remove] <adapter>
                          publish the Playbook's Skills, the Guidelines and
                          Personas they name, every active Persona and a
                          router Skill built from its Bindings, which also
                          names the Skills only a person starts, for a coding
                          agent: claude-code, as Claude Code Skills in
                          .claude/skills, with the hooks that read its token
                          usage in .claude/settings.json and jfl mcp as the
                          jfl server in .mcp.json, or agents-md, as a
                          section of AGENTS.md, Skills in .agents/skills and
                          Personas in .agents/personas, printing the command
                          that registers jfl mcp with the agent; publishing
                          again replaces and removes only what jfl published.
                          Active Personas are those of the Persona Library
                          ($XDG_CONFIG_HOME/jigflow/personas, or
                          ~/.config/jigflow/personas) and of the Playbook,
                          and the active Persona Artifacts; the project's,
                          active or retired, override the Library's by name.
                          It warns about hooks that can answer the
                          Confirmation form, as check does, whichever the
                          Adapter, since any agent may run in Claude Code.
                          --remove deletes what the Adapter published, or
                          only jfl's part of a file a person writes too, and
                          nothing else, so jfl init no longer publishes
                          through it; with nothing published for it, it
                          says so
  install <adapter>       install jfl's own Skills, which belong to no
                          Playbook, for a coding agent at user level, so
                          every project has them before it is set up:
                          claude-code writes jigflow-init, a Skill only a
                          person starts, to $CLAUDE_CONFIG_DIR/skills, or
                          ~/.claude/skills. Typed as /jigflow-init in a
                          repository, it has the agent suggest a Playbook,
                          work out its settings with the person and run
                          jfl init with every answer as a flag. It writes
                          nothing into the directory it runs in; installing
                          again changes nothing, or replaces the Skill with
                          the one this jfl ships, but never one a person
                          wrote. agents-md is refused: those agents have no
                          user-level place for Skills
  ledger                  sum the Ledger: the time each Artifact spent in each
                          Status, so far in the one it is in, and the agent
                          session time and tokens charged to it while it was
                          in Focus; the time every Artifact of a Type spent in
                          each Status; agent time and tokens with nothing in
                          Focus, which are unattributed; and each Status
                          change and change to the Playbook a Confirmation
                          made, with the channel it came through: terminal,
                          dashboard or agent. Every create, Transition,
                          approved Proposal and
                          migration, and every change of a session's Focus,
                          adds an entry of its own to the committed
                          .jigflow/ledger, timed by jfl's clock; tokens come
                          only from the agent's own records, and agents
                          without any record time only
  hook claude-code        run by the Claude Code hooks jfl publish sets up,
                          never by an agent session: at SessionStart, give the
                          session's commands JFL_SESSION; after each turn, and
                          at the end of a sub-agent or the session, add the
                          token usage Claude Code's transcripts record to the
                          Ledger
  mcp                     serve the agent-safe commands as an MCP server over
                          stdio, as an agent session: next, move (a Human
                          Transition only as below), propose, query, show,
                          and create into an Inbox. The session is JFL_SESSION, or a
                          fresh one for the server's life. To a client that
                          declared elicitation, also approve: it asks the
                          person to approve or reject a pending Proposal in a
                          form the client shows only to them, which jfl
                          writes; a dismissed or declined form leaves it
                          pending. There, propose asks the person the same
                          at once about the Proposal it puts forward, and
                          move asks them to make or refuse a Human
                          Transition, which is then theirs. jfl publish
                          registers it for the agent
  ui [--addr <host:port>] serve the Dashboard on this machine, at 127.0.0.1:7457
                          unless --addr names another loopback address, until
                          interrupted: the Artifacts of each Type with their
                          Status, Claim and Links and who they wait on, the
                          human queue and pending Proposals first, the Ledger
                          summed and drawn as a Timeline of how long each
                          Artifact spent in each Status, in a git repository
                          the branch's commits linked to the Artifacts they
                          name, the Mockups in the folder the Playbook
                          declares, served only from it and sandboxed, on a
                          Design page and on the pages of the Artifacts
                          linking them, and each Type's Status machine
                          drawn, with check's warning about hooks that can
                          answer the Confirmation form. The browser that
                          opens the link it prints may approve or reject
                          Proposals, editing their creations first, make
                          Human Transitions, comment on an Artifact from its
                          page, as jfl comment run by them, and give a Gate
                          its command, or its Base Playbook's back, and set
                          the Mockup folder, or its Base Playbook's back,
                          unless it holds a Mockup, move a git Base
                          Playbook to another ref, and change a
                          Connector's command, args, marker and settings,
                          or take it back to its Base Playbook's, on the
                          Playbook page, as a Proposal of theirs approved
                          at once, never committed, and publish the
                          Playbook for an Adapter there, or stop, as jfl
                          publish run by them; a Dashboard an agent
                          session starts is only to look at. It
                          never edits an Artifact's body, except to add such
                          a comment
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
                          at v0.4.0)

Exit status:
  0 done, 1 refused or failed, 2 malformed command line, 3 a Connector failed:
  a problem reaching or using the tracker, not a workflow refusal
`

func cmdVersion(e *env, _ []string) error {
	fmt.Fprintf(e.stdout, "jigflow %s\n", Version)
	return nil
}

func cmdCheck(e *env, args []string) error {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	proposal := fs.String("proposal", "", "check the Playbook as this pending Proposal would make it")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("%w: %v", errUsage, err)
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("%w: jfl check [--proposal <proposal>]", errUsage)
	}
	if *proposal != "" {
		pb, err := e.proposed(*proposal)
		if err != nil {
			return err
		}
		fmt.Fprintf(e.stdout, "Playbook %q, as %s would make it: no problems\n", pb.Name, *proposal)
		if pb.Mockups != "" {
			fmt.Fprintf(e.stdout, "Mockups in %s\n", pb.Mockups)
		}
		if b := pb.Base; b != nil && b.Git != "" {
			fmt.Fprintf(e.stdout, "Base Playbook %s@%s, commit %s\n", b.Git, b.Ref, b.Commit)
		}
		e.warnFormHooks()
		return nil
	}
	pb, _, err := e.load()
	if err != nil {
		return err
	}
	fmt.Fprintf(e.stdout, "Playbook %q: no problems\n", pb.Name)
	if pb.Mockups != "" {
		fmt.Fprintf(e.stdout, "Mockups in %s\n", pb.Mockups)
	}
	e.warnFormHooks()
	return nil
}

// warnFormHooks warns, on stderr, about each Claude Code hook in the
// project's or the user's settings that can answer the Confirmation form in
// the person's place. It never refuses anything (ADR 0003).
func (e *env) warnFormHooks() {
	for _, w := range adapter.FormHookWarnings(e.dir, e.getenv) {
		fmt.Fprintf(e.stderr, "jfl: warning: %s\n", w)
	}
}

// load loads the Playbook and the Store it applies to. A Playbook that
// would leave Artifacts in an undeclared Status doesn't load (ADR 0010).
func (e *env) load() (*engine.Playbook, store.Store, error) {
	pb, err := playbook.Load(e.dir)
	if err != nil {
		return nil, nil, err
	}
	if err := e.checkOrphans(pb); err != nil {
		return nil, nil, err
	}
	return pb, store.Open(e.dir, pb), nil
}

// checkOrphans refuses a Playbook that would leave Artifacts in an
// undeclared Status (ADR 0010).
func (e *env) checkOrphans(pb *engine.Playbook) error {
	// Only files can leave Artifacts in an undeclared Status, since a
	// tracker's item is always in one of its Artifact Type's Statuses, so
	// this reads no tracker.
	files, err := store.NewFile(e.dir).List()
	if err != nil {
		return err
	}
	files = slices.DeleteFunc(files, func(a engine.Artifact) bool {
		t := pb.Type(a.Type)
		return t != nil && t.Store != ""
	})
	if err := engine.CheckOrphans(pb, files); err != nil {
		return fmt.Errorf("%w\n%s", err, migrateHint)
	}
	return nil
}

// proposed loads the Playbook as the pending Proposal id would make it once
// approved, writing nothing, so that it can be checked and simulated first.
func (e *env) proposed(id string) (*engine.Playbook, error) {
	pb, _, err := e.load()
	if err != nil {
		return nil, err
	}
	p, err := store.NewProposals(e.dir).Get(id)
	if err != nil {
		return nil, err
	}
	if p.Status != engine.Pending {
		return nil, fmt.Errorf("%s isn't pending: it was %s", p.ID, p.Status)
	}
	items := playbookItems(p.Items)
	if len(items) == 0 {
		return nil, fmt.Errorf("%s changes nothing in the Playbook", p.ID)
	}
	next, err := e.candidate(items)
	if err != nil {
		return nil, err
	}
	if err := e.mockupsStay(pb, next); err != nil {
		return nil, err
	}
	return next, nil
}

// candidate loads the Playbook as the items, which change it, would make
// it, refusing one that fails its checks or would leave Artifacts in an
// undeclared Status.
func (e *env) candidate(items []engine.ProposalItem) (*engine.Playbook, error) {
	pb, err := playbook.Candidate(e.dir, items)
	if err != nil {
		return nil, err
	}
	if err := e.checkOrphans(pb); err != nil {
		return nil, err
	}
	return pb, nil
}

// playbookItems are the items that change the Playbook.
func playbookItems(items []engine.ProposalItem) []engine.ProposalItem {
	var out []engine.ProposalItem
	for _, it := range items {
		if it.ChangesPlaybook() {
			out = append(out, it)
		}
	}
	return out
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
	if err := e.recordStatus(a, "", ""); err != nil {
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
	// Asked of an agent session whose client can show the person a form, a
	// Human Transition is the person's to make, confirmed in the form, as
	// they would make it in a terminal (ADR 0024).
	actor := e.actor
	human, isHuman := humanTransition(pb, a, to)
	if actor.Agent() && e.form != nil && isHuman {
		actor = engine.Actor{}
	}
	moved, tr, err := engine.Move(pb, actor, a, to, all)
	if err != nil && actor.Agent() && isHuman {
		return fmt.Errorf("%w %s", err, e.askInstead(a.ID, human))
	}
	if err != nil {
		return err
	}
	var via engine.Channel
	if tr.Human {
		if via, err = e.confirmHuman(a, tr); err != nil {
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
	if err := e.recordStatus(moved, a.Status, via); err != nil {
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

// humanTransition returns the declared Human Transition moving a to the
// Status to, and whether there is one.
func humanTransition(pb *engine.Playbook, a engine.Artifact, to string) (engine.Transition, bool) {
	t := pb.Type(a.Type)
	if t == nil {
		return engine.Transition{}, false
	}
	i := slices.IndexFunc(t.Transitions, func(tr engine.Transition) bool {
		return tr.From == a.Status && tr.To == to && tr.Human
	})
	if i < 0 {
		return engine.Transition{}, false
	}
	return t.Transitions[i], true
}

// askInstead tells an agent session refused the Human Transition tr of the
// Artifact id how the person is asked for it instead, so that an agent that
// shelled out finds the way (ADR 0024): through jfl's MCP tools, in a form
// their client shows only to them, or where it waits for them. One the
// Playbook requires the Dashboard for waits only there. Refused in a tool
// call of jfl mcp, whose client can't show a form, it names no tool.
func (e *env) askInstead(id string, tr engine.Transition) string {
	if tr.Dashboard {
		return "An agent can only propose it, and the Playbook requires making it in the Dashboard: tell the person it waits for them there (jfl ui)."
	}
	waits := fmt.Sprintf("tell the person it waits for them: jfl move %s %s in a terminal, or the Dashboard (jfl ui).", id, tr.To)
	if e.tool {
		return "An agent can only propose it, or " + waits
	}
	return "An agent can only propose it, or ask the person for it: if jfl's MCP tools include approve, the move tool asks them in a form only they see. Otherwise " + waits
}

func cmdComment(e *env, args []string) error {
	const use = "jfl comment <id> [--persona <name>] <text>"
	if len(args) < 2 || args[0] == "" || args[0][0] == '-' {
		return fmt.Errorf("%w: %s", errUsage, use)
	}
	id, rest := args[0], args[1:]
	var persona string
	// A comment with no flag is taken whole, even one starting with '-'.
	if len(rest) > 1 {
		fs := flag.NewFlagSet("comment", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		fs.StringVar(&persona, "persona", "", "the Persona the comment is attributed to")
		if err := fs.Parse(rest); err != nil {
			return fmt.Errorf("%w: %v", errUsage, err)
		}
		rest = fs.Args()
	}
	if len(rest) != 1 || strings.TrimSpace(rest[0]) == "" {
		return fmt.Errorf("%w: %s", errUsage, use)
	}
	pb, st, err := e.load()
	if err != nil {
		return err
	}
	if err := e.usablePersona(pb, persona); err != nil {
		return err
	}
	by := e.actor
	by.Persona = persona
	if err := st.Comment(id, rest[0], by); err != nil {
		return err
	}
	fmt.Fprintf(e.stdout, "commented on %s\n", id)
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
// kept in a tracker through jfl as it reads one in files. Given the id of a
// Proposal no Artifact has, it prints the Proposal instead.
func cmdShow(e *env, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("%w: jfl show <id>", errUsage)
	}
	pb, st, err := e.load()
	if err != nil {
		return err
	}
	a, err := st.Get(args[0])
	if errors.Is(err, store.ErrNotFound) {
		if p, perr := store.NewProposals(e.dir).Get(args[0]); perr == nil {
			fmt.Fprint(e.stdout, showProposal(pb, p))
			return nil
		}
	}
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
