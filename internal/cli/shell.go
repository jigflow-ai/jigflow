package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/jigflow-ai/jigflow/internal/engine"
	"github.com/jigflow-ai/jigflow/internal/playbook"
)

// shell runs a user-declared command (a Gate or an Action) of the Transition
// from → to on the Artifact a, with `sh -c` in the project root, and returns
// its combined stdout and stderr. The command sees JFL_ARTIFACT (its id),
// JFL_TITLE, JFL_FROM and JFL_TO in its environment, and no stdin.
func (e *env) shell(command string, a engine.Artifact, from, to string) (string, error) {
	cmd := exec.Command("sh", "-c", command)
	cmd.Dir = e.dir
	cmd.Env = append(os.Environ(), "JFL_ARTIFACT="+a.ID, "JFL_TITLE="+a.Title, "JFL_FROM="+from, "JFL_TO="+to)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// indent formats command output for a report: each line on its own indented
// line, or nothing when there is no output.
func indent(out string) string {
	out = strings.TrimRight(out, "\n")
	if out == "" {
		return ""
	}
	return "\n    " + strings.ReplaceAll(out, "\n", "\n    ")
}

// noCommand says that the Gate g has no command yet, and where to give it
// one.
func noCommand(g engine.Command) string {
	return fmt.Sprintf("Gate %q has no command: give it one under gates in %s (jfl init proposes them)", g.Name, filepath.Join(playbook.Dir, "playbook.yaml"))
}
