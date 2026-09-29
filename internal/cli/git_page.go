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
}

// gitLog returns up to n commits of the current branch, newest first,
// after skipping skip of them, with args narrowing them further; none on a
// branch with no commits yet.
func gitLog(dir string, skip, n int, args ...string) ([]gitCommit, error) {
	if err := exec.Command("git", "-C", dir, "rev-parse", "-q", "--verify", "HEAD").Run(); err != nil {
		return nil, nil // no commit yet
	}
	cmd := exec.Command("git", append([]string{"-C", dir, "log", "-z", "--format=%H%x1f%h%x1f%an%x1f%aI%x1f%s",
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
		if len(f) != 5 {
			continue
		}
		at, _ := time.Parse(time.RFC3339, f[3])
		cs = append(cs, gitCommit{Hash: f[0], Short: f[1], Author: f[2], At: at, Subject: f[4]})
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

// gitPage is a page of the current branch's commits, newest first.
type gitPage struct {
	chrome
	Repo         bool   // whether the project is in a git repository
	Branch       string // empty on a detached HEAD
	Commits      []gitCommit
	N            int    // the page's number, from 1
	Newer, Older string // the pages of newer and older commits, if any
}

// gitView builds the page of the current branch's commits numbered by the
// query's page, from 1.
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
		// One more than a page, to know whether there are older ones.
		cs, err := gitLog(e.dir, (n-1)*gitPageSize, gitPageSize+1)
		if err != nil {
			return nil, err
		}
		if len(cs) > gitPageSize {
			cs, v.Older = cs[:gitPageSize], "/git?page="+strconv.Itoa(n+1)
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

// gitHead looks at the project's repository: where it is, its current
// branch and the commit that branch is at, so that a page updates when a
// commit is made, the branch changes or the project becomes a repository,
// whether through jfl or not.
func gitHead(dir string) look {
	return look{due: func() time.Duration { return filesEvery }, fingerprint: func() string {
		// Outside a repository, or on a branch with no commit yet, what
		// git says is as stable as its answer.
		out, _ := exec.Command("git", "-C", dir, "rev-parse", "HEAD", "--absolute-git-dir", "--symbolic-full-name", "HEAD").CombinedOutput()
		return string(out)
	}}
}
