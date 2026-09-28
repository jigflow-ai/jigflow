package main_test

import (
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jigflow-ai/jigflow/internal/clitest"
	"go.yaml.in/yaml/v3"
)

// baseTicket is the Ticket Artifact Type of the Base Playbooks in these
// tests, bound to the implement Skill.
const baseTicket = `name: Ticket
prefix: T
statuses: [ready-for-agent, in-progress, done]
initial: [ready-for-agent]
final: [done]
bindings:
  ready-for-agent: implement
  in-progress: implement
transitions:
  - {from: ready-for-agent, to: in-progress}
  - {from: in-progress, to: done}
`

// writeBase writes a Base Playbook named base into dir, relative to the
// project root: the Ticket Artifact Type and the implement Skill.
func writeBase(p *clitest.Project, dir string) {
	p.Write(dir+"/playbook.yaml", "name: base\n")
	p.Write(dir+"/types/ticket.yaml", baseTicket)
	writeSkillIn(p, dir, "implement", true)
}

// writeSkillIn declares a bound Skill in the Playbook directory dir.
func writeSkillIn(p *clitest.Project, dir, name string, changes bool) {
	body := "---\nchanges: false\ninvocation: bound\n---\nWork on the Artifact in Focus.\n"
	if changes {
		body = strings.Replace(body, "changes: false", "changes: true", 1)
	}
	p.Write(dir+"/skills/"+name+"/SKILL.md", body)
}

// extending is a project whose Playbook, mine, extends the Base Playbook at
// the local path base and declares nothing of its own.
func extending(t *testing.T) *clitest.Project {
	t.Helper()
	p := bin.NewProject(t)
	writeBase(p, "base")
	p.Write(".jigflow/playbook.yaml", "name: mine\nextends: {path: base}\n")
	return p
}

func TestAPlaybookCanExtendABasePlaybookAtALocalPath(t *testing.T) {
	p := extending(t)
	if r := p.MustRun("check"); firstLine(r.Stdout) != `Playbook "mine": no problems` {
		t.Errorf("check = %q, want mine to have no problems", firstLine(r.Stdout))
	}
	p.MustRun("create", "Ticket", "--title", "Reset-token table")
	if got := firstLine(p.MustRun("next").Stdout); got != `run /implement on T-1 "Reset-token table"` {
		t.Errorf("next = %q, want the Base Playbook's Binding", got)
	}
}

func TestAnArtifactTypeOverridesTheBasePlaybooksOneOfTheSameName(t *testing.T) {
	p := extending(t)
	p.Write("base/types/spec.yaml", "name: Spec\nprefix: S\nstatuses: [draft, done]\ninitial: [draft]\nfinal: [done]\ntransitions:\n  - {from: draft, to: done}\n")
	p.Write(".jigflow/types/ticket.yaml", strings.Replace(baseTicket, "in-progress, done]", "in-progress, in-review, done]", 1)+
		"  - {from: in-progress, to: in-review}\n  - {from: in-review, to: done}\n")

	p.MustRun("check")
	p.MustRun("create", "Ticket", "--title", "Reset-token table")
	p.MustRun("move", "T-1", "in-progress")
	p.MustRun("move", "T-1", "in-review")
	p.MustRun("create", "Spec", "--title", "Password reset by email")
}

func TestProblemsAreReportedInTheFileThatDeclaresThePart(t *testing.T) {
	p := extending(t)
	p.Write("base/types/spec.yaml", "name: Spec\nprefix: S\nstatuses: [draft, done]\ninitial: [draft]\nfinal: [done]\n")
	p.Write(".jigflow/types/ticket.yaml", strings.Replace(baseTicket, "in-progress: implement", "in-progress: implemnt", 1))

	r := p.Run("check")
	for _, want := range []string{
		"the Playbook has 3 problems:",
		`base/types/spec.yaml: Status "done" can't be reached from an initial Status`,
		`base/types/spec.yaml: Status "draft" has no Transition out and isn't final`,
		`.jigflow/types/ticket.yaml: the Binding of "in-progress" names Skill "implemnt", which the Playbook doesn't declare`,
	} {
		if r.ExitCode != 1 || !strings.Contains(r.Stderr, want) {
			t.Errorf("check: exit %d, stderr %q; want exit 1 and %q", r.ExitCode, r.Stderr, want)
		}
	}
}

func TestASkillOverridesTheBasePlaybooksOneOfTheSameName(t *testing.T) {
	p := extending(t)
	p.Write("base/skills/implement/SKILL.md", "---\nchanges: true\n---\nWork on it.\n")
	r := p.Run("check")
	if want := "base/skills/implement/SKILL.md: a Skill must declare its Invocation Mode"; r.ExitCode != 1 || !strings.Contains(r.Stderr, want) {
		t.Fatalf("check: exit %d, stderr %q; want exit 1 and %q", r.ExitCode, r.Stderr, want)
	}

	writeSkillIn(p, ".jigflow", "implement", true)
	p.MustRun("check")
}

func TestTheMergedPlaybookPassesThroughCheck(t *testing.T) {
	// The base's Issue Inbox is safe while its triage Skill changes nothing;
	// the project's triage Skill, overriding it, changes code.
	p := extending(t)
	p.Write("base/types/issue.yaml", `name: Issue
prefix: I
statuses: [needs-triage, done]
initial: [needs-triage]
final: [done]
inbox: [needs-triage]
bindings:
  needs-triage: triage
transitions:
  - {from: needs-triage, to: done}
`)
	writeSkillIn(p, "base", "triage", false)
	p.MustRun("check")

	writeSkillIn(p, ".jigflow", "triage", true)
	r := p.Run("check")
	if want := `base/types/issue.yaml: Issue Inbox "needs-triage" reaches /triage in "needs-triage" without a Human Transition`; r.ExitCode != 1 || !strings.Contains(r.Stderr, want) {
		t.Errorf("check: exit %d, stderr %q; want exit 1 and %q", r.ExitCode, r.Stderr, want)
	}
}

func TestPersonasAndGuidelinesOverrideTheBasePlaybooksOnesOfTheSameName(t *testing.T) {
	p := extending(t)
	p.Write("base/skills/implement/SKILL.md", "---\nchanges: true\ninvocation: bound\npersonas: [{name: reviewer}]\nguidelines: [go-style]\n---\nWork on it.\n")
	r := p.Run("check")
	for _, want := range []string{
		`base/skills/implement/SKILL.md: Persona "reviewer" doesn't exist and has no fallback description`,
		`base/skills/implement/SKILL.md: Guideline "go-style" doesn't exist`,
	} {
		if r.ExitCode != 1 || !strings.Contains(r.Stderr, want) {
			t.Errorf("check: exit %d, stderr %q; want exit 1 and %q", r.ExitCode, r.Stderr, want)
		}
	}

	for _, dir := range []string{"base", ".jigflow"} {
		p.Write(dir+"/personas/reviewer.md", "A senior engineer who reviews for clarity.\n")
		p.Write(dir+"/guidelines/go-style.md", "Run gofmt.\n")
		if r := p.Run("check"); r.ExitCode != 0 {
			t.Errorf("with the Persona and Guideline in %s: check exited %d, stderr %q", dir, r.ExitCode, r.Stderr)
		}
	}
}

func TestOnlyASingleExtendsIsSupported(t *testing.T) {
	const onlyOne = "a Playbook extends a single Base Playbook"
	for name, c := range map[string]struct{ playbook, want string }{
		"a list of Base Playbooks": {"extends: [{path: base}, {path: other}]\n", onlyOne},
		"two sources in one":       {"extends: {path: base, builtin: larapilot}\n", "extends names one of builtin, path or git"},
		"no source":                {"extends: {}\n", "extends names one of builtin, path or git"},
		"a misspelt source":        {"extends: {pth: base}\n", "pth"},
	} {
		t.Run(name, func(t *testing.T) {
			p := extending(t)
			writeBase(p, "other")
			p.Write(".jigflow/playbook.yaml", "name: mine\n"+c.playbook)

			r := p.Run("check")
			if want := ".jigflow/playbook.yaml: "; r.ExitCode != 1 || !strings.Contains(r.Stderr, want) || !strings.Contains(r.Stderr, c.want) {
				t.Errorf("check: exit %d, stderr %q; want exit 1, %q and %q", r.ExitCode, r.Stderr, want, c.want)
			}
		})
	}
}

func TestABasePlaybookThatItselfExtendsAnotherIsRefused(t *testing.T) {
	p := extending(t)
	writeBase(p, "other")
	p.Write("base/playbook.yaml", "name: base\nextends: {path: other}\n")

	r := p.Run("check")
	if want := "Base Playbook base extends another, but a Playbook extends a single Base Playbook"; r.ExitCode != 1 || !strings.Contains(r.Stderr, want) {
		t.Errorf("check: exit %d, stderr %q; want exit 1 and %q", r.ExitCode, r.Stderr, want)
	}
}

func TestAPlaybookCanExtendABasePlaybookBuiltIntoTheBinary(t *testing.T) {
	p := bin.NewProject(t)
	p.Write(".jigflow/playbook.yaml", "name: mine\nextends: {builtin: larapilot}\n")

	if r := p.MustRun("check"); firstLine(r.Stdout) != `Playbook "mine": no problems` {
		t.Errorf("check = %q, want mine to have no problems", firstLine(r.Stdout))
	}
	p.MustRun("create", "Task", "--title", "Reset-token table")
	if got := firstLine(p.MustRun("next").Stdout); got != `run /implement on T-1 "Reset-token table"` {
		t.Errorf("next = %q, want the built-in Base Playbook's Binding", got)
	}
}

func TestExtendingAnUnknownBuiltInPlaybookIsRefused(t *testing.T) {
	p := bin.NewProject(t)
	p.Write(".jigflow/playbook.yaml", "name: mine\nextends: {builtin: larapilot-ish}\n")

	r := p.Run("check")
	if want := `.jigflow/playbook.yaml: no Playbook "larapilot-ish" is built in (built in: larapilot)`; r.ExitCode != 1 || !strings.Contains(r.Stderr, want) {
		t.Errorf("check: exit %d, stderr %q; want exit 1 and %q", r.ExitCode, r.Stderr, want)
	}
}

// upstream is a git repository holding a Base Playbook, in a directory of
// its own next to the project.
type upstream struct {
	t   *testing.T
	p   *clitest.Project // writes its files
	dir string           // relative to the project root
	URL string
}

// gitUpstream commits the Base Playbook of writeBase to a new repository on
// branch main, tagged v1. It skips the test without git.
func gitUpstream(t *testing.T, p *clitest.Project) *upstream {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("needs git")
	}
	u := &upstream{t: t, p: p, dir: "upstream", URL: "file://" + filepath.Join(p.Dir, "upstream")}
	writeBase(p, u.dir)
	u.git("init", "-q", "-b", "main")
	u.commit("Base Playbook")
	u.git("tag", "v1")
	return u
}

func (u *upstream) git(args ...string) string {
	u.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = filepath.Join(u.p.Dir, u.dir)
	cmd.Env = append(cmd.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		u.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// commit commits every change in the repository and returns the commit.
func (u *upstream) commit(msg string) string {
	u.t.Helper()
	u.git("add", "-A")
	u.git("commit", "-q", "-m", msg)
	return u.git("rev-parse", "HEAD")
}

// addSpec adds a Spec Artifact Type to the Base Playbook, uncommitted.
func (u *upstream) addSpec() {
	u.p.Write(u.dir+"/types/spec.yaml", "name: Spec\nprefix: S\nstatuses: [draft, done]\ninitial: [draft]\nfinal: [done]\ntransitions:\n  - {from: draft, to: done}\n")
}

// lock is the project's lockfile, parsed.
func lock(t *testing.T, p *clitest.Project) map[string]string {
	t.Helper()
	var m map[string]string
	if err := yaml.Unmarshal([]byte(p.Read(".jigflow/playbook.lock")), &m); err != nil {
		t.Fatalf("playbook.lock is not YAML: %v", err)
	}
	return m
}

func TestAPlaybookCanExtendAGitBasePlaybookPinnedToARef(t *testing.T) {
	p := bin.NewProject(t)
	u := gitUpstream(t, p)
	v1 := u.git("rev-parse", "v1")
	p.Write(".jigflow/playbook.yaml", "name: mine\nextends: {git: "+u.URL+", ref: v1}\n")

	if r := p.MustRun("check"); firstLine(r.Stdout) != `Playbook "mine": no problems` {
		t.Errorf("check = %q, want mine to have no problems", firstLine(r.Stdout))
	}
	p.MustRun("create", "Ticket", "--title", "Reset-token table")
	if got := firstLine(p.MustRun("next").Stdout); got != `run /implement on T-1 "Reset-token table"` {
		t.Errorf("next = %q, want the git Base Playbook's Binding", got)
	}
	if got, want := lock(t, p), map[string]string{"git": u.URL, "ref": "v1", "commit": v1}; !maps.Equal(got, want) {
		t.Errorf("playbook.lock = %v, want %v", got, want)
	}
}

func TestAChangedUpstreamIsNeverPickedUpSilently(t *testing.T) {
	p := bin.NewProject(t)
	u := gitUpstream(t, p)
	first := u.git("rev-parse", "main")
	p.Write(".jigflow/playbook.yaml", "name: mine\nextends: {git: "+u.URL+", ref: main}\n")
	p.MustRun("check")

	u.addSpec()
	u.commit("Add Spec")
	// Even with nothing fetched before, the lockfile's commit is used.
	if err := os.RemoveAll(filepath.Join(p.Dir, ".jigflow", "cache")); err != nil {
		t.Fatal(err)
	}
	r := p.Run("create", "Spec", "--title", "Password reset by email")
	if r.ExitCode == 0 || !strings.Contains(r.Stderr, `unknown Artifact Type "Spec"`) {
		t.Errorf("create Spec after upstream added it: exit %d, stderr %q; want it refused, the Base Playbook pinned", r.ExitCode, r.Stderr)
	}
	if got := lock(t, p)["commit"]; got != first {
		t.Errorf("playbook.lock commit = %s, want %s still", got, first)
	}
}

func TestChangingTheRefMovesTheLockfileToItsCommit(t *testing.T) {
	p := bin.NewProject(t)
	u := gitUpstream(t, p)
	p.Write(".jigflow/playbook.yaml", "name: mine\nextends: {git: "+u.URL+", ref: v1}\n")
	p.MustRun("check")
	u.addSpec()
	v2 := u.commit("Add Spec")
	u.git("tag", "v2")

	p.Write(".jigflow/playbook.yaml", "name: mine\nextends: {git: "+u.URL+", ref: v2}\n")
	p.MustRun("create", "Spec", "--title", "Password reset by email")
	if got, want := lock(t, p), map[string]string{"git": u.URL, "ref": "v2", "commit": v2}; !maps.Equal(got, want) {
		t.Errorf("playbook.lock = %v, want %v", got, want)
	}
}

func TestTheGitCheckoutOfABasePlaybookIsNeverCommitted(t *testing.T) {
	p := bin.NewProject(t)
	u := gitUpstream(t, p)
	p.Write(".jigflow/playbook.yaml", "name: mine\nextends: {git: "+u.URL+", ref: v1}\n")
	p.MustRun("check")

	git := func(args ...string) error {
		cmd := exec.Command("git", args...)
		cmd.Dir = p.Dir
		return cmd.Run()
	}
	if err := git("init", "-q"); err != nil {
		t.Fatal(err)
	}
	if err := git("check-ignore", "-q", ".jigflow/cache/git/"+u.git("rev-parse", "v1")+"/types/ticket.yaml"); err != nil {
		t.Errorf("git would commit the Base Playbook's checkout: check-ignore said %v", err)
	}
	if err := git("check-ignore", "-q", ".jigflow/playbook.lock"); err == nil {
		t.Error("git ignores playbook.lock, which should be committed")
	}
}

func TestAGitBasePlaybookMustBePinnedToARef(t *testing.T) {
	for name, c := range map[string]struct{ extends, want string }{
		"git without a ref": {"{git: https://example.com/playbook.git}", "a git Base Playbook must be pinned to a ref"},
		"a ref without git": {"{path: base, ref: v1}", "only a git Base Playbook is pinned to a ref"},
	} {
		t.Run(name, func(t *testing.T) {
			p := extending(t)
			p.Write(".jigflow/playbook.yaml", "name: mine\nextends: "+c.extends+"\n")
			r := p.Run("check")
			if r.ExitCode != 1 || !strings.Contains(r.Stderr, ".jigflow/playbook.yaml: "+c.want) {
				t.Errorf("check: exit %d, stderr %q; want exit 1 and %q", r.ExitCode, r.Stderr, c.want)
			}
		})
	}
}
