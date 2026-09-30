package cli

import (
	"bytes"
	"fmt"
	"net/http"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jigflow-ai/jigflow/internal/engine"
)

// gitPageSize is how many commits a page of the Git page lists.
const gitPageSize = 50

// gitDir returns the git directory of the repository the project is in,
// and false when it isn't in one, or there is no git to read it with: the
// Git page and the Commits lists are then left out, not shown as broken.
func gitDir(dir string) (string, bool) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--absolute-git-dir").Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

// gitCommit is a commit as the Dashboard lists it: linked to the Artifact
// whose id its subject starts with, followed by ":", as the commit Action
// names it (ADR 0020), when a Store keeps that Artifact.
type gitCommit struct {
	Hash, Short, Author string
	At                  time.Time
	Subject             string
	// Artifact is the id of the Artifact it is linked to, and Rest the
	// subject after it; Artifact is empty when it is linked to none.
	Artifact, Rest string
	Parents        []string // first parent first
	Refs           []gitRef // what points at it
	Lanes          laneCell // how the Git page draws it, in its lanes
}

// gitRef is a ref pointing at a commit, as the Git page labels it: Kind is
// "head" for HEAD, "branch", "remote" for a remote-tracking branch, or
// "tag".
type gitRef struct{ Kind, Name string }

// refsOf reads what %D says points at a commit, with --decorate=full;
// refs other than HEAD, branches, remote-tracking branches and tags, such
// as a stash, and a remote's own HEAD, are left out.
func refsOf(d string) []gitRef {
	var refs []gitRef
	for r := range strings.SplitSeq(d, ", ") {
		if b, ok := strings.CutPrefix(r, "HEAD -> "); ok {
			refs = append(refs, gitRef{"head", "HEAD"})
			r = b
		}
		if r == "HEAD" {
			refs = append(refs, gitRef{"head", "HEAD"})
		} else if b, ok := strings.CutPrefix(r, "refs/heads/"); ok {
			refs = append(refs, gitRef{"branch", b})
		} else if b, ok := strings.CutPrefix(r, "refs/remotes/"); ok && !strings.HasSuffix(b, "/HEAD") {
			refs = append(refs, gitRef{"remote", b})
		} else if b, ok := strings.CutPrefix(r, "tag: refs/tags/"); ok {
			refs = append(refs, gitRef{"tag", b})
		}
	}
	return refs
}

// gitLog returns up to n commits of the current branch, newest first,
// after skipping skip of them, with args narrowing them further or naming
// other commits to start from; none on a branch with no commits yet.
func gitLog(dir string, skip, n int, args ...string) ([]gitCommit, error) {
	if err := exec.Command("git", "-C", dir, "rev-parse", "-q", "--verify", "HEAD").Run(); err != nil {
		return nil, nil // no commit yet
	}
	cmd := exec.Command("git", append([]string{"-C", dir, "log", "-z", "--decorate=full", "--format=%H%x1f%h%x1f%an%x1f%aI%x1f%s%x1f%P%x1f%D",
		"--skip=" + strconv.Itoa(skip), "--max-count=" + strconv.Itoa(n)}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git log: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	var cs []gitCommit
	for rec := range strings.SplitSeq(strings.TrimSuffix(string(out), "\x00"), "\x00") {
		f := strings.Split(rec, "\x1f")
		if len(f) != 7 {
			continue
		}
		at, _ := time.Parse(time.RFC3339, f[3])
		cs = append(cs, gitCommit{Hash: f[0], Short: f[1], Author: f[2], At: at, Subject: f[4],
			Parents: strings.Fields(f[5]), Refs: refsOf(f[6])})
	}
	return cs, nil
}

// linkTo links each commit whose subject starts with "<id>:", for an
// Artifact kept, to that Artifact.
func linkTo(cs []gitCommit, kept map[string]engine.Artifact) {
	for i, c := range cs {
		id, rest, ok := strings.Cut(c.Subject, ":")
		if _, known := kept[id]; ok && known {
			cs[i].Artifact, cs[i].Rest = id, ":"+rest
		}
	}
}

// commitsOf returns the commits of the current branch whose subject starts
// with "<id>:", newest first.
func commitsOf(dir, id string) ([]gitCommit, error) {
	// --grep matches any line of the message: the subject is checked here.
	cs, err := gitLog(dir, 0, -1, "--grep=^"+regexp.QuoteMeta(id)+":", "--extended-regexp")
	if err != nil {
		return nil, err
	}
	var of []gitCommit
	for _, c := range cs {
		if strings.HasPrefix(c.Subject, id+":") {
			c.Artifact, c.Rest = id, strings.TrimPrefix(c.Subject, id)
			of = append(of, c)
		}
	}
	return of, nil
}

// gitPage is a page of the commits of every local branch and HEAD, newest
// first, drawn in lanes.
type gitPage struct {
	chrome
	Repo         bool   // whether the project is in a git repository
	Branch       string // empty on a detached HEAD
	Commits      []gitCommit
	Width        int    // how many lanes wide the page's graph is
	N            int    // the page's number, from 1
	Newer, Older string // the pages of newer and older commits, if any
}

// gitView builds the page of the commits of every local branch and HEAD
// numbered by the query's page, from 1. The lanes are laid out over every
// commit down to the page's last, so each keeps its column from page to
// page.
func (e *env) gitView(r *http.Request) func() (any, error) {
	return func() (any, error) {
		pb, st, err := e.load()
		if err != nil {
			return nil, err
		}
		n, err := strconv.Atoi(r.URL.Query().Get("page"))
		if err != nil || n < 1 {
			n = 1
		}
		v := gitPage{chrome: chrome{Playbook: pb.Name, Page: "git"}, N: n}
		if n > 1 {
			v.Path = "/git?page=" + strconv.Itoa(n)
		}
		if _, v.Repo = gitDir(e.dir); !v.Repo {
			return v, nil
		}
		if out, err := exec.Command("git", "-C", e.dir, "symbolic-ref", "-q", "--short", "HEAD").Output(); err == nil {
			v.Branch = strings.TrimSpace(string(out))
		}
		// One more than the pages down to this one, to know whether there
		// are older ones.
		cs, err := gitLog(e.dir, 0, n*gitPageSize+1, "--date-order", "--branches", "HEAD")
		if err != nil {
			return nil, err
		}
		if len(cs) > n*gitPageSize {
			cs, v.Older = cs[:n*gitPageSize], "/git?page="+strconv.Itoa(n+1)
		}
		lc := make([]laneCommit, len(cs))
		for i, c := range cs {
			lc[i] = laneCommit{Hash: c.Hash, Parents: c.Parents}
		}
		spine, err := spineOf(e.dir, v.Branch)
		if err != nil {
			return nil, err
		}
		rows := layOut(lc, spine)
		first := min((n-1)*gitPageSize, len(cs))
		cs, rows = cs[first:], rows[first:]
		for _, row := range rows {
			v.Width = max(v.Width, row.width())
		}
		for i := range cs {
			cs[i].Lanes = cellOf(rows[i], len(cs[i].Parents) > 1, v.Width)
		}
		if n > 1 {
			v.Newer = "/git?page=" + strconv.Itoa(n-1)
		}
		all, err := st.List()
		if err != nil {
			return nil, err
		}
		linkTo(cs, byID(all))
		v.Commits = cs
		return v, nil
	}
}

// spineOf returns the first-parent chain of the repository's default
// branch, newest first, which the Git page draws straight down the left:
// the local branch the remote origin's HEAD names, else main, else master,
// else current, the current branch; none when there is none of them.
func spineOf(dir, current string) ([]string, error) {
	var names []string
	if out, err := exec.Command("git", "-C", dir, "symbolic-ref", "-q", "--short", "refs/remotes/origin/HEAD").Output(); err == nil {
		names = append(names, strings.TrimPrefix(strings.TrimSpace(string(out)), "origin/"))
	}
	names = append(names, "main", "master")
	if current != "" {
		names = append(names, current)
	}
	for _, b := range names {
		ref := "refs/heads/" + b
		if exec.Command("git", "-C", dir, "rev-parse", "-q", "--verify", ref+"^{commit}").Run() != nil {
			continue
		}
		out, err := exec.Command("git", "-C", dir, "rev-list", "--first-parent", ref).Output()
		if err != nil {
			return nil, fmt.Errorf("git rev-list: %v", err)
		}
		return strings.Fields(string(out)), nil
	}
	return nil, nil
}

// gitHead looks at the project's repository: where it is, its current
// branch and every ref, so that a page updates when a commit is made on
// any branch, the branch changes, a tag or remote-tracking branch moves or
// the project becomes a repository, whether through jfl or not.
func gitHead(dir string) look {
	return func() string {
		// Outside a repository, or on a branch with no commit yet, what
		// git says is as stable as its answer.
		out, _ := exec.Command("git", "-C", dir, "rev-parse", "HEAD", "--absolute-git-dir", "--symbolic-full-name", "HEAD").CombinedOutput()
		refs, _ := exec.Command("git", "-C", dir, "for-each-ref", "--format=%(objectname) %(refname)").CombinedOutput()
		return string(out) + string(refs)
	}
}
