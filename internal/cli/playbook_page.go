package cli

import (
	"cmp"
	"fmt"
	"html/template"
	"maps"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/jigflow-ai/jigflow/internal/adapter"
	"github.com/jigflow-ai/jigflow/internal/engine"
	"github.com/jigflow-ai/jigflow/internal/playbook"
	"github.com/jigflow-ai/jigflow/internal/store"
)

// playbookPage is what the project's Playbook is made of, and where each
// part of it comes from. The person who opened the link jfl ui printed
// changes the values of the Playbook file on it, each change a Proposal
// they confirm as they make it (ADR 0030); to anyone else it is read-only.
type playbookPage struct {
	chrome
	Outcome    *outcome  // what the person's last change did, if they just made one
	CanAct     bool      // whether the person viewing it may change the Playbook file here
	Cannot     string    // why not, when they may not
	Base       *basePart // the Base Playbook, when the Playbook file extends one
	Types      []typePart
	Bindings   []bindingPart
	Skills     []skillPart
	Guidelines []namedPart
	Personas   []personaPart
	Gates      []gatePart
	Mockups    *mockupsPart // the Mockup folder, when the Playbook declares one
	Connectors []connectorPart
	Adapters   []adapterPart
}

// connectorPart is a Connector as the Playbook page shows it: its values
// in the Playbook file, which the person changes in one form (ADR 0030).
// Its credentials are never among them: a Connector reads them from its
// environment.
type connectorPart struct {
	Name, From      string
	Command, Marker string
	Args            string // one per line
	Settings        []settingField
	Types           []connectorTypePart // the Artifact Types it keeps
	// Base is whether the project's Playbook file declares it over the
	// Base Playbook's declaration, which it can go back to.
	Base bool
	// Pending are the pending Proposals that change it too, which the
	// person's own change doesn't wait for (ADR 0030).
	Pending []string
}

// connectorTypePart is an Artifact Type a Connector keeps, with the
// settings merged over the Connector's for it, and the label or state each
// of its Statuses and field values is mapped to.
type connectorTypePart struct {
	Name     string
	Settings []settingField
	Mappings []mappingField
	Prefix   string // what the names of its fields start with
}

// mappingField is a Status, or a field's value, of an Artifact Type a
// Connector keeps, as the Playbook page's form draws it, with the label and
// the state it is mapped to: by default, a label of its own name. Changing
// it relabels the Artifacts carrying the one it replaces (ADR 0030).
type mappingField struct {
	Name         string // e.g. status in-progress, or category enhancement
	Field        string // what the names of its label's and state's fields start with
	Label, State string
	status       string // the Status it maps, or empty for a field's value
	field, value string // the field and the value it maps
}

// mappingFields draws each Status of the Artifact Type t, then each value
// of each of its fields, in the order t declares them, with what m maps it
// to, as fields whose names start with prefix.
func mappingFields(prefix string, t *engine.ArtifactType, m engine.TrackerMapping) []mappingField {
	var fields []mappingField
	for _, st := range t.Statuses {
		term := m.StatusTerm(st)
		fields = append(fields, mappingField{Name: "status " + st, Field: prefix + "status." + st + ".", Label: term.Label, State: term.State, status: st})
	}
	for _, field := range slices.Sorted(maps.Keys(t.Fields)) {
		for _, v := range t.Fields[field] {
			term := m.FieldTerm(field, v)
			fields = append(fields, mappingField{Name: field + " " + v, Field: prefix + "field." + field + "." + v + ".", Label: term.Label, State: term.State, field: field, value: v})
		}
	}
	return fields
}

// posted reads the mapping back from the form the person posted: the label
// or state as the Playbook file writes it, nil when they emptied both, which
// makes it a label of its own name again, and whether they changed it.
func (f mappingField) posted(form url.Values) (any, bool) {
	if !form.Has(f.Field+"label") && !form.Has(f.Field+"state") {
		return nil, false
	}
	t := engine.TrackerTerm{Label: strings.TrimSpace(form.Get(f.Field + "label")), State: strings.TrimSpace(form.Get(f.Field + "state"))}
	switch {
	case t == engine.TrackerTerm{Label: f.Label, State: f.State}:
		return nil, false
	case t == engine.TrackerTerm{}:
		return nil, true
	}
	return t.Value(), true
}

// settingField is a Connector's setting as the Playbook page's form draws
// it, and reads it back: a toggle for a true or false value, a text field
// for any other. It is drawn from the value the Playbook file gives it,
// until Connectors describe their settings (ADR 0031).
type settingField struct {
	Name  string
	Field string // the name of the form's field
	Value string // what a text field holds
	// Toggle is whether it is a toggle, On whether the toggle is on.
	Toggle, On bool
	now        any
}

// newSettingField draws the setting name, whose value is now, as the field
// named prefix + "setting." + name.
func newSettingField(prefix, name string, now any) settingField {
	f := settingField{Name: name, Field: prefix + "setting." + name, now: now}
	if on, ok := now.(bool); ok {
		f.Toggle, f.On = true, on
	} else if now != nil {
		f.Value = fmt.Sprint(now)
	}
	return f
}

// posted reads the setting back from the form the person posted: its new
// value, nil to remove it, and whether they changed it. An emptied text
// field removes the setting; a number stays a number while it reads as one.
func (f settingField) posted(form url.Values) (any, bool) {
	if f.Toggle {
		on := form.Has(f.Field)
		return on, on != f.On
	}
	if !form.Has(f.Field) {
		return nil, false
	}
	text := strings.TrimSpace(form.Get(f.Field))
	switch {
	case text == f.Value:
		return nil, false
	case text == "":
		return nil, true
	}
	switch f.now.(type) {
	case int:
		if n, err := strconv.Atoi(text); err == nil {
			return n, true
		}
	case float64:
		if n, err := strconv.ParseFloat(text, 64); err == nil {
			return n, true
		}
	}
	return text, true
}

// settingFields draws each of settings, in the order of their names, as a
// field whose name starts with prefix.
func settingFields(prefix string, settings map[string]any) []settingField {
	var fields []settingField
	for _, name := range slices.Sorted(maps.Keys(settings)) {
		fields = append(fields, newSettingField(prefix, name, settings[name]))
	}
	return fields
}

// adapterPart is an Adapter, and whether the Playbook's Skills are published
// through it: what jfl init publishes through again (ADR 0030).
type adapterPart struct {
	Name, Agent string
	Published   bool
}

// gatePart is a Gate on one Transition, the command it runs, and where
// that command comes from.
type gatePart struct {
	Name, Type, Transition, Cmd string
	CmdFrom                     string // who gives it its command
	Missing                     bool   // declared by name only, with no command yet
	From                        string // the Artifact Type's origin, which declares it
	// Base is whether the project's Playbook file gives it a command over
	// one its Base Playbook's gives, which it can go back to.
	Base bool
	// Pending are the pending Proposals that change its command too, which
	// the person's own change doesn't wait for (ADR 0030).
	Pending []string
}

// mockupsPart is the Mockup folder as the Playbook page shows it.
type mockupsPart struct {
	Folder, From string
	// Base is whether the project's Playbook file gives it over one its
	// Base Playbook's gives, which it can go back to.
	Base bool
	// Pending are the pending Proposals that change it too, which the
	// person's own change doesn't wait for (ADR 0030).
	Pending []string
}

// basePart is the Base Playbook the Playbook file extends, as the Playbook
// page shows it: only a git one's ref changes there, switching to another
// Base Playbook staying with playbook-author, jfl check and jfl simulate
// (ADR 0030).
type basePart struct {
	engine.Base
	// Pending are the pending Proposals that change its ref too, which
	// the person's own change doesn't wait for (ADR 0030).
	Pending []string
}

// namedPart is a part of the Playbook known by its name alone, such as a
// Guideline.
type namedPart struct {
	Name, From string
}

// personaPart is a Persona as the Playbook page lists it: shipped, by the
// Playbook or the Persona Library, and usable as it is, or a Persona
// Artifact, proposed, active or retired (ADR 0018).
type personaPart struct {
	Name, State string
	From        string // where a shipped one comes from
	Artifact    string // the Persona Artifact deciding it, if one does
	Overrides   string // where the shipped one it overrides comes from
	Proposed    string // a Persona Artifact proposed to replace the shipped one
	file        string // the file of the shipped one, when it isn't an Artifact
	markdown    string // the Markdown describing it
}

// bindingPart is a Binding: the Skill working on the Artifacts of a Type
// in one Status.
type bindingPart struct {
	Type, Status, Skill string
	Hints               []string // e.g. a fresh session
	From                string   // the Artifact Type's origin, which declares it
}

// skillPart is a Skill as the Playbook page lists it.
type skillPart struct {
	Name, Description, Invocation, From string
	Bound                               []string // "Type Status" of each Binding naming it
}

// typePart is an Artifact Type as the Playbook page lists it.
type typePart struct {
	Name, Prefix, Store, From string
}

// playbookView builds the Playbook page as the person making the request
// sees it, with the outcome of the change they just made, if any.
func (d *dashboard) playbookView(r *http.Request, done *outcome) func() (any, error) {
	return func() (any, error) {
		v, err := d.e.playbookPage()
		if err != nil && done != nil {
			// The change was made, or refused, all the same.
			said := strings.TrimSpace(done.Problem + "\n" + done.Said)
			return nil, fmt.Errorf("%s\n\nThe Playbook can't be shown now: %w", said, err)
		}
		if err != nil {
			return nil, err
		}
		v.Outcome = done
		if err := d.mayAct(r); err != nil {
			v.Cannot = err.Error()
		} else {
			v.CanAct = true
		}
		return v, nil
	}
}

// playbookPage is what the project's Playbook is made of, as anyone sees it.
func (e *env) playbookPage() (playbookPage, error) {
	pb, _, err := e.load()
	if err != nil {
		return playbookPage{}, err
	}
	v := playbookPage{chrome: chrome{Playbook: pb.Name, Page: "playbook"}}
	for _, t := range pb.Types {
		v.Types = append(v.Types, typePart{t.Name, t.Prefix, cmp.Or(t.Store, playbook.FileStore), from(pb.Origins.Types[t.Name])})
	}
	bound := map[string][]string{}
	for _, t := range pb.Types {
		for _, s := range t.Statuses {
			skill, ok := t.Bindings[s]
			if !ok {
				continue
			}
			b := bindingPart{Type: t.Name, Status: s, Skill: skill, From: from(pb.Origins.Types[t.Name])}
			if h := t.Hints[s]; h.Fresh {
				b.Hints = append(b.Hints, "fresh session")
			}
			if h := t.Hints[s]; h.Isolated {
				b.Hints = append(b.Hints, "isolated")
			}
			v.Bindings = append(v.Bindings, b)
			bound[skill] = append(bound[skill], t.Name+" "+s)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(pb.Skills)) {
		s := pb.Skills[name]
		v.Skills = append(v.Skills, skillPart{Name: name, Description: s.Description, Invocation: s.Invocation, From: from(pb.Origins.Skills[name]), Bound: bound[name]})
	}
	for _, name := range slices.Sorted(maps.Keys(pb.Guidelines)) {
		v.Guidelines = append(v.Guidelines, namedPart{name, from(pb.Origins.Guidelines[name])})
	}
	proposals, err := store.NewProposals(e.dir).List()
	if err != nil {
		return playbookPage{}, err
	}
	changing := engine.Changing(proposals)
	for _, t := range pb.Types {
		for _, tr := range t.Transitions {
			for _, g := range tr.Gates {
				o := pb.Origins.Gates[g.Name]
				v.Gates = append(v.Gates, gatePart{
					Name: g.Name, Type: t.Name, Transition: tr.From + " → " + tr.To, Cmd: g.Cmd,
					CmdFrom: gateCmdFrom(pb, g), Missing: g.Cmd == "",
					From:    from(pb.Origins.Types[t.Name]),
					Base:    o.Project && o.Base != "",
					Pending: changing[engine.ProposalItem{Gate: g.Name}.FileValue()],
				})
			}
		}
	}
	if pb.Base != nil {
		v.Base = &basePart{Base: *pb.Base, Pending: changing[engine.ProposalItem{BaseRef: pb.Base.Ref}.FileValue()]}
	}
	if pb.Mockups != "" {
		o := pb.Origins.Mockups
		v.Mockups = &mockupsPart{
			Folder: pb.Mockups, From: from(o),
			Base:    o.Project && o.Base != "",
			Pending: changing[engine.ProposalItem{Mockups: pb.Mockups}.FileValue()],
		}
	}
	for _, name := range slices.Sorted(maps.Keys(pb.Connectors)) {
		v.Connectors = append(v.Connectors, connectorView(pb, pb.Connectors[name], changing))
	}
	if v.Personas, err = e.personaParts(pb); err != nil {
		return playbookPage{}, err
	}
	published, err := adapter.Published(e.dir)
	if err != nil {
		return playbookPage{}, err
	}
	for i, a := range adapter.Adapters {
		v.Adapters = append(v.Adapters, adapterPart{a.Name, a.Agent, slices.Contains(published, &adapter.Adapters[i])})
	}
	return v, nil
}

// connectorView is the Connector c of pb as the Playbook page shows it;
// changing names the pending Proposals changing each value of the Playbook
// file.
func connectorView(pb *engine.Playbook, c *engine.Connector, changing map[string][]string) connectorPart {
	o := pb.Origins.Connectors[c.Name]
	v := connectorPart{
		Name: c.Name, From: from(o),
		Command: c.Command, Marker: c.Marker, Args: strings.Join(c.Args, "\n"),
		Settings: settingFields("", c.Settings),
		Base:     o.Project && o.Base != "",
		Pending:  changing[engine.ProposalItem{Connector: c.Name}.FileValue()],
	}
	for _, t := range pb.Types {
		if t.Store != c.Name {
			continue
		}
		prefix := "type." + t.Name + "."
		m := c.Types[t.Name]
		v.Types = append(v.Types, connectorTypePart{Name: t.Name, Prefix: prefix, Settings: settingFields(prefix, m.Settings), Mappings: mappingFields(prefix, t, m)})
	}
	return v
}

// gateCmdFrom says who gives the Gate g its command: its Transition, or a
// Playbook file under gates, which replaces the Transition's (ADR 0019).
func gateCmdFrom(pb *engine.Playbook, g engine.Command) string {
	o, given := pb.Origins.Gates[g.Name]
	switch {
	case g.Cmd == "":
		return "no command yet: give it one under gates in " + playbook.Dir + "/playbook.yaml"
	case given && o.Project && o.Base != "":
		return "given by the project's Playbook file, over " + baseName(o.Base) + "'s"
	case given && o.Project:
		return "given by the project's Playbook file"
	case given:
		return "given by the Playbook file of " + baseName(o.Base)
	}
	return "declared with its Transition"
}

// personaParts is every Persona the project knows, by name: those the
// Persona Library and the Playbook ship, and the Persona Artifacts, each
// in the state that decides whether agents may use it, as publishing
// decides (see engine.ActivePersonas).
func (e *env) personaParts(pb *engine.Playbook) ([]personaPart, error) {
	library, err := playbook.Library(e.getenv)
	if err != nil {
		return nil, err
	}
	// shipped are the Personas usable as they are, each as the page
	// lists it, the Playbook's overriding the Library's.
	shipped := map[string]personaPart{}
	for name, md := range library {
		shipped[name] = personaPart{From: "Persona Library", file: filepath.Join(playbook.LibraryDir(e.getenv), name+".md"), markdown: md}
	}
	for name, md := range pb.Personas {
		o := pb.Origins.Personas[name]
		shipped[name] = personaPart{From: from(o), file: o.File, markdown: md}
	}
	files := store.NewFile(e.dir)
	all, err := files.List()
	if err != nil {
		return nil, err
	}
	artifacts := map[string]engine.Artifact{}
	for _, a := range all {
		if a.Type == engine.PersonaType {
			artifacts[a.Title] = a
		}
	}
	var parts []personaPart
	names := map[string]bool{}
	for name := range shipped {
		names[name] = true
	}
	for name := range artifacts {
		names[name] = true
	}
	for _, name := range slices.Sorted(maps.Keys(names)) {
		a, decided := artifacts[name]
		p, isShipped := shipped[name]
		p.Name = name
		// A proposed Persona overrides nothing yet.
		if decided && a.Status == engine.PersonaProposed && isShipped {
			p.Proposed, decided = a.ID, false
		}
		if decided {
			body, err := files.Body(a.ID)
			if err != nil {
				return nil, err
			}
			if isShipped {
				p.Overrides = p.From
			}
			p.State, p.Artifact, p.From, p.file = a.Status, a.ID, "", ""
			// The body starts after the frontmatter's closing line.
			p.markdown = strings.TrimLeft(body, "\n")
		} else {
			p.State = "shipped"
		}
		parts = append(parts, p)
	}
	return parts, nil
}

// from says where a part of the Playbook comes from, as a person reads it.
func from(o engine.Origin) string {
	switch {
	case o.Builtin:
		return "built into jfl"
	case o.Project && o.Base != "":
		return "project, overriding " + baseName(o.Base)
	case o.Project:
		return "project"
	}
	return baseName(o.Base)
}

// baseName names the Base Playbook base as a person reads it.
func baseName(base string) string {
	if name, ok := strings.CutPrefix(base, "builtin:"); ok {
		return "builtin " + name
	}
	return "Base Playbook " + base
}

// playbookFilePage is a Skill, Guideline or Persona on a page of its own,
// with its file rendered as Markdown.
type playbookFilePage struct {
	chrome
	Kind, Name, Description string
	Facts                   []fact
	Contents                template.HTML
}

// fact is one thing the page of a part of the Playbook says about it; with
// a link, when it names something with a page.
type fact struct {
	Name, Value, Link string
}

// playbookFileView builds the page of the part name of the Playbook, of
// the kind the path names: skills, guidelines or personas.
func (e *env) playbookFileView(kind, name string) func() (any, error) {
	return func() (any, error) {
		pb, _, err := e.load()
		if err != nil {
			return nil, err
		}
		v := playbookFilePage{chrome: chrome{Playbook: pb.Name, Page: "playbook", Path: "/playbook/" + kind + "/" + url.PathEscape(name)}, Name: name}
		var markdown string
		switch kind {
		case "skills":
			s, ok := pb.Skills[name]
			if !ok {
				return nil, fmt.Errorf("there is no Skill %s in the Playbook: %w", name, store.ErrNotFound)
			}
			o := pb.Origins.Skills[name]
			changes := "no"
			if s.Changes {
				changes = "yes"
			}
			var bound []string
			for _, t := range pb.Types {
				for _, st := range t.Statuses {
					if t.Bindings[st] == name {
						bound = append(bound, t.Name+" "+st)
					}
				}
			}
			v.Kind, v.Description, markdown = "Skill", s.Description, s.Prompt
			v.Facts = []fact{
				{Name: "Invocation Mode", Value: s.Invocation},
				{Name: "Changes code or Artifacts", Value: changes},
				{Name: "Bound to", Value: cmp.Or(strings.Join(bound, ", "), "no Binding")},
			}
			for _, g := range s.Guidelines {
				v.Facts = append(v.Facts, fact{Name: "Guideline", Value: g, Link: "/playbook/guidelines/" + url.PathEscape(g)})
			}
			for _, p := range s.Personas {
				v.Facts = append(v.Facts, fact{Name: "Persona", Value: p.Name, Link: "/playbook/personas/" + url.PathEscape(p.Name)})
			}
			v.Facts = append(v.Facts, fact{Name: "Comes from", Value: from(o)}, fact{Name: "File", Value: o.File})
		case "guidelines":
			g, ok := pb.Guidelines[name]
			if !ok {
				return nil, fmt.Errorf("there is no Guideline %s in the Playbook: %w", name, store.ErrNotFound)
			}
			o := pb.Origins.Guidelines[name]
			v.Kind, markdown = "Guideline", g
			v.Facts = []fact{{Name: "Comes from", Value: from(o)}, {Name: "File", Value: o.File}}
		case "personas":
			parts, err := e.personaParts(pb)
			if err != nil {
				return nil, err
			}
			i := slices.IndexFunc(parts, func(p personaPart) bool { return p.Name == name })
			if i < 0 {
				return nil, fmt.Errorf("there is no Persona %s in the project: %w", name, store.ErrNotFound)
			}
			p := parts[i]
			v.Kind, markdown = "Persona", p.markdown
			v.Facts = []fact{{Name: "State", Value: p.State}}
			if p.Artifact != "" {
				v.Facts = append(v.Facts, fact{Name: "Comes from", Value: "Persona Artifact " + p.Artifact, Link: "/artifacts/" + url.PathEscape(p.Artifact)})
			} else {
				v.Facts = append(v.Facts, fact{Name: "Comes from", Value: p.From}, fact{Name: "File", Value: p.file})
			}
			if p.Overrides != "" {
				v.Facts = append(v.Facts, fact{Name: "Overrides", Value: p.Overrides})
			}
			if p.Proposed != "" {
				v.Facts = append(v.Facts, fact{Name: "Proposed to replace it", Value: p.Proposed, Link: "/artifacts/" + url.PathEscape(p.Proposed)})
			}
		default:
			return nil, fmt.Errorf("the Playbook has no %s: %w", kind, store.ErrNotFound)
		}
		if v.Contents, err = renderMarkdown(markdown); err != nil {
			return nil, err
		}
		return v, nil
	}
}
