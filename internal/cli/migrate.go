package cli

import (
	"errors"
	"fmt"

	"github.com/jigflow-ai/jigflow/internal/engine"
	"github.com/jigflow-ai/jigflow/internal/playbook"
	"github.com/jigflow-ai/jigflow/internal/store"
)

// migrateHint says how to bring orphaned Artifacts back into the Status
// machine.
const migrateHint = "map their Statuses to declared ones in a Playbook Migration (" + playbook.Dir + "/migrations/*.yaml), then run jfl migrate"

// cmdMigrate applies the Playbook Migrations. It loads the Playbook without
// refusing the Artifacts it orphans, since those are what it migrates, and
// saves them through the Store, which hashes what it writes. It changes
// Statuses outside any Transition, so only a person may run it.
func cmdMigrate(e *env, args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("%w: jfl migrate takes no arguments", errUsage)
	}
	if e.actor.Agent() {
		return errors.New("Only a human can migrate Artifacts.")
	}
	pb, err := playbook.Load(e.dir)
	if err != nil {
		return err
	}
	st := store.NewFile(e.dir)
	all, err := st.List()
	if err != nil {
		return err
	}
	migrated, err := engine.Migrate(pb, all)
	if err != nil {
		return fmt.Errorf("%w\n%s", err, migrateHint)
	}
	// Only jfl may change frontmatter (ADR 0002), so every migrated Artifact
	// is re-validated before any is saved: rewriting one edited outside jfl
	// would make its edit look like the Store's.
	for _, m := range migrated {
		if _, err := st.Verify(m.Artifact.ID); err != nil {
			return err
		}
	}
	for _, m := range migrated {
		if err := st.Save(m.Artifact); err != nil {
			return err
		}
		if err := e.recordStatus(m.Artifact, m.From); err != nil {
			return err
		}
		if err := e.unfocus(pb, m.Artifact); err != nil {
			return err
		}
		fmt.Fprintf(e.stdout, "%s: %s → %s\n", m.Artifact.ID, m.From, m.Artifact.Status)
	}
	return nil
}
