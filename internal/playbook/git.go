package playbook

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

// lockPath is the project-relative lockfile recording the commit a git
// Base Playbook is pinned to. It is committed with the Playbook.
var lockPath = filepath.Join(Dir, "playbook.lock")

// cacheDir holds the checkouts of git Base Playbooks, one per commit. It is
// never committed: the lockfile says which commit to fetch again.
var cacheDir = filepath.Join(Dir, "cache", "git")

type lockFile struct {
	Git    string `yaml:"git"`
	Ref    string `yaml:"ref"`
	Commit string `yaml:"commit"`
}

const lockHeader = `# Written by jfl: the commit the git Base Playbook is pinned to. Commit it.
# jfl keeps using this commit whatever its ref points to upstream now;
# change the ref in playbook.yaml to move to another one.
`

// gitBase reads the git Base Playbook ref names, and the commit it is
// pinned to. The first time, and whenever its URL or ref changes in the
// Playbook file, it resolves the ref to a commit and, with pin, records it
// in the lockfile; otherwise it reads the commit the lockfile records, so a
// changed upstream is never picked up silently. Without pin, as when a
// Playbook a Proposal would make is checked, the lockfile is left as it is.
func gitBase(root string, ref *baseRef, pin bool) (*layer, string, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return nil, "", fmt.Errorf("a git Base Playbook needs git installed: %w", err)
	}
	var lk lockFile
	data, err := os.ReadFile(filepath.Join(root, lockPath))
	switch {
	case err == nil:
		if err := decodeYAML(data, lockPath, &lk); err != nil {
			return nil, "", err
		}
	case !errors.Is(err, os.ErrNotExist):
		return nil, "", err
	}
	want := lk.Commit
	if lk.Git != ref.Git || lk.Ref != ref.Ref {
		want = ""
	}
	dir, commit, err := checkout(root, ref, want)
	if err != nil {
		return nil, "", fmt.Errorf("Base Playbook %s@%s: %w", ref.Git, ref.Ref, err)
	}
	if pin && (commit != lk.Commit || lk.Git != ref.Git || lk.Ref != ref.Ref) {
		out, err := yaml.Marshal(lockFile{Git: ref.Git, Ref: ref.Ref, Commit: commit})
		if err != nil {
			return nil, "", err
		}
		if err := os.WriteFile(filepath.Join(root, lockPath), append([]byte(lockHeader), out...), 0o644); err != nil {
			return nil, "", err
		}
	}
	l, err := readBase(os.DirFS(dir), ref.Git+"@"+ref.Ref)
	return l, commit, err
}

// checkout returns the directory holding the git Base Playbook at commit,
// fetching it when it isn't cached yet. With no commit, it fetches ref and
// returns the commit ref points to.
func checkout(root string, ref *baseRef, commit string) (string, string, error) {
	cache := filepath.Join(root, cacheDir)
	if commit != "" {
		if _, err := os.Stat(filepath.Join(cache, commit)); err == nil {
			return filepath.Join(cache, commit), commit, nil
		}
	}
	if err := os.MkdirAll(cache, 0o755); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(filepath.Join(root, Dir, "cache", ".gitignore"), []byte("*\n"), 0o644); err != nil {
		return "", "", err
	}
	tmp, err := os.MkdirTemp(cache, "fetch-")
	if err != nil {
		return "", "", err
	}
	defer os.RemoveAll(tmp)
	what := ref.Ref
	if commit != "" {
		what = commit
	}
	if _, err := git(tmp, "init", "-q"); err != nil {
		return "", "", err
	}
	if _, err := git(tmp, "fetch", "-q", "--depth", "1", ref.Git, what); err != nil {
		return "", "", err
	}
	got, err := git(tmp, "rev-parse", "FETCH_HEAD^{commit}")
	if err != nil {
		return "", "", err
	}
	if commit != "" && got != commit {
		return "", "", fmt.Errorf("fetched commit %s, but the lockfile pins %s", got, commit)
	}
	if _, err := git(tmp, "-c", "advice.detachedHead=false", "checkout", "-q", got); err != nil {
		return "", "", err
	}
	if err := os.RemoveAll(filepath.Join(tmp, ".git")); err != nil {
		return "", "", err
	}
	dir := filepath.Join(cache, got)
	if err := os.Rename(tmp, dir); err != nil {
		// Another jfl may have fetched the same commit meanwhile.
		if _, statErr := os.Stat(dir); statErr != nil {
			return "", "", err
		}
	}
	return dir, got, nil
}

// git runs git in dir and returns its trimmed output, or an error holding
// what it printed.
func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}
