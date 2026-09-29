package cli

import (
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	"embed"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io"
	"maps"
	"net"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jigflow-ai/jigflow/internal/adapter"
	"github.com/jigflow-ai/jigflow/internal/engine"
	"github.com/jigflow-ai/jigflow/internal/store"
)

// defaultUIAddr is where jfl ui serves unless told otherwise.
const defaultUIAddr = "127.0.0.1:7457"

//go:embed ui
var uiFiles embed.FS

// uiPages are the Dashboard's page templates, each rendered inside the
// layout.
var uiPages = func() map[string]*template.Template {
	pages := map[string]*template.Template{}
	// since is the version of the project a page shows, which writePage
	// gives each page it writes.
	funcs := template.FuncMap{"duration": duration, "tokens": tokens, "since": func() string { return "" }}
	for _, name := range []string{"backlog", "playbook", "workflows", "ledger", "artifact", "playbookfile", "problem"} {
		pages[name] = template.Must(template.New("layout.html").Funcs(funcs).ParseFS(uiFiles, "ui/layout.html", "ui/parts.html", "ui/"+name+".html"))
	}
	return pages
}()

// cmdUI serves the Dashboard on a loopback address until interrupted.
func cmdUI(e *env, args []string) error {
	fs := flag.NewFlagSet("ui", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	addr := fs.String("addr", defaultUIAddr, "the loopback address and port to serve on")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("%w: %v", errUsage, err)
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("%w: jfl ui [--addr <host:port>]", errUsage)
	}
	// The Dashboard is a person's view of the project, and the channel an
	// agent in a terminal can't reach (ADR 0003): it serves only this
	// machine (ADR 0016).
	if host, _, err := net.SplitHostPort(*addr); err != nil || !loopback(host) {
		return fmt.Errorf("the Dashboard serves only on this machine: --addr must be a loopback address, such as %s, not %q", defaultUIAddr, *addr)
	}
	// No Dashboard on a Playbook that doesn't load, as no command runs on one.
	if _, _, err := e.load(); err != nil {
		return err
	}
	every, err := trackerInterval(e.getenv)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	d := &dashboard{e: e, changes: newChanges(projectFiles(e.dir), trackers(e.dir, every)), closing: make(chan struct{})}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	d.cookie = "jfl-dashboard-" + port
	if !e.actor.Agent() {
		// Only the person who started jfl ui sees this terminal, and so the
		// link that lets their browser act: an agent in another terminal
		// can read the Dashboard, but not approve (ADR 0003).
		d.key = rand.Text()
	}
	// No WriteTimeout: a page's change stream lasts as long as the page.
	srv := &http.Server{Handler: d.handler(), ReadHeaderTimeout: 10 * time.Second}
	// Shutting down waits for requests in flight, which change streams
	// never finish of their own accord: they end on it.
	srv.RegisterOnShutdown(func() { close(d.closing) })
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	shutDown := make(chan struct{})
	go func() {
		defer close(shutDown)
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shut)
	}()
	if d.key != "" {
		fmt.Fprintf(e.stdout, "Dashboard at http://%s/?key=%s\n", ln.Addr(), d.key)
		fmt.Fprintln(e.stdout, "Open this link in your browser to approve Proposals, make Human Transitions and comment there; it is yours alone, until jfl ui stops.")
	} else {
		fmt.Fprintf(e.stdout, "Dashboard at http://%s/ (to look only: agent session %s started it)\n", ln.Addr(), e.actor.Session)
	}
	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	// A decision in flight finishes before jfl ui exits.
	<-shutDown
	return nil
}

// dashboard is the Dashboard's HTTP interface. Every page reads the
// Playbook and the Stores afresh, so it shows what jfl last wrote.
type dashboard struct {
	e *env
	// key is the secret in the link jfl ui printed in the terminal of the
	// person who started it; empty when an agent session started it, and
	// nobody may act through it.
	key string
	// cookie names the cookie in which the person's browser keeps the key
	// once it opened the link; one per port, so the Dashboards of two
	// projects don't share it.
	cookie string
	// mu lets one decision at a time change the project.
	mu sync.Mutex
	// changes tells the pages listening that the project changed.
	changes *changes
	// closing is closed when jfl ui stops, ending the change streams.
	closing chan struct{}
}

func (d *dashboard) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("key") {
			d.open(w, r)
			return
		}
		d.render(w, http.StatusOK, "backlog", "", d.backlogView(r, nil))
	})
	mux.HandleFunc("GET /playbook", func(w http.ResponseWriter, r *http.Request) {
		d.render(w, http.StatusOK, "playbook", "", d.e.playbookView)
	})
	mux.HandleFunc("GET /playbook/{kind}/{name}", func(w http.ResponseWriter, r *http.Request) {
		d.render(w, http.StatusOK, "playbookfile", r.URL.Path, d.e.playbookFileView(r.PathValue("kind"), r.PathValue("name")))
	})
	mux.HandleFunc("GET /workflows", func(w http.ResponseWriter, r *http.Request) {
		d.render(w, http.StatusOK, "workflows", "", d.e.workflowsView)
	})
	mux.HandleFunc("GET /ledger", func(w http.ResponseWriter, r *http.Request) {
		d.render(w, http.StatusOK, "ledger", "", d.e.ledgerView)
	})
	mux.HandleFunc("GET /artifacts/{id}", func(w http.ResponseWriter, r *http.Request) {
		d.render(w, http.StatusOK, "artifact", r.URL.Path, d.artifactView(r, r.PathValue("id"), nil))
	})
	mux.HandleFunc("GET /changes", d.stream)
	mux.HandleFunc("POST /proposals/{id}/approve", d.act(func(c *env, r *http.Request) error {
		return c.approve(r.PathValue("id"), func(pb *engine.Playbook, p *engine.Proposal) error {
			return editItems(pb, p, r.PostForm)
		})
	}, d.showBacklog))
	mux.HandleFunc("POST /artifacts/{id}/move", d.act(func(c *env, r *http.Request) error {
		return c.move(r.PathValue("id"), r.PostForm.Get("to"))
	}, d.showBacklog))
	mux.HandleFunc("POST /artifacts/{id}/comment", d.act(comment, d.showArtifact))
	mux.HandleFunc("POST /proposals/{id}/reject", d.act(func(c *env, r *http.Request) error {
		return c.reject(r.PathValue("id"))
	}, d.showBacklog))
	// A page elsewhere may post a form here through the person's browser,
	// which carries the key: browsers say where a request comes from, and
	// only the Dashboard's own pages may act.
	cop := http.NewCrossOriginProtection()
	cop.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d.refuse(w, http.StatusForbidden, errors.New("only the Dashboard's own pages may act on it, not a page elsewhere"))
	}))
	protected := cop.Handler(mux)
	// A web page elsewhere can point a name of its own at 127.0.0.1 and
	// reach the Dashboard through the browser under that name: only
	// requests addressed to this machine by a loopback name are answered.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		if !loopback(host) {
			http.Error(w, "the Dashboard answers only requests addressed to this machine, such as 127.0.0.1 or localhost", http.StatusForbidden)
			return
		}
		protected.ServeHTTP(w, r)
	})
}

// humanMoves returns the Statuses the Human Transitions of the Artifact
// Type t lead to from the Status of a.
func humanMoves(t *engine.ArtifactType, a engine.Artifact) []string {
	var to []string
	for _, tr := range t.Transitions {
		if tr.Human && tr.From == a.Status {
			to = append(to, tr.To)
		}
	}
	return to
}

// byID indexes the Artifacts by their id.
func byID(all []engine.Artifact) map[string]engine.Artifact {
	m := make(map[string]engine.Artifact, len(all))
	for _, a := range all {
		m[a.ID] = a
	}
	return m
}

// loopback reports whether host names this machine: localhost or a
// loopback IP address.
func loopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

// render writes the page built by view inside the layout, with the status
// given, or why view failed. path is the page's own path, when it isn't the
// one its name gives, which the page loads again when the project changes.
func (d *dashboard) render(w http.ResponseWriter, status int, page, path string, view func() (any, error)) {
	// Before view reads the project, so that a change written meanwhile
	// is signalled to the page.
	since := d.changes.version()
	data, err := view()
	if err != nil {
		// A Connector failing is a problem with the tracker, which the
		// person must tell apart from the Playbook or the state being wrong.
		status = http.StatusInternalServerError
		if errors.Is(err, store.ErrNotFound) {
			status = http.StatusNotFound
		}
		if _, ok := errors.AsType[*store.ConnectorError](err); ok {
			err = fmt.Errorf("tracker problem, not a workflow refusal: %w", err)
			status = http.StatusBadGateway
		}
		page, data = "problem", problem{chrome: chrome{Page: page, Path: path}, Problem: err.Error()}
	}
	writePage(w, status, page, data, since)
}

// refuse writes the page that says why the request can't be served.
func (d *dashboard) refuse(w http.ResponseWriter, status int, why error) {
	writePage(w, status, "problem", problem{Problem: why.Error()}, d.changes.version())
}

// writePage writes the page, rendered with data, with the status given,
// saying it shows the project as of the version since.
func writePage(w http.ResponseWriter, status int, page string, data any, since string) {
	var buf bytes.Buffer
	t, err := uiPages[page].Clone()
	if err == nil {
		err = t.Funcs(template.FuncMap{"since": func() string { return since }}).Execute(&buf, data)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

// problem is a page that couldn't be shown, and why.
type problem struct {
	chrome
	Problem string
}

// backlog is the Dashboard's main page: the Artifacts of each Type.
type backlog struct {
	chrome
	Outcome   *outcome      // what the person's last decision did, if they just made one
	CanAct    bool          // whether the person viewing it may decide here
	Cannot    string        // why not, when they may not
	Queue     []artifactRow // human work: what only a person moves on
	Proposals []pendingProposal
	Types     []typeArtifacts
	// FormHooks warn about the agent's hooks that can answer the
	// Confirmation form in the person's place, as jfl check does.
	FormHooks []string
}

// pendingProposal is a Proposal waiting for a person's decision.
type pendingProposal struct {
	ID, By, Summary string
	Approve         string // what approving it does, e.g. "Approve all 4 changes"
	Items           []proposalItem
}

// proposalItem is an item of a pending Proposal, and, for a creation, the
// fields a person may edit before approving.
type proposalItem struct {
	N    int // its number in the Proposal, from 1
	Text string
	// Artifacts are the ids of the Artifacts it names, each with a page.
	Artifacts []string
	Create    bool
	Type      string // the Artifact Type of a creation
	Title     string
	Statuses  []option    // the Statuses it may be created in
	Links     []linkField // the Links its Type declares
}

// option is one choice of a select.
type option struct {
	Value    string
	Selected bool
}

// linkField is a Link of a proposed creation, as a person edits it: the ids
// or refs it points to, separated by commas.
type linkField struct {
	Name, IDs string
}

// chrome is what every page shows around its content: the Playbook's name
// and which page it is.
type chrome struct {
	Playbook string
	Page     string
	// Path is the page's own path, for a page that isn't one of the
	// navigation's, such as an Artifact's.
	Path string
}

// Here is the path of the page, which it loads again when the project
// changes: its own Path, when it has one, and otherwise the page its name
// gives; after a decision, or a refusal, the backlog.
func (c chrome) Here() string {
	if c.Path != "" {
		return c.Path
	}
	switch c.Page {
	case "playbook", "workflows", "ledger":
		return "/" + c.Page
	}
	return "/"
}

type typeArtifacts struct {
	Name      string
	Artifacts []artifactRow
}

// artifactRow is an Artifact as the backlog shows it.
type artifactRow struct {
	engine.Artifact
	Work  engine.Work
	Links []link
	Note  string   // why next doesn't hand out agent work, if it doesn't
	Moves []string // the Statuses its Human Transitions lead to
}

// link is one named Link of an Artifact and the Artifacts it points to.
type link struct {
	Name string
	IDs  []string
}

// backlogView builds the backlog as the person making the request sees it,
// with the outcome of the decision they just made, if any.
func (d *dashboard) backlogView(r *http.Request, done *outcome) func() (any, error) {
	return func() (any, error) {
		v, err := d.e.backlog()
		if err != nil && done != nil {
			// The decision was made, or refused, all the same.
			said := strings.TrimSpace(done.Problem + "\n" + done.Said)
			return nil, fmt.Errorf("%s\n\nThe backlog can't be shown now: %w", said, err)
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

func (e *env) backlog() (backlog, error) {
	pb, st, err := e.load()
	if err != nil {
		return backlog{}, err
	}
	all, err := st.List()
	if err != nil {
		return backlog{}, err
	}
	proposals, err := store.NewProposals(e.dir).List()
	if err != nil {
		return backlog{}, err
	}
	// Why next skips agent work, as a person sees it: a Claim is shown as
	// such, so only the other reasons are notes.
	notes := map[string]string{}
	for _, s := range engine.Next(pb, engine.Actor{}, all, proposals).Skipped {
		if s.Artifact.Claim == "" {
			notes[s.Artifact.ID] = s.Reason
		}
	}
	v := backlog{chrome: chrome{Playbook: pb.Name, Page: "backlog"}, FormHooks: adapter.FormHookWarnings(e.dir, e.getenv)}
	for _, t := range pb.Types {
		ta := typeArtifacts{Name: t.Name}
		for _, a := range engine.InOrder(pb, all) {
			if a.Type != t.Name {
				continue
			}
			r := artifactRow{Artifact: a, Work: pb.WorkOf(a), Moves: humanMoves(t, a)}
			for _, name := range slices.Sorted(maps.Keys(a.Links)) {
				r.Links = append(r.Links, link{name, a.Links[name]})
			}
			if r.Work == engine.ForAgent {
				r.Note = notes[a.ID]
			}
			ta.Artifacts = append(ta.Artifacts, r)
			if r.Work == engine.ForHuman {
				v.Queue = append(v.Queue, r)
			}
		}
		v.Types = append(v.Types, ta)
	}
	kept := byID(all)
	for _, p := range proposals {
		if p.Status != engine.Pending {
			continue
		}
		pp := pendingProposal{ID: p.ID, By: p.ProposedBy(), Summary: p.Summary}
		pp.Approve = "Approve"
		if len(p.Items) > 1 {
			pp.Approve = "Approve all " + plural(len(p.Items), "change")
		}
		for i, it := range p.Items {
			pp.Items = append(pp.Items, itemView(pb, i, it, kept))
		}
		v.Proposals = append(v.Proposals, pp)
	}
	// P-2 before P-10.
	slices.SortFunc(v.Proposals, func(x, y pendingProposal) int {
		return cmp.Or(cmp.Compare(len(x.ID), len(y.ID)), cmp.Compare(x.ID, y.ID))
	})
	return v, nil
}

// ledgerPage is the Ledger summed, as jfl ledger sums it.
type ledgerPage struct {
	chrome
	engine.LedgerSummary
	Empty bool
	// AsOf is when the times are summed to: the page updates only when
	// the project changes, not as time passes (ADR 0025).
	AsOf time.Time
}

func (e *env) ledgerView() (any, error) {
	pb, _, err := e.load()
	if err != nil {
		return nil, err
	}
	// Pages are served concurrently, so each reads the Ledger through its
	// own handle rather than the command's.
	l, err := store.NewLedger(e.dir).Read()
	if err != nil {
		return nil, err
	}
	now := e.now()
	sum := engine.Summarise(pb, l, now)
	return ledgerPage{
		AsOf:          now,
		chrome:        chrome{Playbook: pb.Name, Page: "ledger"},
		LedgerSummary: sum,
		Empty:         len(sum.Artifacts) == 0 && sum.Unattributed == 0 && !sum.Usage,
	}, nil
}

// workflows is the shape of each Artifact Type's workflow: its Status
// machine drawn, and what happens in each Status and on each Transition.
type workflows struct {
	chrome
	Types []workflow
}

type workflow struct {
	Name        string
	Store       string
	Diagram     diagram
	Statuses    []described
	Transitions []described
}

// described is a Status or a Transition, and what happens there.
type described struct {
	Name  string
	Marks []string
}

func (e *env) workflowsView() (any, error) {
	pb, _, err := e.load()
	if err != nil {
		return nil, err
	}
	v := workflows{chrome: chrome{Playbook: pb.Name, Page: "workflows"}}
	for _, t := range pb.Types {
		w := workflow{Name: t.Name, Store: t.Store, Diagram: layout(t)}
		for _, s := range t.Statuses {
			marks := atStatus(t, s)
			if slices.Contains(t.Initial, s) {
				marks = append([]string{"initial"}, marks...)
			}
			if slices.Contains(t.Inbox, s) {
				marks = append(marks, "Inbox")
			}
			w.Statuses = append(w.Statuses, described{s, marks})
		}
		for _, tr := range t.Transitions {
			w.Transitions = append(w.Transitions, described{tr.From + " → " + tr.To, onTransition(tr)[1:]})
		}
		v.Types = append(v.Types, w)
	}
	return v, nil
}
