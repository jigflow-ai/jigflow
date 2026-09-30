package cli

import (
	"bytes"
	"cmp"
	"crypto/subtle"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/jigflow-ai/jigflow/internal/engine"
	"github.com/jigflow-ai/jigflow/internal/playbook"
	"github.com/jigflow-ai/jigflow/internal/store"
)

// The Dashboard is where a person approves or rejects Proposals and makes
// Human Transitions, out of band: an agent in a terminal can't click in the
// person's browser (ADR 0003). Each decision runs the command a person
// would run in a terminal, with the click as its confirmation.

// outcome is what a decision made in the Dashboard did, as jfl would say it
// in a terminal.
type outcome struct {
	Refused bool
	Said    string // what the command reported
	Problem string // why it was refused or failed, if it was
}

// open is the person opening the link jfl ui printed: their browser keeps
// its key, and may act from then on.
func (d *dashboard) open(w http.ResponseWriter, r *http.Request) {
	if d.key != "" && subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("key")), []byte(d.key)) == 1 {
		http.SetCookie(w, &http.Cookie{Name: d.cookie, Value: d.key, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
	}
	// Out of the address bar, and the browser's history.
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// mayAct says why the request may not make a decision, or nil when it may:
// only the browser that opened the link jfl ui printed may.
func (d *dashboard) mayAct(r *http.Request) error {
	if d.key == "" {
		return fmt.Errorf("agent session %s started this Dashboard, so it is only to look at: approve Proposals, make Human Transitions, comment and change the Playbook file in a Dashboard you start yourself with jfl ui", d.e.actor.Session)
	}
	c, err := r.Cookie(d.cookie)
	if err != nil || subtle.ConstantTimeCompare([]byte(c.Value), []byte(d.key)) != 1 {
		return errors.New("to approve Proposals, make Human Transitions, comment and change the Playbook file here, open the link jfl ui printed in the terminal where you started it")
	}
	return nil
}

// act handles a decision: when the request may make it, it runs do as the
// person, one decision at a time, and shows with then what it did.
func (d *dashboard) act(do func(c *env, r *http.Request) error, then func(w http.ResponseWriter, r *http.Request, status int, done *outcome)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := d.mayAct(r); err != nil {
			d.refuse(w, http.StatusForbidden, err)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		if err := r.ParseForm(); err != nil {
			d.refuse(w, http.StatusBadRequest, err)
			return
		}
		d.mu.Lock()
		defer d.mu.Unlock()
		var out bytes.Buffer
		err := do(d.e.clickedBy(&out), r)
		o := &outcome{Said: out.String()}
		status := http.StatusOK
		if err != nil {
			o.Refused, o.Problem, status = true, err.Error(), http.StatusConflict
			if _, ok := errors.AsType[*store.ConnectorError](err); ok {
				o.Problem = "tracker problem, not a workflow refusal: " + o.Problem
				status = http.StatusBadGateway
			}
		}
		then(w, r, status, o)
	}
}

// showBacklog shows the backlog after a decision, with what it did.
func (d *dashboard) showBacklog(w http.ResponseWriter, r *http.Request, status int, done *outcome) {
	d.render(w, status, "backlog", "", d.backlogView(r, done))
}

// showArtifact shows the page of the Artifact the decision was made on,
// with what it did.
func (d *dashboard) showArtifact(w http.ResponseWriter, r *http.Request, status int, done *outcome) {
	d.render(w, status, "artifact", "/artifacts/"+url.PathEscape(r.PathValue("id")), d.artifactView(r, r.PathValue("id"), done))
}

// comment runs jfl comment as the person, with the text they posted: the
// one way the Dashboard adds to an Artifact's body, or to its comments in
// a tracker.
func comment(c *env, r *http.Request) error {
	// A browser sends a textarea's lines ending in CRLF.
	text := strings.ReplaceAll(r.PostForm.Get("text"), "\r\n", "\n")
	if strings.TrimSpace(text) == "" {
		return errors.New("a comment needs some text: nothing was added")
	}
	return cmdComment(c, []string{r.PathValue("id"), text})
}

// showPlaybook shows the Playbook page after a change to the Playbook
// file, with what it did.
func (d *dashboard) showPlaybook(w http.ResponseWriter, r *http.Request, status int, done *outcome) {
	d.render(w, status, "playbook", "/playbook", d.playbookView(r, done))
}

// giveGateCommand gives the Gates of the name the path names the command
// the person posted, in the project's Playbook file.
func giveGateCommand(c *env, r *http.Request) error {
	return c.changePlaybook(engine.ProposalItem{Gate: r.PathValue("name"), Cmd: strings.TrimSpace(r.PostForm.Get("cmd"))})
}

// gateBackToBase removes from the project's Playbook file the command it
// gives the Gates of the name the path names, which then run the Base
// Playbook's.
func gateBackToBase(c *env, r *http.Request) error {
	return c.changePlaybook(engine.ProposalItem{Gate: r.PathValue("name"), Remove: true})
}

// publishFor runs jfl publish as the person, for the Adapter the path
// names. It is no Proposal: it changes no workflow state, and an agent may
// run it too (ADR 0030).
func publishFor(c *env, r *http.Request) error {
	return cmdPublish(c, []string{r.PathValue("name")})
}

// stopPublishingFor runs jfl publish --remove as the person, for the
// Adapter the path names.
func stopPublishingFor(c *env, r *http.Request) error {
	return cmdPublish(c, []string{"--remove", r.PathValue("name")})
}

// moveBaseRef moves the git Base Playbook to the ref the person posted, in
// the project's Playbook file, pinning the commit it points to.
func moveBaseRef(c *env, r *http.Request) error {
	ref := strings.TrimSpace(r.PostForm.Get("ref"))
	if ref == "" {
		return errors.New("not proposed: the git Base Playbook needs a ref: a tag, branch or commit")
	}
	return c.changePlaybook(engine.ProposalItem{BaseRef: ref})
}

// setMockupFolder makes the folder the person posted the Mockup folder, in
// the project's Playbook file.
func setMockupFolder(c *env, r *http.Request) error {
	folder := strings.TrimSpace(r.PostForm.Get("folder"))
	if folder == "" {
		return errors.New("not proposed: the Mockup folder needs a folder inside the project, relative to its root, such as .jigflow/mockups")
	}
	return c.changePlaybook(engine.ProposalItem{Mockups: folder})
}

// mockupsBackToBase removes from the project's Playbook file the Mockup
// folder it gives, so that the Base Playbook's is the folder again.
func mockupsBackToBase(c *env, r *http.Request) error {
	pb, _, err := c.load()
	if err != nil {
		return err
	}
	return c.changePlaybook(engine.ProposalItem{Mockups: pb.Mockups, Remove: true})
}

// changeConnector gives the Connector the path names the values the person
// posted that differ from those it has, in the project's Playbook file,
// which first copies the Base Playbook's declaration of it, if it has none
// (ADR 0030).
func changeConnector(c *env, r *http.Request) error {
	pb, _, err := c.load()
	if err != nil {
		return err
	}
	name := r.PathValue("name")
	now := pb.Connectors[name]
	if now == nil {
		return fmt.Errorf("not proposed: the Playbook declares no Connector %q", name)
	}
	form := r.PostForm
	it := engine.ProposalItem{Connector: name}
	changed := false
	command := strings.TrimSpace(form.Get("command"))
	if command == "" {
		return fmt.Errorf("not proposed: Connector %q needs a command, the executable it runs", name)
	}
	if command != now.Command {
		it.Command, changed = command, true
	}
	// A browser sends a textarea's lines ending in CRLF.
	args := engine.List{}
	for line := range strings.Lines(strings.ReplaceAll(form.Get("args"), "\r\n", "\n")) {
		if arg := strings.TrimSpace(line); arg != "" {
			args = append(args, arg)
		}
	}
	if !slices.Equal(args, now.Args) {
		it.Args, changed = args, true
	}
	marker := cmp.Or(strings.TrimSpace(form.Get("marker")), playbook.DefaultMarker)
	if marker != now.Marker {
		it.Marker, changed = marker, true
	}
	described := store.Describe(c.dir, now)
	if it.Settings, err = postedSettings(form, "", now.Settings, described, false); err != nil {
		return err
	}
	for _, t := range form["type"] {
		prefix := "type." + t + "."
		settings, err := postedSettings(form, prefix, now.Types[t].Settings, described, true)
		if err != nil {
			return err
		}
		ct := engine.ConnectorType{Settings: settings}
		// Each Status and field value whose label or state the person
		// changed is remapped, which relabels the Artifacts carrying the
		// one it replaces.
		if typ := pb.Type(t); typ != nil && typ.Store == name {
			for _, f := range mappingFields(prefix, typ, now.Types[t]) {
				v, ok := f.posted(form)
				switch {
				case !ok:
				case f.status != "":
					if ct.Statuses == nil {
						ct.Statuses = map[string]any{}
					}
					ct.Statuses[f.status] = v
				default:
					if ct.Fields == nil {
						ct.Fields = map[string]map[string]any{}
					}
					if ct.Fields[f.field] == nil {
						ct.Fields[f.field] = map[string]any{}
					}
					ct.Fields[f.field][f.value] = v
				}
			}
		}
		if ct.Settings != nil || ct.Statuses != nil || ct.Fields != nil {
			if it.ConnectorTypes == nil {
				it.ConnectorTypes = map[string]engine.ConnectorType{}
			}
			it.ConnectorTypes[t] = ct
		}
	}
	if !changed && it.Settings == nil && it.ConnectorTypes == nil {
		return fmt.Errorf("not proposed: nothing to change in Connector %q", name)
	}
	if err := needSettings(pb, now, it, described); err != nil {
		return err
	}
	return c.changePlaybook(it)
}

// needSettings refuses the change it to the Connector now of pb when it
// leaves a setting the Connector describes as required empty: for the
// Connector, or for an Artifact Type it keeps, which may be given it
// either per Type or by the Connector's settings (ADR 0031).
func needSettings(pb *engine.Playbook, now *engine.Connector, it engine.ProposalItem, described []store.Setting) error {
	after := func(settings, changes map[string]any) map[string]any {
		out := maps.Clone(settings)
		if out == nil {
			out = map[string]any{}
		}
		for k, v := range changes {
			if v == nil {
				delete(out, k)
			} else {
				out[k] = v
			}
		}
		return out
	}
	empty := func(v any) bool { return v == nil || v == "" }
	settings := after(now.Settings, it.Settings)
	for _, d := range described {
		if d.Required && !d.PerType && empty(settings[d.Name]) {
			return fmt.Errorf("not proposed: Connector %q needs setting %s: %s", now.Name, d.Name, d.Help)
		}
	}
	for _, t := range pb.Types {
		if t.Store != now.Name {
			continue
		}
		merged := after(settings, after(now.Types[t.Name].Settings, it.ConnectorTypes[t.Name].Settings))
		for _, d := range described {
			if d.Required && d.PerType && empty(merged[d.Name]) {
				return fmt.Errorf("not proposed: Connector %q needs %s setting %s: %s", now.Name, t.Name, d.Name, d.Help)
			}
		}
	}
	return nil
}

// postedSettings returns the settings, whose fields' names start with
// prefix, that the person changed from those the Connector has, now, or
// describes as described, those given per Artifact Type when perType is,
// each with its new value, or nil to remove it, and the one they added, if
// any; nil if they changed none.
func postedSettings(form url.Values, prefix string, now map[string]any, described []store.Setting, perType bool) (map[string]any, error) {
	var changed map[string]any
	set := func(name string, v any) {
		if changed == nil {
			changed = map[string]any{}
		}
		changed[name] = v
	}
	for _, f := range settingFields(prefix, now, described, perType) {
		if v, ok := f.posted(form); ok {
			set(f.Name, v)
		}
	}
	name, value := strings.TrimSpace(form.Get(prefix+"new-setting")), strings.TrimSpace(form.Get(prefix+"new-value"))
	switch {
	case name == "" && value != "":
		return nil, fmt.Errorf("not proposed: the new setting %s needs a name", value)
	case name == "":
	case value == "":
		return nil, fmt.Errorf("not proposed: the new setting %s needs a value", name)
	case value == "true" || value == "false":
		set(name, value == "true")
	default:
		set(name, value)
	}
	return changed, nil
}

// connectorBackToBase removes from the project's Playbook file its
// declaration of the Connector the path names, so that the Base Playbook's
// is the Connector again.
func connectorBackToBase(c *env, r *http.Request) error {
	return c.changePlaybook(engine.ProposalItem{Connector: r.PathValue("name"), Remove: true})
}

// changePlaybook makes it, a change to the Playbook file, a Proposal of
// the person's, as jfl propose would, and approves it at once, as jfl
// approve would with the click as its Confirmation (ADR 0030): the change
// goes through the checks, the all-or-nothing writing and the Ledger entry
// of any Proposal. One that can't be applied is rejected, so that nothing
// the person meant to confirm at once waits for them afterwards.
func (e *env) changePlaybook(it engine.ProposalItem) error {
	p, _, _, err := e.propose(it.String(), []engine.ProposalItem{it})
	if err != nil {
		return err
	}
	if err := e.approve(p.ID, nil); err != nil {
		if rejected := e.reject(p.ID); rejected != nil {
			return errors.Join(err, rejected)
		}
		return err
	}
	return nil
}

// clickedBy is the env of a command a person runs by clicking in the
// Dashboard: theirs, confirmed by the click, reporting to out. Commands are
// served concurrently, so it has a Ledger handle of its own.
func (e *env) clickedBy(out *bytes.Buffer) *env {
	c := *e
	c.actor = engine.Actor{}
	c.stdin = nil
	c.stdout, c.stderr = out, out
	c.led = nil
	c.clicked = true
	return &c
}

// itemView is the i-th item of a pending Proposal as the Dashboard shows it;
// kept is every Artifact the Stores keep, by id.
func itemView(pb *engine.Playbook, i int, it engine.ProposalItem, kept map[string]engine.Artifact) proposalItem {
	v := proposalItem{N: i + 1, Text: it.String()}
	v.Now, v.Replaces = pb.Now(it)
	// The Artifacts it names, as against refs to creations of the Proposal.
	for _, id := range it.Names() {
		if _, ok := kept[id]; ok {
			v.Artifacts = append(v.Artifacts, id)
		}
	}
	t := pb.Type(it.Create)
	if t == nil {
		return v
	}
	v.Create, v.Type, v.Title = true, t.Name, it.Title
	status := startStatus(it.Status, t.Initial)
	for _, s := range t.Initial {
		v.Statuses = append(v.Statuses, option{s, s == status})
	}
	for _, name := range slices.Sorted(maps.Keys(t.Links)) {
		v.Links = append(v.Links, linkField{name, strings.Join(it.Links[name], ", ")})
	}
	return v
}

// startStatus returns status, or the first of the Type's initial Statuses a
// creation that names none starts in.
func startStatus(status string, initial []string) string {
	if status == "" && len(initial) > 0 {
		return initial[0]
	}
	return status
}

// editItems applies a person's edits, posted from the Dashboard, to the
// creations of p: their title, the Status they start in, and their Links.
// A field the form doesn't carry is left as it is.
func editItems(pb *engine.Playbook, p *engine.Proposal, form url.Values) error {
	for i := range p.Items {
		it := &p.Items[i]
		if it.Create == "" {
			continue
		}
		field := func(name string) (string, bool) {
			key := "item-" + strconv.Itoa(i+1) + "-" + name
			return strings.TrimSpace(form.Get(key)), form.Has(key)
		}
		if title, ok := field("title"); ok {
			if title == "" {
				return fmt.Errorf("item %d (%s): a %s needs a title", i+1, it, it.Create)
			}
			it.Title = title
		}
		t := pb.Type(it.Create)
		if t == nil {
			continue
		}
		if status, ok := field("status"); ok && status != startStatus(it.Status, t.Initial) {
			it.Status = status
		}
		links := maps.Clone(it.Links)
		for _, name := range slices.Sorted(maps.Keys(t.Links)) {
			ids, ok := field("link-" + name)
			if !ok {
				continue
			}
			split := strings.FieldsFunc(ids, func(r rune) bool { return r == ',' || r == ' ' })
			if links == nil {
				links = map[string][]string{}
			}
			if len(split) == 0 {
				delete(links, name)
			} else {
				links[name] = split
			}
		}
		if len(links) == 0 {
			links = nil
		}
		it.Links = links
	}
	return nil
}
