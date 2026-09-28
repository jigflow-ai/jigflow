package cli

import (
	"fmt"
	"slices"
	"strings"

	"github.com/jigflow-ai/jigflow/internal/engine"
)

// cmdSimulate walks a pretend Artifact of one Artifact Type through the
// Playbook and prints every Path it can take. It is a dry run: it writes no
// state and runs no Gates or Actions.
func cmdSimulate(e *env, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("%w: jfl simulate <Type>", errUsage)
	}
	pb, _, err := e.load()
	if err != nil {
		return err
	}
	t := pb.Type(args[0])
	if t == nil {
		names := make([]string, len(pb.Types))
		for i, other := range pb.Types {
			names[i] = other.Name
		}
		return fmt.Errorf("unknown Artifact Type %q. Declared Types: %s", args[0], strings.Join(names, ", "))
	}
	paths := engine.Paths(t)
	n := "1 path"
	if len(paths) != 1 {
		n = fmt.Sprintf("%d paths", len(paths))
	}
	fmt.Fprintf(e.stdout, "%s: %s from an initial Status to a final one\n", t.Name, n)
	for i, p := range paths {
		fmt.Fprintf(e.stdout, "\n%d. %s\n", i+1, strings.Join(p.Statuses, " → "))
		for j, status := range p.Statuses {
			fmt.Fprintf(e.stdout, "   %s: %s\n", status, strings.Join(atStatus(t, status), "; "))
			if j < len(p.Transitions) {
				fmt.Fprintf(e.stdout, "     %s\n", strings.Join(onTransition(p.Transitions[j]), "; "))
			}
		}
	}
	return nil
}

// atStatus describes what happens to an Artifact in status: the Skill its
// Binding runs and the Readiness that work waits on, or that a person works
// on it, or that it is final.
func atStatus(t *engine.ArtifactType, status string) []string {
	if slices.Contains(t.Final, status) {
		return []string{"final"}
	}
	skill := t.Bindings[status]
	if skill == "" {
		return []string{"no Binding, human work"}
	}
	marks := []string{"Skill /" + skill}
	if conds := t.Readiness[status]; len(conds) > 0 {
		marks = append(marks, "Readiness: "+conditions(conds))
	}
	return marks
}

// onTransition describes taking tr: whether only a person may, the Guards
// that must hold, the Gates that must succeed and the Actions run after.
func onTransition(tr engine.Transition) []string {
	marks := []string{"→ " + tr.To}
	if tr.Human {
		marks = append(marks, "Human Transition"+prefixed(", ", onlyIn(tr)))
	}
	if len(tr.Guards) > 0 {
		marks = append(marks, "Guards: "+conditions(tr.Guards))
	}
	if len(tr.Gates) > 0 {
		marks = append(marks, "Gates: "+commandList(tr.Gates))
	}
	if len(tr.Actions) > 0 {
		marks = append(marks, "Actions: "+commandList(tr.Actions))
	}
	return marks
}

// conditions describes what Readiness or Guards need, every one of them.
func conditions(conds []engine.Condition) string {
	s := make([]string, len(conds))
	for i, c := range conds {
		s[i] = c.String()
	}
	return strings.Join(s, " and ")
}

// commandList names each Gate or Action with the command it would run.
func commandList(cmds []engine.Command) string {
	s := make([]string, len(cmds))
	for i, c := range cmds {
		cmd := c.Cmd
		if cmd == "" {
			cmd = "no command yet"
		}
		s[i] = fmt.Sprintf("%s (%s)", c.Name, cmd)
	}
	return strings.Join(s, ", ")
}

// onlyIn says where the Human Transition tr may only be made, when the
// Playbook requires the Dashboard for it.
func onlyIn(tr engine.Transition) string {
	if tr.Dashboard {
		return "in the Dashboard only"
	}
	return ""
}

// prefixed returns s after prefix, or nothing when s is empty.
func prefixed(prefix, s string) string {
	if s == "" {
		return ""
	}
	return prefix + s
}
