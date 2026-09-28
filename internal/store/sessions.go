package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

// SessionDir is the project-relative directory where each agent session's
// Focus is kept, one YAML file per session:
//
//	.jigflow/sessions/<session>.yaml
//
// A Focus lives only in the session, never in git, so the directory ignores
// itself.
const SessionDir = ".jigflow/sessions"

// Sessions is the session store of one project.
type Sessions struct {
	dir string
}

// NewSessions returns the session store of the project rooted at root.
func NewSessions(root string) *Sessions {
	return &Sessions{dir: filepath.Join(root, SessionDir)}
}

// sessionFile is the on-disk form of a session.
type sessionFile struct {
	Focus string `yaml:"focus"`
}

// SetFocus records id as the agent session's Focus; an empty id clears it.
func (s *Sessions) SetFocus(session, id string) error {
	if !validID(session) {
		return fmt.Errorf("invalid session id %q", session)
	}
	if id == "" {
		err := os.Remove(s.path(session))
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(s.dir, ".gitignore"), []byte("*\n"), 0o644); err != nil {
		return err
	}
	data, err := yaml.Marshal(sessionFile{Focus: id})
	if err != nil {
		return err
	}
	return os.WriteFile(s.path(session), data, 0o644)
}

func (s *Sessions) path(session string) string { return filepath.Join(s.dir, session+".yaml") }

// Unfocus clears the Focus of every session whose Focus is the Artifact id.
func (s *Sessions) Unfocus(id string) error {
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		session, ok := strings.CutSuffix(e.Name(), ".yaml")
		if !ok || e.IsDir() {
			continue
		}
		data, err := os.ReadFile(s.path(session))
		if err != nil {
			return err
		}
		var sf sessionFile
		if err := yaml.Unmarshal(data, &sf); err != nil {
			return fmt.Errorf("%s: %w", s.path(session), err)
		}
		if sf.Focus == id {
			if err := s.SetFocus(session, ""); err != nil {
				return err
			}
		}
	}
	return nil
}
