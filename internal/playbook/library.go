package playbook

import (
	"errors"
	"os"
	"path/filepath"
)

// LibraryDir is where the user's Persona Library is, one Markdown file per
// Persona named after it, shared by every project on the machine:
// $XDG_CONFIG_HOME/jigflow/personas, or ~/.config/jigflow/personas. It is
// empty when neither XDG_CONFIG_HOME nor HOME is set.
func LibraryDir(getenv func(string) string) string {
	config := getenv("XDG_CONFIG_HOME")
	if config == "" {
		home := getenv("HOME")
		if home == "" {
			return ""
		}
		config = filepath.Join(home, ".config")
	}
	return filepath.Join(config, "jigflow", "personas")
}

// Library reads the user's Persona Library: each Persona by name, with the
// Markdown describing it. A missing Library has no Personas. The Library
// is the user's, like the Playbook, so its Personas are usable as they are.
func Library(getenv func(string) string) (map[string]string, error) {
	personas := map[string]string{}
	dir := LibraryDir(getenv)
	if dir == "" {
		return personas, nil
	}
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return personas, nil
	}
	if err := readMarkdown(os.DirFS(dir), ".", personas); err != nil {
		return nil, err
	}
	return personas, nil
}
