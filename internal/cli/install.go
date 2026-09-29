package cli

import (
	"fmt"
	"strings"

	"github.com/jigflow-ai/jigflow/internal/adapter"
)

// cmdInstall installs jfl's own Skills, those of no Playbook, for a coding
// agent at user level, through an Adapter: in every project, before any is
// set up. Unlike publish, it writes nothing into the project it runs in.
func cmdInstall(e *env, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("%w: jfl install <adapter> (%s)", errUsage, installableNames())
	}
	a := adapter.Find(args[0])
	if a == nil {
		return fmt.Errorf("unknown Adapter %q (want %s)", args[0], installableNames())
	}
	wrote, err := a.Install(e.getenv)
	if err != nil {
		return err
	}
	if len(wrote) == 0 {
		fmt.Fprintf(e.stdout, "installed jfl's Skills for %s: nothing changed\n", a.Agent)
		return nil
	}
	fmt.Fprintf(e.stdout, "installed jfl's Skills for %s\n", a.Agent)
	for _, p := range wrote {
		fmt.Fprintf(e.stdout, "  wrote %s\n", p)
	}
	return nil
}

// installableNames lists the Adapters `jfl install` can install through.
func installableNames() string {
	var names []string
	for _, a := range adapter.Adapters {
		if a.Installs() {
			names = append(names, a.Name)
		}
	}
	return strings.Join(names, " or ")
}
