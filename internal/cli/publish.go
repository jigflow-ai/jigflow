package cli

import (
	"fmt"
	"strings"

	"github.com/jigflow-ai/jigflow/internal/adapter"
)

// cmdPublish publishes the Playbook's Skills, Guidelines and active
// Personas through an Adapter, in the format its coding agent expects.
func cmdPublish(e *env, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("%w: jfl publish <adapter> (%s)", errUsage, adapterNames())
	}
	a := adapter.Find(args[0])
	if a == nil {
		return fmt.Errorf("unknown Adapter %q (want %s)", args[0], adapterNames())
	}
	return e.publish(a)
}

// publish publishes the Playbook through the Adapter a and reports what it
// wrote and removed.
func (e *env) publish(a *adapter.Adapter) error {
	pb, _, err := e.load()
	if err != nil {
		return err
	}
	personas, err := e.activePersonas(pb)
	if err != nil {
		return err
	}
	ch, err := a.Publish(e.dir, pb, personas)
	if err != nil {
		return err
	}
	if len(ch.Wrote)+len(ch.Removed) == 0 {
		fmt.Fprintf(e.stdout, "published Playbook %q for %s: nothing changed\n", pb.Name, a.Agent)
		return nil
	}
	fmt.Fprintf(e.stdout, "published Playbook %q for %s\n", pb.Name, a.Agent)
	for _, p := range ch.Wrote {
		fmt.Fprintf(e.stdout, "  wrote %s\n", p)
	}
	for _, p := range ch.Removed {
		fmt.Fprintf(e.stdout, "  removed %s\n", p)
	}
	return nil
}

// adapterNames lists the Adapters `jfl publish` knows.
func adapterNames() string {
	names := make([]string, len(adapter.Adapters))
	for i, a := range adapter.Adapters {
		names[i] = a.Name
	}
	return strings.Join(names, " or ")
}
