// Package playbook loads a project's Playbook from disk: a small Playbook
// file plus one YAML file per Artifact Type.
//
//	.jigflow/playbook.yaml       the Playbook file
//	.jigflow/types/*.yaml        one file per Artifact Type
//	.jigflow/skills/*/SKILL.md   one directory per Skill, named after it
//	.jigflow/personas/*.md       one file per Persona the Playbook ships, named after it
//	.jigflow/guidelines/*.md     one file per Guideline, named after it
//	.jigflow/migrations/*.yaml   Playbook Migrations, one or more per file
//
// The Playbook file may extend a single Base Playbook, laid out the same way
// in a directory of its own, whose parts the Playbook overrides by name. It
// also holds the project's Connectors and their settings, which Artifact
// Types that keep their Artifacts in a tracker name as their Store.
//
// Every Playbook also has the built-in Persona Artifact Type (see
// engine.PersonaArtifactType), declared after its own Types, whose name
// and prefix no Type of the Playbook may take. The Personas a Playbook
// ships are usable as they are, like those of the user's Persona Library
// (see Library): the person wrote them; a Persona an agent proposes is an
// Artifact, usable once a person activates it (ADR 0018).
//
// A Playbook that fails any check doesn't load: Load reports every problem
// at once, so `jfl check` and every other command print the same list.
package playbook

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jigflow-ai/jigflow/internal/engine"
	"go.yaml.in/yaml/v3"
)

// Dir is the project-relative directory holding the Playbook.
const Dir = ".jigflow"

type playbookFile struct {
	Name       string                   `yaml:"name"`
	Extends    yaml.Node                `yaml:"extends"` // the Base Playbook; see parseBaseRef
	Connectors map[string]connectorFile `yaml:"connectors"`
	// Gates gives each Gate named here, wherever the Playbook declares it,
	// the project's command: a Playbook may declare a Gate by name only and
	// leave its command to the project (ADR 0019).
	//
	//	gates:
	//	  tests: go test ./...
	Gates map[string]string `yaml:"gates"`
	// Mockups is the one folder for Mockups, relative to the project's
	// root, which the Dashboard serves sandboxed (ADR 0027).
	//
	//	mockups: .jigflow/mockups
	Mockups string `yaml:"mockups"`
}

// FileStore is the Store an Artifact Type may name to say, as it does when
// it names none, that its Artifacts are files in the repository.
const FileStore = "files"

// DefaultMarker is the AI-generated marker added to text agents write into
// a tracker when the Connector's settings don't word it.
const DefaultMarker = "_Written by an AI agent through JigFlow._"

// connectorFile is a Connector and the project's settings for it: the
// executable, the settings it is sent as they are, and how the Artifacts of
// each Artifact Type it keeps look in the tracker.
//
//	connectors:
//	  github:
//	    command: jfl-connector-github
//	    settings: {repo: acme/shop}
//	    marker: "_Drafted by an agent._"
//	    types:
//	      Ticket:
//	        statuses:
//	          in-progress: "status: doing"   # a label
//	          done: {state: closed}
//	        fields:
//	          category: {enhancement: "kind: feature"}
type connectorFile struct {
	Command  string         `yaml:"command"`
	Args     []string       `yaml:"args"`
	Marker   string         `yaml:"marker"`
	Settings map[string]any `yaml:"settings"`
	Types    map[string]struct {
		Settings map[string]any                 `yaml:"settings"`
		Statuses map[string]termFile            `yaml:"statuses"`
		Fields   map[string]map[string]termFile `yaml:"fields"`
	} `yaml:"types"`
}

// termFile is a label or state in a tracker: the label's name, or a mapping
// naming a label, a state, or both.
//
//	in-progress: "status: doing"
//	done: {state: closed}
type termFile engine.TrackerTerm

func (tf *termFile) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		return n.Decode(&tf.Label)
	}
	if n.Kind == yaml.MappingNode {
		for i := 0; i < len(n.Content); i += 2 {
			switch k := n.Content[i].Value; k {
			case "label", "state":
			default:
				return fmt.Errorf("line %d: unknown field %q (want label or state)", n.Content[i].Line, k)
			}
		}
	}
	var v struct {
		Label string `yaml:"label"`
		State string `yaml:"state"`
	}
	if err := n.Decode(&v); err != nil {
		return err
	}
	*tf = termFile{Label: v.Label, State: v.State}
	return nil
}

// linksFile is the Links of an Artifact Type: each name and the Artifact
// Type it points to, in the order they are declared.
//
//	links:
//	  part_of: Story
//	  blocked_by: Task
type linksFile struct {
	To    map[string]string
	Order []string
}

func (lf *linksFile) UnmarshalYAML(n *yaml.Node) error {
	if err := n.Decode(&lf.To); err != nil {
		return err
	}
	if n.Kind == yaml.MappingNode {
		for i := 0; i < len(n.Content); i += 2 {
			lf.Order = append(lf.Order, n.Content[i].Value)
		}
	}
	return nil
}

type typeFile struct {
	Name        string                     `yaml:"name"`
	Prefix      string                     `yaml:"prefix"`
	Store       string                     `yaml:"store"`
	Fields      map[string][]string        `yaml:"fields"`
	Statuses    []string                   `yaml:"statuses"`
	Initial     []string                   `yaml:"initial"`
	Final       []string                   `yaml:"final"`
	Inbox       []string                   `yaml:"inbox"`
	Bindings    map[string]bindingFile     `yaml:"bindings"`
	Links       linksFile                  `yaml:"links"`
	Readiness   map[string][]conditionFile `yaml:"readiness"`
	Transitions []struct {
		From    string          `yaml:"from"`
		To      string          `yaml:"to"`
		Human   humanFile       `yaml:"human"`
		Guards  []conditionFile `yaml:"guards"`
		Gates   []commandFile   `yaml:"gates"`
		Actions []commandFile   `yaml:"actions"`
	} `yaml:"transitions"`
}

// humanFile says whether a Transition is a Human Transition: true, or
// dashboard when the Playbook requires making it in the Dashboard.
//
//	human: true
//	human: dashboard
type humanFile int

const (
	humanNo humanFile = iota
	humanYes
	humanDashboard
)

func (h *humanFile) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode && n.Value == "dashboard" {
		*h = humanDashboard
		return nil
	}
	var b bool
	if err := n.Decode(&b); err != nil {
		return fmt.Errorf("line %d: human wants true, false or dashboard, not %q", n.Line, n.Value)
	}
	*h = humanNo
	if b {
		*h = humanYes
	}
	return nil
}

// bindingFile is a Binding: the name of its Skill, or a mapping that also
// says how the Skill should run.
//
//	in-progress: implement
//	in-progress: {skill: implement, fresh: true, isolated: true}
type bindingFile struct {
	Skill    string `yaml:"skill"`
	Fresh    bool   `yaml:"fresh"`    // run the Skill in a fresh session
	Isolated bool   `yaml:"isolated"` // run the Skill in an isolated sub-agent
}

func (b *bindingFile) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		return n.Decode(&b.Skill)
	}
	// A misspelt key is an error, as everywhere else in a Playbook; a
	// Node's Decode doesn't check for them.
	if n.Kind == yaml.MappingNode {
		for i := 0; i < len(n.Content); i += 2 {
			switch k := n.Content[i].Value; k {
			case "skill", "fresh", "isolated":
			default:
				return fmt.Errorf("line %d: unknown Binding field %q (want skill, fresh or isolated)", n.Content[i].Line, k)
			}
		}
	}
	type plain bindingFile
	return n.Decode((*plain)(b))
}

// skillFile is the frontmatter of a SKILL.md. Changes is a pointer so a
// Skill that doesn't say whether it changes anything can be told apart.
type skillFile struct {
	Description string   `yaml:"description"`
	Changes     *bool    `yaml:"changes"`
	Invocation  string   `yaml:"invocation"`
	Guidelines  []string `yaml:"guidelines"`
	Personas    []struct {
		Name     string `yaml:"name"`
		Fallback string `yaml:"fallback"`
	} `yaml:"personas"`
}

type conditionFile struct {
	Kind     string   `yaml:"kind"`
	Link     string   `yaml:"link"`
	Statuses []string `yaml:"statuses"`
	Min      int      `yaml:"min"`
}

type commandFile struct {
	Name string `yaml:"name"`
	Cmd  string `yaml:"cmd"`
}

// Load reads the Playbook of the project rooted at root, merged over its
// Base Playbook when it extends one. Artifact Types are declared in the
// lexical order of their file names, a Base Playbook's first.
func Load(root string) (*engine.Playbook, error) {
	return load(root, os.DirFS(filepath.Join(root, Dir)))
}

// load reads the Playbook whose own files are in fsys, laid out as Dir is,
// for the project rooted at root, where its Base Playbook is resolved.
func load(root string, fsys fs.FS) (*engine.Playbook, error) {
	own, err := readLayer(fsys, Dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("no Playbook found: %s is missing", path.Join(Dir, "playbook.yaml"))
	}
	if err != nil {
		return nil, err
	}
	l := own
	var base *layer
	if own.extends != nil {
		base, err = resolveBase(root, own.extends)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path.Join(Dir, "playbook.yaml"), err)
		}
		l = merge(base, own)
	}
	giveGatesCommands(l.types, l.gates)
	pb := &engine.Playbook{Name: own.name, Types: l.types, Skills: l.skills, Personas: l.personas, Guidelines: l.guidelines, Connectors: l.connectors, Gates: l.gates, Mockups: l.mockups, Origins: origins(base, own)}
	problems := builtinTypeProblems(pb, l.typeFiles)
	problems = append(problems, mockupsProblems(pb)...)
	pb.Types = append(pb.Types, engine.PersonaArtifactType())
	pb.Origins.Types[engine.PersonaType] = engine.Origin{Builtin: true}
	migrationProblems, err := readMigrations(fsys, Dir, pb)
	if err != nil {
		return nil, err
	}
	problems = append(problems, migrationProblems...)
	for _, name := range slices.Sorted(maps.Keys(l.skills)) {
		problems = append(problems, l.skillProblems[name]...)
	}
	if problems = append(problems, check(pb, l.typeFiles, l.skillFiles, l.connectorFiles)...); len(problems) > 0 {
		return nil, &Invalid{Problems: problems}
	}
	return pb, nil
}

// origins says where each part of the Playbook own, extending base, if
// any, comes from.
func origins(base, own *layer) engine.Origins {
	// of says where each of the parts parts gives a layer comes from, by
	// name, and the file declaring it.
	of := func(parts func(*layer) map[string]string) map[string]engine.Origin {
		m := map[string]engine.Origin{}
		if base != nil {
			for name, file := range parts(base) {
				m[name] = engine.Origin{Base: base.label, File: file}
			}
		}
		for name, file := range parts(own) {
			o := m[name]
			o.Project, o.File = true, file
			m[name] = o
		}
		return m
	}
	// files names the file of each part a map of the layer holds, in dir.
	files := func(parts map[string]string, l *layer, dir string) map[string]string {
		m := map[string]string{}
		for name := range parts {
			m[name] = in(l.label, dir+"/"+name+".md")
		}
		return m
	}
	var mockups engine.Origin
	switch {
	case own.mockups != "":
		mockups = engine.Origin{Project: true, File: in(own.label, "playbook.yaml")}
		if base != nil && base.mockups != "" {
			mockups.Base = base.label
		}
	case base != nil && base.mockups != "":
		mockups = engine.Origin{Base: base.label, File: in(base.label, "playbook.yaml")}
	}
	return engine.Origins{
		Mockups:    mockups,
		Types:      of(func(l *layer) map[string]string { return l.typeFiles }),
		Skills:     of(func(l *layer) map[string]string { return l.skillFiles }),
		Personas:   of(func(l *layer) map[string]string { return files(l.personas, l, "personas") }),
		Guidelines: of(func(l *layer) map[string]string { return files(l.guidelines, l, "guidelines") }),
		Gates: of(func(l *layer) map[string]string {
			m := map[string]string{}
			for name := range l.gates {
				m[name] = in(l.label, "playbook.yaml")
			}
			return m
		}),
	}
}

// mockupsProblems reports a Mockup folder outside the project, or the
// project itself: the Dashboard serves every file in it (ADR 0027). It
// cleans the folder's path otherwise.
func mockupsProblems(pb *engine.Playbook) []string {
	if pb.Mockups == "" {
		return nil
	}
	dir := path.Clean(filepath.ToSlash(pb.Mockups))
	if path.IsAbs(dir) || filepath.IsAbs(pb.Mockups) || filepath.VolumeName(pb.Mockups) != "" || dir == "." || dir == ".." || strings.HasPrefix(dir, "../") {
		return []string{fmt.Sprintf("%s: mockups %q must name a folder inside the project, relative to its root, such as .jigflow/mockups", pb.Origins.Mockups.File, pb.Mockups)}
	}
	pb.Mockups = dir
	return nil
}

// builtinTypeProblems reports Artifact Types a Playbook declares that
// would clash with the built-in Persona Type, by name or by prefix, and
// drops them from pb, so that the built-in one is the only Persona Type.
func builtinTypeProblems(pb *engine.Playbook, typeFiles map[string]string) []string {
	var problems []string
	persona := engine.PersonaArtifactType()
	pb.Types = slices.DeleteFunc(pb.Types, func(t *engine.ArtifactType) bool {
		switch {
		case t.Name == persona.Name:
			problems = append(problems, fmt.Sprintf("%s: Artifact Type %q is built in; a Playbook can't declare it", typeFiles[t.Name], t.Name))
		case t.Prefix == persona.Prefix:
			problems = append(problems, fmt.Sprintf("%s: prefix %q is the built-in %s Type's", typeFiles[t.Name], t.Prefix, persona.Name))
		default:
			return false
		}
		return true
	})
	return problems
}

// layer is one Playbook as read from its files, before it is merged with
// the Playbook extending it. Every file is named by label, the directory
// the Playbook's author knows it by, for messages.
type layer struct {
	name       string
	label      string // the directory the Playbook's author knows it by
	extends    *baseRef
	types      []*engine.ArtifactType // in declaration order
	skills     map[string]*engine.Skill
	personas   map[string]string // name -> its Markdown
	guidelines map[string]string // name -> its Markdown
	connectors map[string]*engine.Connector
	gates      map[string]string // Gate name -> the command the Playbook file gives it
	mockups    string            // the Mockup folder, if the Playbook file declares one

	typeFiles, skillFiles, connectorFiles map[string]string   // name -> the file declaring it
	skillProblems                         map[string][]string // Skill -> what its file lacks
}

// readLayer reads the Playbook held in fsys. A missing playbook.yaml is an
// error wrapping os.ErrNotExist.
func readLayer(fsys fs.FS, label string) (*layer, error) {
	var pf playbookFile
	if err := readYAML(fsys, label, "playbook.yaml", &pf); err != nil {
		return nil, err
	}
	l := &layer{
		name:           pf.Name,
		label:          label,
		skills:         map[string]*engine.Skill{},
		personas:       map[string]string{},
		guidelines:     map[string]string{},
		connectors:     map[string]*engine.Connector{},
		gates:          pf.Gates,
		mockups:        pf.Mockups,
		typeFiles:      map[string]string{},
		skillFiles:     map[string]string{},
		connectorFiles: map[string]string{},
		skillProblems:  map[string][]string{},
	}
	for name, cf := range pf.Connectors {
		l.connectors[name] = connector(name, cf)
		l.connectorFiles[name] = in(label, "playbook.yaml")
	}
	if !pf.Extends.IsZero() {
		ref, err := parseBaseRef(&pf.Extends)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", in(label, "playbook.yaml"), err)
		}
		l.extends = ref
	}
	if err := l.readTypes(fsys, label); err != nil {
		return nil, err
	}
	if err := l.readSkills(fsys, label); err != nil {
		return nil, err
	}
	if err := readMarkdown(fsys, "personas", l.personas); err != nil {
		return nil, err
	}
	if err := readMarkdown(fsys, "guidelines", l.guidelines); err != nil {
		return nil, err
	}
	return l, nil
}

// readMarkdown reads each Markdown file in dir into into, by its name.
func readMarkdown(fsys fs.FS, dir string, into map[string]string) error {
	paths, err := fs.Glob(fsys, path.Join(dir, "*.md"))
	if err != nil {
		return err
	}
	for _, p := range paths {
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		into[strings.TrimSuffix(path.Base(p), ".md")] = string(data)
	}
	return nil
}

func (l *layer) readTypes(fsys fs.FS, label string) error {
	paths, err := fs.Glob(fsys, "types/*.yaml")
	if err != nil {
		return err
	}
	slices.Sort(paths)
	for _, p := range paths {
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		rel := in(label, p)
		var tf typeFile
		if err := decodeYAML(data, rel, &tf); err != nil {
			return err
		}
		if tf.Name == "" || tf.Prefix == "" {
			return fmt.Errorf("%s: an Artifact Type needs a name and a prefix", rel)
		}
		if l.typeFiles[tf.Name] != "" {
			return fmt.Errorf("%s: Artifact Type %q is declared twice", rel, tf.Name)
		}
		if tf.Store == FileStore {
			tf.Store = ""
		}
		t := &engine.ArtifactType{
			Name:      tf.Name,
			Prefix:    tf.Prefix,
			Store:     tf.Store,
			Fields:    tf.Fields,
			Statuses:  tf.Statuses,
			Initial:   tf.Initial,
			Final:     tf.Final,
			Inbox:     tf.Inbox,
			Links:     tf.Links.To,
			LinkOrder: tf.Links.Order,
			File:      rel,
			Source:    string(data),
		}
		for status, b := range tf.Bindings {
			if t.Bindings == nil {
				t.Bindings = map[string]string{}
			}
			t.Bindings[status] = b.Skill
			if b.Fresh || b.Isolated {
				if t.Hints == nil {
					t.Hints = map[string]engine.Hints{}
				}
				t.Hints[status] = engine.Hints{Fresh: b.Fresh, Isolated: b.Isolated}
			}
		}
		for status, cfs := range tf.Readiness {
			if t.Readiness == nil {
				t.Readiness = map[string][]engine.Condition{}
			}
			t.Readiness[status] = conditions(cfs)
		}
		for _, tr := range tf.Transitions {
			if err := checkGates(tr.Gates, tr.From, tr.To); err != nil {
				return fmt.Errorf("%s: %w", rel, err)
			}
			if err := checkCommands(tr.Actions, "an Action", tr.From, tr.To); err != nil {
				return fmt.Errorf("%s: %w", rel, err)
			}
			t.Transitions = append(t.Transitions, engine.Transition{
				From:      tr.From,
				To:        tr.To,
				Human:     tr.Human != humanNo,
				Dashboard: tr.Human == humanDashboard,
				Guards:    conditions(tr.Guards),
				Gates:     commands(tr.Gates),
				Actions:   commands(tr.Actions),
			})
		}
		l.types = append(l.types, t)
		l.typeFiles[t.Name] = rel
	}
	return nil
}

// readSkills reads every Skill: a directory under skills/ holding a
// SKILL.md whose YAML frontmatter declares whether it changes code or
// Artifacts and its Invocation Mode. The problems of a Skill that doesn't
// are kept with it, so a Skill overriding it drops them.
func (l *layer) readSkills(fsys fs.FS, label string) error {
	paths, err := fs.Glob(fsys, "skills/*/SKILL.md")
	if err != nil {
		return err
	}
	slices.Sort(paths)
	for _, p := range paths {
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		rel := in(label, p)
		var sf skillFile
		fm, prompt, ok := frontmatter(data)
		if ok {
			if err := decodeYAML(fm, rel, &sf); err != nil {
				return err
			}
		}
		name := path.Base(path.Dir(p))
		var problems []string
		if sf.Changes == nil {
			problems = append(problems, rel+": a Skill must declare changes: true or false")
		}
		switch sf.Invocation {
		case engine.InvokedByUser, engine.InvokedByAgent, engine.InvokedByBinding:
		case "":
			problems = append(problems, rel+": a Skill must declare its Invocation Mode (invocation: user, agent or bound)")
		default:
			problems = append(problems, fmt.Sprintf("%s: unknown Invocation Mode %q (want user, agent or bound)", rel, sf.Invocation))
		}
		s := &engine.Skill{
			Name:        name,
			Description: sf.Description,
			Changes:     sf.Changes != nil && *sf.Changes,
			Invocation:  sf.Invocation,
			Guidelines:  sf.Guidelines,
			Prompt:      string(prompt),
		}
		for _, pf := range sf.Personas {
			s.Personas = append(s.Personas, engine.PersonaRef{Name: pf.Name, Fallback: pf.Fallback})
		}
		l.skills[name] = s
		l.skillFiles[name] = rel
		l.skillProblems[name] = problems
	}
	return nil
}

// merge lays the extending Playbook own over its Base Playbook: an Artifact
// Type, Skill, Persona or Guideline own declares replaces the base's one of
// the same name, and everything else the base declares is kept. A replaced
// Artifact Type keeps the base's place in the declaration order; own's other
// Types follow the base's.
func merge(base, own *layer) *layer {
	m := &layer{
		skills:         maps.Clone(base.skills),
		personas:       maps.Clone(base.personas),
		guidelines:     maps.Clone(base.guidelines),
		connectors:     maps.Clone(base.connectors),
		gates:          maps.Clone(base.gates),
		mockups:        cmp.Or(own.mockups, base.mockups),
		typeFiles:      maps.Clone(base.typeFiles),
		skillFiles:     maps.Clone(base.skillFiles),
		connectorFiles: maps.Clone(base.connectorFiles),
		skillProblems:  maps.Clone(base.skillProblems),
	}
	for _, t := range base.types {
		if o := slices.IndexFunc(own.types, func(o *engine.ArtifactType) bool { return o.Name == t.Name }); o >= 0 {
			t = own.types[o]
		}
		m.types = append(m.types, t)
	}
	for _, t := range own.types {
		if !slices.ContainsFunc(base.types, func(b *engine.ArtifactType) bool { return b.Name == t.Name }) {
			m.types = append(m.types, t)
		}
	}
	maps.Copy(m.typeFiles, own.typeFiles)
	maps.Copy(m.skills, own.skills)
	maps.Copy(m.skillFiles, own.skillFiles)
	maps.Copy(m.skillProblems, own.skillProblems)
	maps.Copy(m.personas, own.personas)
	maps.Copy(m.guidelines, own.guidelines)
	maps.Copy(m.connectors, own.connectors)
	maps.Copy(m.connectorFiles, own.connectorFiles)
	if m.gates == nil {
		m.gates = map[string]string{}
	}
	maps.Copy(m.gates, own.gates)
	return m
}

// connector is the Connector a Playbook file declares under name.
func connector(name string, cf connectorFile) *engine.Connector {
	c := &engine.Connector{Name: name, Command: cf.Command, Args: cf.Args, Marker: cf.Marker, Settings: cf.Settings}
	if c.Marker == "" {
		c.Marker = DefaultMarker
	}
	for typeName, tm := range cf.Types {
		m := engine.TrackerMapping{Settings: tm.Settings}
		for status, term := range tm.Statuses {
			if m.Statuses == nil {
				m.Statuses = map[string]engine.TrackerTerm{}
			}
			m.Statuses[status] = engine.TrackerTerm(term)
		}
		for field, values := range tm.Fields {
			if m.Fields == nil {
				m.Fields = map[string]map[string]engine.TrackerTerm{}
			}
			m.Fields[field] = map[string]engine.TrackerTerm{}
			for value, term := range values {
				m.Fields[field][value] = engine.TrackerTerm(term)
			}
		}
		if c.Types == nil {
			c.Types = map[string]engine.TrackerMapping{}
		}
		c.Types[typeName] = m
	}
	return c
}

// frontmatter returns the YAML block a Markdown file starts with, if any,
// and the Markdown after it: the whole file when it has none.
func frontmatter(data []byte) (fm, body []byte, ok bool) {
	rest, ok := bytes.CutPrefix(data, []byte("---\n"))
	if !ok {
		return nil, data, false
	}
	if body, ok := bytes.CutPrefix(rest, []byte("---\n")); ok {
		return nil, body, true
	}
	fm, body, ok = bytes.Cut(rest, []byte("\n---\n"))
	if !ok {
		return nil, data, false
	}
	return fm, body, true
}

// in names the file name of the Playbook labelled label, for messages. A
// label may be a URL, which path.Join would mangle.
func in(label, name string) string {
	return strings.TrimSuffix(label, "/") + "/" + name
}

// readYAML decodes the file name in fsys, reporting it as label/name.
func readYAML(fsys fs.FS, label, name string, v any) error {
	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		return err
	}
	return decodeYAML(data, in(label, name), v)
}

func decodeYAML(data []byte, file string, v any) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true) // a misspelt field is an error, not silently ignored
	if err := dec.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("%s: %w", file, err)
	}
	return nil
}

func commands(cfs []commandFile) []engine.Command {
	var cs []engine.Command
	for _, c := range cfs {
		cs = append(cs, engine.Command{Name: c.Name, Cmd: c.Cmd})
	}
	return cs
}

func conditions(cfs []conditionFile) []engine.Condition {
	var cs []engine.Condition
	for _, c := range cfs {
		cs = append(cs, engine.Condition{Kind: c.Kind, Link: c.Link, Statuses: c.Statuses, Min: c.Min})
	}
	return cs
}

// giveGatesCommands gives every Gate of types that gates names the command
// it gives, replacing any the Gate is declared with.
func giveGatesCommands(types []*engine.ArtifactType, gates map[string]string) {
	for _, t := range types {
		for i := range t.Transitions {
			for j, g := range t.Transitions[i].Gates {
				if cmd, ok := gates[g.Name]; ok {
					t.Transitions[i].Gates[j].Cmd = cmd
				}
			}
		}
	}
}

// checkGates refuses a Gate that lacks a name, used to report it and to
// give it its command from the Playbook file.
func checkGates(cfs []commandFile, from, to string) error {
	for _, c := range cfs {
		if c.Name == "" {
			return fmt.Errorf("a Gate on %q → %q needs a name", from, to)
		}
	}
	return nil
}

// checkCommands refuses a Gate or Action that lacks a name (used to report
// it) or a cmd (what runs).
func checkCommands(cfs []commandFile, what, from, to string) error {
	for _, c := range cfs {
		if c.Name == "" || c.Cmd == "" {
			return fmt.Errorf("%s on %q → %q needs a name and a cmd", what, from, to)
		}
	}
	return nil
}
