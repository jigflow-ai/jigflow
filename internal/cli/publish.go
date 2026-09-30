package cli

import (
	"fmt"
	"strings"

	"github.com/jigflow-ai/jigflow/internal/adapter"
)

// cmdPublish publishes the Playbook's Skills, Guidelines and active
// Personas through an Adapter, in the format its coding agent expects, then
// warns about hooks that can answer the Confirmation form jfl's MCP server
// asks for.
func cmdPublish(e *env, args []string) error {
	remove := len(args) == 2 && args[0] == "--remove"
	if remove {
		args = args[1:]
	}
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		return fmt.Errorf("%w: jfl publish [--remove] <adapter> (%s)", errUsage, adapterNames())
	}
	a := adapter.Find(args[0])
	if a == nil {
		return fmt.Errorf("unknown Adapter %q (want %s)", args[0], adapterNames())
	}
	if remove {
		return e.unpublish(a)
	}
	if err := e.publish(a); err != nil {
		return err
	}
	e.warnFormHooks()
	return nil
}

// publish publishes the Playbook through the Adapter a and reports what it
// wrote and removed, then how to register jfl's MCP server when a can't.
func (e *env) publish(a *adapter.Adapter) error {
	if err := e.publishFiles(a); err != nil {
		return err
	}
	fmt.Fprint(e.stdout, a.RegisterHint)
	return nil
}

// publishFiles publishes the Playbook through the Adapter a and reports
// what it wrote and removed.
func (e *env) publishFiles(a *adapter.Adapter) error {
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

// unpublish removes what the Adapter a published into the project, and
// reports what it removed; jfl init no longer publishes through a after it.
// It needs no Playbook, so that one that fails to load can be stopped too.
func (e *env) unpublish(a *adapter.Adapter) error {
	removed, ok, err := a.Unpublish(e.dir)
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintf(e.stdout, "nothing is published for %s: nothing changed\n", a.Agent)
		return nil
	}
	fmt.Fprintf(e.stdout, "stopped publishing the Playbook for %s\n", a.Agent)
	for _, p := range removed {
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
