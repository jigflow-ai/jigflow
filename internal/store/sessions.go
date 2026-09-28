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
// Focus and autopilot run are kept, one YAML file per session:
//
//	.jigflow/sessions/<session>.yaml
//
// They live only in the session, never in git, so the directory ignores
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

// Step is what an autopilot step handed out: the Artifact, the Status it was
// in, and the Skill to run on it; and why the session's last move since was
// refused, if it was.
type Step struct {
	ID      string `yaml:"id"`
	Status  string `yaml:"status"`
	Skill   string `yaml:"skill"`
	Refusal string `yaml:"refusal,omitempty"`
}

// sessionFile is the on-disk form of a session.
type sessionFile struct {
	Focus     string `yaml:"focus,omitempty"`
	Autopilot *Step  `yaml:"autopilot,omitempty"` // the last autopilot step
}

// SetFocus records id as the agent session's Focus; an empty id clears it.
func (s *Sessions) SetFocus(session, id string) error {
	return s.update(session, func(sf *sessionFile) { sf.Focus = id })
}

// Autopilot returns the step the agent session's autopilot run last handed
// out, or nil when it isn't in one.
func (s *Sessions) Autopilot(session string) (*Step, error) {
	sf, err := s.read(session)
	return sf.Autopilot, err
}

// SetAutopilot records the step the agent session's autopilot run handed
// out; nil ends the run.
func (s *Sessions) SetAutopilot(session string, step *Step) error {
	return s.update(session, func(sf *sessionFile) { sf.Autopilot = step })
}

func (s *Sessions) read(session string) (sessionFile, error) {
	var sf sessionFile
	if !validID(session) {
		return sf, fmt.Errorf("invalid session id %q", session)
	}
	data, err := os.ReadFile(s.path(session))
	if errors.Is(err, os.ErrNotExist) {
		return sf, nil
	}
	if err != nil {
		return sf, err
	}
	if err := yaml.Unmarshal(data, &sf); err != nil {
		return sf, fmt.Errorf("%s: %w", s.path(session), err)
	}
	return sf, nil
}

// update changes the session's file, removing it once nothing is left in it.
func (s *Sessions) update(session string, change func(*sessionFile)) error {
	sf, err := s.read(session)
	if err != nil {
		return err
	}
	change(&sf)
	if sf == (sessionFile{}) {
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
	data, err := yaml.Marshal(sf)
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
		sf, err := s.read(session)
		if err != nil {
			return err
		}
		if sf.Focus == id {
			if err := s.SetFocus(session, ""); err != nil {
				return err
			}
		}
	}
	return nil
}
