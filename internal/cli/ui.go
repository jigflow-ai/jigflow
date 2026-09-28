package cli

import (
	"bytes"
	"cmp"
	"context"
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
	"syscall"
	"time"

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
	funcs := template.FuncMap{"duration": duration, "tokens": tokens}
	for _, name := range []string{"backlog", "workflows", "ledger", "problem"} {
		pages[name] = template.Must(template.New("layout.html").Funcs(funcs).ParseFS(uiFiles, "ui/layout.html", "ui/"+name+".html"))
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
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: e.dashboard(), ReadHeaderTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shut)
	}()
	fmt.Fprintf(e.stdout, "Dashboard at http://%s/\n", ln.Addr())
	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// dashboard is the Dashboard's HTTP interface. Every page reads the
// Playbook and the Stores afresh, so it shows what jfl last wrote.
func (e *env) dashboard() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		e.render(w, "backlog", e.backlogView)
	})
	mux.HandleFunc("GET /workflows", func(w http.ResponseWriter, r *http.Request) {
		e.render(w, "workflows", e.workflowsView)
	})
	mux.HandleFunc("GET /ledger", func(w http.ResponseWriter, r *http.Request) {
		e.render(w, "ledger", e.ledgerView)
	})
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
		mux.ServeHTTP(w, r)
	})
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

// render writes the page built by view inside the layout.
func (e *env) render(w http.ResponseWriter, page string, view func() (any, error)) {
	status := http.StatusOK
	data, err := view()
	if err != nil {
		// A Connector failing is a problem with the tracker, which the
		// person must tell apart from the Playbook or the state being wrong.
		p := problem{chrome: chrome{Page: page}, Problem: err.Error()}
		status = http.StatusInternalServerError
		if _, ok := errors.AsType[*store.ConnectorError](err); ok {
			p.Problem = "tracker problem, not a workflow refusal: " + p.Problem
			status = http.StatusBadGateway
		}
		page, data = "problem", p
	}
	var buf bytes.Buffer
	if err := uiPages[page].Execute(&buf, data); err != nil {
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
	Queue     []artifactRow // human work: what only a person moves on
	Proposals []pendingProposal
	Types     []typeArtifacts
}

// pendingProposal is a Proposal waiting for a person's decision.
type pendingProposal struct {
	ID, By, Summary string
	Items           []string
}

// chrome is what every page shows around its content: the Playbook's name
// and which page it is.
type chrome struct {
	Playbook string
	Page     string
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
	Note  string // why next doesn't hand out agent work, if it doesn't
}

// link is one named Link of an Artifact and the Artifacts it points to.
type link struct {
	Name string
	IDs  []string
}

func (e *env) backlogView() (any, error) {
	pb, st, err := e.load()
	if err != nil {
		return nil, err
	}
	all, err := st.List()
	if err != nil {
		return nil, err
	}
	proposals, err := store.NewProposals(e.dir).List()
	if err != nil {
		return nil, err
	}
	// Why next skips agent work, as a person sees it: a Claim is shown as
	// such, so only the other reasons are notes.
	notes := map[string]string{}
	for _, s := range engine.Next(pb, engine.Actor{}, all, proposals).Skipped {
		if s.Artifact.Claim == "" {
			notes[s.Artifact.ID] = s.Reason
		}
	}
	v := backlog{chrome: chrome{pb.Name, "backlog"}}
	for _, t := range pb.Types {
		ta := typeArtifacts{Name: t.Name}
		for _, a := range engine.InOrder(pb, all) {
			if a.Type != t.Name {
				continue
			}
			r := artifactRow{Artifact: a, Work: pb.WorkOf(a)}
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
	for _, p := range proposals {
		if p.Status != engine.Pending {
			continue
		}
		pp := pendingProposal{ID: p.ID, By: "a person", Summary: p.Summary}
		if p.By != "" {
			pp.By = "agent session " + p.By
		}
		for _, it := range p.Items {
			pp.Items = append(pp.Items, it.String())
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
	sum := engine.Summarise(pb, l, e.now())
	return ledgerPage{
		chrome:        chrome{pb.Name, "ledger"},
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
	v := workflows{chrome: chrome{pb.Name, "workflows"}}
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
