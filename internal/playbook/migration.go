package playbook

import (
	"fmt"
	"io/fs"
	"maps"
	"slices"

	"github.com/jigflow-ai/jigflow/internal/engine"
)

// migrationFile is a file of Playbook Migrations: for each Artifact Type, a
// mapping from old Statuses to new ones.
type migrationFile map[string]map[string]string

// readMigrations reads the project's Playbook Migrations into the Artifact
// Types of pb, and returns the problems with them. Only the project declares
// them, since its Artifacts are what they migrate, whether its own Playbook
// or its Base Playbook changed.
func readMigrations(fsys fs.FS, label string, pb *engine.Playbook) ([]string, error) {
	paths, err := fs.Glob(fsys, "migrations/*.yaml")
	if err != nil {
		return nil, err
	}
	slices.Sort(paths)
	var problems []string
	for _, p := range paths {
		var mf migrationFile
		if err := readYAML(fsys, label, p, &mf); err != nil {
			return nil, err
		}
		rel := in(label, p)
		for _, name := range slices.Sorted(maps.Keys(mf)) {
			t := pb.Type(name)
			if t == nil {
				problems = append(problems, fmt.Sprintf("%s: a Migration names Artifact Type %q, which the Playbook doesn't declare", rel, name))
				continue
			}
			for _, old := range slices.Sorted(maps.Keys(mf[name])) {
				if to := mf[name][old]; !slices.Contains(t.Statuses, to) {
					problems = append(problems, fmt.Sprintf("%s: the Migration of %s %q names Status %q, which a %s doesn't declare", rel, t.Name, old, to, t.Name))
					continue
				}
				if t.Migrations == nil {
					t.Migrations = map[string]string{}
				}
				t.Migrations[old] = mf[name][old]
			}
		}
	}
	return problems, nil
}
