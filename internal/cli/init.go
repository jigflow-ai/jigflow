package cli

import (
	"bufio"
	"cmp"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/jigflow-ai/jigflow/internal/adapter"
	"github.com/jigflow-ai/jigflow/internal/engine"
	"github.com/jigflow-ai/jigflow/internal/playbook"
	"github.com/jigflow-ai/jigflow/internal/store"
	"github.com/jigflow-ai/jigflow/internal/toolchain"
	"go.yaml.in/yaml/v3"
)

// offer is a way of working jfl init offers: a shipped Playbook, which the
// project's Playbook extends, or building the project's own.
type offer struct {
	key      string // how --playbook names it
	name     string
	about    string
	builtin  string // the Playbook built into jfl it extends, or
	git, ref string // the git repository it extends, pinned to ref,
	env      string // unless <env>_GIT and <env>_REF name another
}

// extends is what the project's Playbook file says it extends: nothing, for
// the project's own.
func (o offer) extends() string {
	switch {
	case o.builtin != "":
		return "{builtin: " + o.builtin + "}"
	case o.git != "":
		return fmt.Sprintf("{git: %s, ref: %s}", o.git, o.ref)
	}
	return ""
}

// available reports whether this build of jfl has the offer's Playbook.
func (o offer) available() bool {
	switch {
	case o.key == own:
		return true
	case o.builtin != "":
		return playbook.Builtin(o.builtin)
	}
	return o.git != ""
}

// own is the key of the offer to build the project's own Playbook.
const own = "own"

// playbookAuthor is the Skill that builds a Playbook by interviewing the
// person about how they work (ADR 0004).
const playbookAuthor = "playbook-author"

// starterGuideline is the name of the Guideline jfl init proposes from the
// project's toolchain and conventions. A Playbook's Skills may name it, and
// ship a general one the project's overrides.
const starterGuideline = "conventions"

// offers are the ways of working jfl init offers, none of them the default
// (ADR 0009).
var offers = []offer{
	{key: "larapilot", name: "Larapilot-style", about: "PRD, Requirement, Story and Task: spec, plan, implement, review", builtin: "larapilot"},
	// The Pocock Playbook lives in a repository of its own, so it can
	// follow upstream without a release of jfl (ADR 0009); a fork or a
	// mirror can stand in for it.
	{key: "pocock", name: "Pocock", about: "Matt Pocock's workflow: triage, specs broken into tickets, implement, review, wayfinder maps",
		git: "https://github.com/jigflow-ai/jigflow-playbook-pocock", ref: "v0.4.0", env: "JFL_POCOCK"},
	{key: own, name: "Build my own", about: "describe how you work to the " + playbookAuthor + " Skill, which proposes a Playbook"},
}

func cmdInit(e *env, args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	choice := fs.String("playbook", "", "the Playbook to use")
	adapterName := fs.String("adapter", "", "the Adapter to publish through")
	settings := pairFlag{flag: "--setting", want: "<connector>.<setting>=<value> or <connector>.<Type>.<setting>=<value>", pairs: map[string]string{}}
	fs.Var(settings, "setting", "a Connector setting the Playbook leaves empty, or the Connector requires; repeatable")
	labels := pairFlag{flag: "--label", want: "<Type>.<status>=<label>", pairs: map[string]string{}}
	fs.Var(labels, "label", "the tracker label of a Type's Status; repeatable")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("%w: %v", errUsage, err)
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("%w: unexpected argument %q", errUsage, fs.Arg(0))
	}
	in := bufio.NewReader(e.stdin)

	// Every choice is made before anything is written, so a refusal
	// leaves the project as it was.
	_, err := os.Stat(filepath.Join(e.dir, playbook.Dir, "playbook.yaml"))
	initialised := err == nil
	var o offer
	if initialised {
		if *choice != "" {
			fmt.Fprintf(e.stdout, "the project already has a Playbook, in %s: keeping it, not choosing %s\n", playbook.Dir, *choice)
		}
	} else if o, err = e.chooseOffer(in, *choice); err != nil {
		return err
	}
	adapters, err := e.chooseAdapters(in, *adapterName)
	if err != nil {
		return err
	}
	if !initialised {
		if err := e.writePlaybook(o); err != nil {
			return err
		}
	}
	pb, _, err := e.load()
	if err != nil {
		if !initialised {
			// The Playbook chosen can't be loaded, e.g. its repository
			// can't be fetched: leave the project as it was.
			e.unwritePlaybook()
		}
		return err
	}
	if err := e.setUpConnectors(in, pb, settings.pairs, labels.pairs); err != nil {
		if !initialised && e.actor.Agent() {
			// An agent session left an answer out: leave the project as
			// it was, for it to run init again with every flag.
			e.unwritePlaybook()
		}
		return err
	}
	if pb, _, err = e.load(); err != nil {
		return err
	}
	if err := e.proposeSetup(pb); err != nil {
		return err
	}
	for _, a := range adapters {
		if err := e.publish(a); err != nil {
			return err
		}
	}
	e.warnFormHooks()
	if o.key == own {
		if _, ok := playbook.BuiltinSkill(playbookAuthor); ok {
			fmt.Fprintf(e.stdout, "Next, run the %s Skill in %s: it asks how you work and proposes your Playbook's Artifact Types, Statuses and Bindings for you to approve.\n", playbookAuthor, adapters[0].Agent)
		} else {
			fmt.Fprintf(e.stdout, "The %s Skill isn't in this build of jfl: declare your Artifact Types in %s, one YAML file each, and try them with jfl check and jfl simulate.\n", playbookAuthor, filepath.Join(playbook.Dir, "types"))
		}
	}
	return nil
}

// setUpConnectors writes into the project's Playbook file what each
// Connector that keeps an Artifact Type's Artifacts needs and the Playbook
// leaves to the project: the settings it leaves empty, those the Connector
// describes as required and the Playbook doesn't give (ADR 0031), and the
// tracker label of every Status it doesn't map (ADR 0011). Each comes from
// the flags, or else the person at the terminal, who may keep a label the
// Status's name; without a terminal a label is the Status's name, and a
// setting stays empty, to give later. In an agent session each must come
// from the flags (ADR 0026): one missing fails, naming every flag missing,
// and writes nothing.
func (e *env) setUpConnectors(in *bufio.Reader, pb *engine.Playbook, settings, labels map[string]string) error {
	f, err := playbook.OpenFile(e.dir)
	if err != nil {
		return err
	}
	// missing lists the flags an agent session didn't give, whose answers
	// are then left out.
	var missing []string
	answerOrMiss := func(given, flag, question, fallback string) (v string, ok bool, err error) {
		if given == "" && e.actor.Agent() {
			missing = append(missing, flag)
			return "", false, nil
		}
		v, err = e.answer(in, given, question, fallback)
		return v, true, err
	}
	// setUp asks for the setting key, named in its flag after where and in
	// its question after asked, as the Connector describes it in d, if it
	// does, and writes it at path; it reports as empty a setting still left
	// empty.
	setUp := func(where, asked, key string, d *store.Setting, empty string, path ...string) error {
		question := key
		if d != nil && d.Help != "" {
			question += " (" + d.Help
			if d.Kind == store.SettingYesNo {
				question += ", yes or no"
			}
			question += ")"
		}
		flag := fmt.Sprintf("--setting %s.%s=<value>", where, key)
		v, ok, err := answerOrMiss(settings[where+"."+key], flag, fmt.Sprintf("%s: %s: ", asked, question), "")
		if err != nil || !ok {
			return err
		}
		if v == "" {
			fmt.Fprintf(e.stdout, "%s is empty: give it with jfl init --setting %s.%s=<value>\n", empty, where, key)
			return nil
		}
		value, err := settingValue(d, v)
		if err != nil {
			return fmt.Errorf("%w: %s: %v", errUsage, flag, err)
		}
		return f.Set(value, path...)
	}
	var done []string
	described := map[string][]store.Setting{}
	for _, t := range pb.Types {
		c := pb.Connectors[t.Store]
		if c == nil {
			continue
		}
		if err := f.DeclareConnector(c); err != nil {
			return err
		}
		if !slices.Contains(done, c.Name) {
			done = append(done, c.Name)
			described[c.Name] = store.Describe(e.dir, c)
			for _, key := range toSetUp(c.Settings, nil, described[c.Name], false) {
				if err := setUp(c.Name, fmt.Sprintf("Connector %q", c.Name), key, describedAs(described[c.Name], key), fmt.Sprintf("Connector %q: setting %s", c.Name, key), "connectors", c.Name, "settings", key); err != nil {
					return err
				}
			}
		}
		m := c.Types[t.Name]
		for _, key := range toSetUp(m.Settings, c.Settings, described[c.Name], true) {
			if err := setUp(c.Name+"."+t.Name, fmt.Sprintf("Connector %q, %s", c.Name, t.Name), key, describedAs(described[c.Name], key), fmt.Sprintf("Connector %q: %s's setting %s", c.Name, t.Name, key), "connectors", c.Name, "types", t.Name, "settings", key); err != nil {
				return err
			}
		}
		for _, status := range t.Statuses {
			if _, ok := m.Statuses[status]; ok {
				continue
			}
			label, ok, err := answerOrMiss(labels[t.Name+"."+status], fmt.Sprintf("--label %s.%s=<label>", t.Name, status), fmt.Sprintf("%s in %q is labelled [%s]: ", t.Name, status, status), status)
			if err != nil {
				return err
			}
			if !ok {
				continue
			}
			if err := f.Set(label, "connectors", c.Name, "types", t.Name, "statuses", status); err != nil {
				return err
			}
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: an agent session gives every answer as a flag: jfl init %s", errUsage, strings.Join(missing, " "))
	}
	for _, name := range done {
		if f.Changed() {
			fmt.Fprintf(e.stdout, "Connector %q: set up in %s\n", name, filepath.Join(playbook.Dir, "playbook.yaml"))
		} else {
			fmt.Fprintf(e.stdout, "Connector %q: already set up\n", name)
		}
	}
	return f.Save()
}

// toSetUp returns, in the order of their names, the settings init sets
// up: those of settings the Playbook leaves empty, and those described as
// required that neither settings nor, for one given per Artifact Type,
// the Connector's settings, over, give, those given per Artifact Type
// when perType is.
func toSetUp(settings, over map[string]any, described []store.Setting, perType bool) []string {
	var keys []string
	for key, v := range settings {
		if v == "" {
			keys = append(keys, key)
		}
	}
	given := func(m map[string]any, key string) bool { v, ok := m[key]; return ok && v != "" }
	for _, d := range described {
		if d.Required && d.PerType == perType && !given(settings, d.Name) && !given(over, d.Name) && !slices.Contains(keys, d.Name) {
			keys = append(keys, d.Name)
		}
	}
	slices.Sort(keys)
	return keys
}

// describedAs is the setting key as the Connector describes it among
// described, or nil.
func describedAs(described []store.Setting, key string) *store.Setting {
	if i := slices.IndexFunc(described, func(d store.Setting) bool { return d.Name == key }); i >= 0 {
		return &described[i]
	}
	return nil
}

// settingValue is the answer v to a setting the Connector describes as d,
// if it does, as the Playbook file writes it: true or false for a yes/no
// one, a number for a number one, and text for any other.
func settingValue(d *store.Setting, v string) (any, error) {
	switch {
	case d == nil:
	case d.Kind == store.SettingYesNo:
		switch strings.ToLower(v) {
		case "yes", "true":
			return true, nil
		case "no", "false":
			return false, nil
		}
		return nil, fmt.Errorf("%s is yes or no, not %q", d.Name, v)
	case d.Kind == store.SettingNumber:
		if n, err := strconv.Atoi(v); err == nil {
			return n, nil
		}
		if n, err := strconv.ParseFloat(v, 64); err == nil {
			return n, nil
		}
		return nil, fmt.Errorf("%s is a number, not %q", d.Name, v)
	}
	return v, nil
}

// answer is given, when a flag gave it; or else the person's answer to the
// question at the terminal, fallback when they answer nothing; or else,
// without a terminal, fallback.
func (e *env) answer(in *bufio.Reader, given, question, fallback string) (string, error) {
	if given != "" {
		return given, nil
	}
	if !e.asks() {
		return fallback, nil
	}
	fmt.Fprint(e.stderr, question)
	line, err := in.ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("no answer to %q (%v)", strings.TrimSpace(question), err)
	}
	if v := strings.TrimSpace(line); v != "" {
		return v, nil
	}
	return fallback, nil
}

// pairFlag collects a repeated flag of <key>=<value> pairs.
type pairFlag struct {
	flag, want string
	pairs      map[string]string
}

func (f pairFlag) String() string { return "" }

func (f pairFlag) Set(v string) error {
	key, value, ok := strings.Cut(v, "=")
	if !ok || key == "" || value == "" {
		return fmt.Errorf("%s wants %s, got %q", f.flag, f.want, v)
	}
	f.pairs[key] = value
	return nil
}

// proposeSetup proposes, as one Proposal from whoever runs init, the
// commands of the Gates and the starter Guideline that fit the project's
// toolchain: those the Playbook doesn't have yet and no pending Proposal
// proposes. An agent session's waits for a person's Confirmation, as any
// Proposal of its own does (ADR 0026).
func (e *env) proposeSetup(pb *engine.Playbook) error {
	d, err := toolchain.Detect(e.dir)
	if err != nil {
		return err
	}
	if len(d.Toolchains) == 0 {
		fmt.Fprintln(e.stdout, "no toolchain detected")
	} else {
		names := make([]string, len(d.Toolchains))
		for i, t := range d.Toolchains {
			names[i] = t.String()
		}
		fmt.Fprintf(e.stdout, "detected %s\n", strings.Join(names, ", "))
	}
	ps := store.NewProposals(e.dir)
	existing, err := ps.List()
	if err != nil {
		return err
	}
	pending := func(match func(engine.ProposalItem) bool) bool {
		for _, p := range existing {
			if p.Status == engine.Pending && slices.ContainsFunc(p.Items, match) {
				return true
			}
		}
		return false
	}
	var items []engine.ProposalItem
	for _, g := range d.Gates {
		if _, ok := pb.Gates[g.Name]; ok || pending(func(it engine.ProposalItem) bool { return it.Gate == g.Name }) {
			continue
		}
		items = append(items, engine.ProposalItem{Gate: g.Name, Cmd: g.Cmd})
	}
	_, statErr := os.Stat(filepath.Join(e.dir, playbook.Dir, "guidelines", starterGuideline+".md"))
	if text := d.Guideline(); text != "" && errors.Is(statErr, os.ErrNotExist) && !pending(func(it engine.ProposalItem) bool { return it.Guideline == starterGuideline }) {
		items = append(items, engine.ProposalItem{Guideline: starterGuideline, Text: text})
	}
	if len(items) == 0 {
		fmt.Fprintln(e.stdout, "nothing new to propose for the toolchain")
		return nil
	}
	p, err := engine.Propose(pb, e.actor, "set up the Gates and a starter Guideline for the project's toolchain", items, nil, existing)
	if err != nil {
		return err
	}
	if err := ps.Save(p); err != nil {
		return err
	}
	if e.actor.Agent() {
		fmt.Fprintf(e.stdout, "%s%s\n", proposedForAHuman(p), e.waiting(p.ID, ""))
		return nil
	}
	fmt.Fprintf(e.stdout, "proposed %s: %s (%s, waiting for you: read it in %s, then jfl approve %s or jfl reject %s, or decide in jfl ui)\n%s", p.ID, p.Summary, plural(len(p.Items), "change"), filepath.Join(store.ProposalDir, p.ID+".yaml"), p.ID, p.ID, listItems(p))
	return nil
}

// chooseOffer is the offer --playbook names, or else the one the person
// picks at the terminal: there is no default.
func (e *env) chooseOffer(in *bufio.Reader, key string) (offer, error) {
	if key == "" {
		if !e.asks() {
			return offer{}, fmt.Errorf("%w: which Playbook? there is no default: jfl init --playbook %s\n%s", errUsage, offerKeys(), offerList())
		}
		fmt.Fprintf(e.stderr, "Which Playbook should this project use? There is no default.\n%s", offerList())
		i, err := e.choose(in, "Playbook", len(offers))
		if err != nil {
			return offer{}, err
		}
		key = offers[i].key
	}
	for _, o := range offers {
		if o.key != key {
			continue
		}
		if !o.available() {
			return offer{}, fmt.Errorf("the %s Playbook isn't in this build of jfl yet; choose another", o.name)
		}
		if o.env != "" {
			o.git = cmp.Or(e.getenv(o.env+"_GIT"), o.git)
			o.ref = cmp.Or(e.getenv(o.env+"_REF"), o.ref)
		}
		return o, nil
	}
	return offer{}, fmt.Errorf("%w: unknown Playbook %q (want %s)", errUsage, key, offerKeys())
}

// chooseAdapters is the Adapter --adapter names, or else those that have
// published into the project before, or else the one the person picks at
// the terminal.
func (e *env) chooseAdapters(in *bufio.Reader, name string) ([]*adapter.Adapter, error) {
	if name == "" {
		published, err := adapter.Published(e.dir)
		if err != nil || len(published) > 0 {
			return published, err
		}
		if !e.asks() {
			return nil, fmt.Errorf("%w: which coding agent? jfl init --adapter %s", errUsage, strings.ReplaceAll(adapterNames(), " or ", "|"))
		}
		fmt.Fprintln(e.stderr, "Which coding agent should the Playbook's Skills be published for?")
		for i, a := range adapter.Adapters {
			fmt.Fprintf(e.stderr, "  %d. %s (%s)\n", i+1, a.Agent, a.Name)
		}
		i, err := e.choose(in, "Adapter", len(adapter.Adapters))
		if err != nil {
			return nil, err
		}
		return []*adapter.Adapter{&adapter.Adapters[i]}, nil
	}
	a := adapter.Find(name)
	if a == nil {
		return nil, fmt.Errorf("%w: unknown Adapter %q (want %s)", errUsage, name, adapterNames())
	}
	return []*adapter.Adapter{a}, nil
}

// choose asks the person at the terminal for a number from 1 to n, until
// they answer one, and returns its index.
func (e *env) choose(in *bufio.Reader, what string, n int) (int, error) {
	for {
		fmt.Fprintf(e.stderr, "%s [1-%d]: ", what, n)
		line, err := in.ReadString('\n')
		if i, convErr := strconv.Atoi(strings.TrimSpace(line)); convErr == nil && i >= 1 && i <= n {
			return i - 1, nil
		}
		if err != nil {
			return 0, fmt.Errorf("no %s chosen (%v)", what, err)
		}
		fmt.Fprintf(e.stderr, "Answer a number from 1 to %d.\n", n)
	}
}

// writePlaybook writes the project's Playbook file for the offer o: one
// extending its Playbook, or one of its own with, when this build has it,
// the playbook-author Skill.
func (e *env) writePlaybook(o offer) error {
	dir := filepath.Join(e.dir, playbook.Dir)
	content, err := yaml.Marshal(map[string]string{"name": filepath.Base(e.dir)})
	if err != nil {
		return err
	}
	if o.extends() != "" {
		content = append(content, "extends: "+o.extends()+"\n"...)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "playbook.yaml"), content, 0o644); err != nil {
		return err
	}
	if o.extends() != "" {
		fmt.Fprintf(e.stdout, "wrote %s, extending the %s Playbook\n", filepath.Join(playbook.Dir, "playbook.yaml"), o.name)
	} else {
		fmt.Fprintf(e.stdout, "wrote %s, for a Playbook of your own\n", filepath.Join(playbook.Dir, "playbook.yaml"))
	}
	if o.key != own {
		return nil
	}
	skill, ok := playbook.BuiltinSkill(playbookAuthor)
	if !ok {
		return nil
	}
	path := filepath.Join(dir, "skills", playbookAuthor, "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, skill, 0o644); err != nil {
		return err
	}
	return nil
}

// unwritePlaybook removes what writePlaybook, and loading what it wrote,
// put in the project: the Playbook file, the lockfile and the cache of a
// git Base Playbook, and the Playbook's directory if nothing else is in it.
func (e *env) unwritePlaybook() {
	dir := filepath.Join(e.dir, playbook.Dir)
	for _, name := range []string{"playbook.yaml", "playbook.lock", "cache"} {
		_ = os.RemoveAll(filepath.Join(dir, name))
	}
	_ = os.Remove(dir)
}

// offerKeys lists how --playbook names each offer.
func offerKeys() string {
	keys := make([]string, len(offers))
	for i, o := range offers {
		keys[i] = o.key
	}
	return strings.Join(keys, "|")
}

// offerList lists the offers, numbered, one per line.
func offerList() string {
	s := ""
	for i, o := range offers {
		s += fmt.Sprintf("  %d. %s (%s): %s\n", i+1, o.name, o.key, o.about)
	}
	return s
}

// asks reports whether init may ask its questions: at an interactive
// terminal, and never in an agent session, which gives every answer as a
// flag (ADR 0026).
func (e *env) asks() bool {
	return e.interactive() && !e.actor.Agent()
}
