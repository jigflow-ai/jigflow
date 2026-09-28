// Package adapter publishes a Playbook's Skills, Guidelines and active
// Personas in the format a particular coding agent expects. An Adapter
// renders the files; Publish writes them into the project and keeps track
// of which files it owns.
package adapter

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jigflow-ai/jigflow/internal/engine"
	"go.yaml.in/yaml/v3"
)

// File is one file an Adapter publishes, at a slash-separated path relative
// to the project root.
type File struct {
	Path    string
	Content string
	// Merge makes the file one a person writes too, of which only a part is
	// jfl's: a publish writes what Merge makes of the file's content, empty
	// when there is no file yet, in place of Content, keeping the rest.
	Merge func(old string) (string, error)
}

// Adapter publishes a Playbook for one coding agent.
type Adapter struct {
	Name   string   // how `jfl publish` names it
	Agent  string   // the coding agent it publishes for, for messages
	Where  []string // the files and directories it publishes into
	render func(pb *engine.Playbook, personas map[string]string) []File
}

// Adapters are the Adapters JigFlow ships, by name.
var Adapters = []Adapter{
	{Name: ClaudeCode, Agent: "Claude Code", Where: []string{claudeCodeSkills, claudeCodeSettings}, render: claudeCode},
	{Name: "agents-md", Agent: "agents that read AGENTS.md", Where: []string{"AGENTS.md", agentsSkills, agentsPersonas}, render: agentsMD},
}

// Find returns the Adapter with the given name, or nil.
func Find(name string) *Adapter {
	for i := range Adapters {
		if Adapters[i].Name == name {
			return &Adapters[i]
		}
	}
	return nil
}

// Manifest is the project-relative file recording, per Adapter, the files
// it published: those, and only those, a later publish may replace or
// remove. It is committed, like what it lists.
//
//	claude-code:
//	  - .claude/skills/implement/SKILL.md
const Manifest = ".jigflow/published.yaml"

const manifestHeader = "# Written by jfl publish: the files each Adapter published, which it may\n# replace or remove. Commit it.\n"

// Changes are what a publish did to the project, by path.
type Changes struct {
	Wrote   []string // files it created or whose content it changed
	Removed []string // files it published before that the Playbook no longer has
}

// Publish writes the files a renders from pb and the active personas (name
// -> the Markdown describing it) into the project rooted at root, and
// removes the ones it published before that it no longer renders. It
// refuses, changing nothing, to overwrite a file it didn't publish.
func (a *Adapter) Publish(root string, pb *engine.Playbook, personas map[string]string) (Changes, error) {
	var ch Changes
	if pb.Skills[Router] != nil {
		return ch, fmt.Errorf("Skill %q takes the name of the router Skill jfl publishes; rename it", Router)
	}
	manifest, err := readManifest(root)
	if err != nil {
		return ch, err
	}
	owned := manifest[a.Name]
	// The Manifest is committed, and so may be edited: never trust it to
	// remove a file the Adapter couldn't have published.
	for _, p := range owned {
		if !a.publishes(p) {
			return ch, fmt.Errorf("%s lists %s, which the %s Adapter doesn't publish; remove it from the list", Manifest, p, a.Name)
		}
	}
	files := a.render(pb, personas)
	var write []File
	for _, f := range files {
		old, err := os.ReadFile(abs(root, f.Path))
		exists := err == nil
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return ch, err
		}
		if f.Merge != nil {
			// Only a part of the file is jfl's; the rest is kept.
			if f.Content, err = f.Merge(string(old)); err != nil {
				return ch, err
			}
		}
		switch {
		case !exists:
			write = append(write, f)
		case string(old) == f.Content:
			// Already published, or identical to it: nothing to do.
		case f.Merge == nil && !slices.Contains(owned, f.Path):
			return ch, fmt.Errorf("%s was not published by jfl, so it isn't replaced; move it away, or rename the Playbook's Skill", f.Path)
		default:
			write = append(write, f)
		}
	}
	for _, f := range write {
		dst := abs(root, f.Path)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return ch, err
		}
		if err := os.WriteFile(dst, []byte(f.Content), 0o644); err != nil {
			return ch, err
		}
		ch.Wrote = append(ch.Wrote, f.Path)
	}
	var paths []string
	for _, f := range files {
		paths = append(paths, f.Path)
	}
	for _, p := range owned {
		if slices.Contains(paths, p) {
			continue
		}
		if err := removeFile(root, p); err != nil {
			return ch, err
		}
		ch.Removed = append(ch.Removed, p)
	}
	manifest[a.Name] = paths
	return ch, writeManifest(root, manifest)
}

// publishes reports whether rel is a file a publishes into.
func (a *Adapter) publishes(rel string) bool {
	if rel != path.Clean(rel) || strings.HasPrefix(rel, "../") {
		return false
	}
	for _, w := range a.Where {
		if rel == w || strings.HasPrefix(rel, w+"/") {
			return true
		}
	}
	return false
}

// abs is the path on disk of the project-relative, slash-separated rel.
func abs(root, rel string) string { return filepath.Join(root, filepath.FromSlash(rel)) }

// removeFile removes the published file rel, or only jfl's section or hooks
// of it when it has them, then each directory above it that this leaves empty.
func removeFile(root, rel string) error {
	data, err := os.ReadFile(abs(root, rel))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if rel == claudeCodeSettings {
		// Only jfl's hooks are jfl's; the person's settings stay.
		rest, err := withHooks(string(data), false)
		if err != nil {
			return err
		}
		if rest != "" {
			return os.WriteFile(abs(root, rel), []byte(rest), 0o644)
		}
	} else if rest := withSection(string(data), ""); rest != string(data) && strings.TrimSpace(rest) != "" {
		return os.WriteFile(abs(root, rel), []byte(rest), 0o644)
	}
	if err := os.Remove(abs(root, rel)); err != nil {
		return err
	}
	for dir := filepath.Dir(rel); dir != "." && dir != "/"; dir = filepath.Dir(dir) {
		if os.Remove(abs(root, dir)) != nil {
			break // not empty: it holds files someone else wrote
		}
	}
	return nil
}

// The markers around the section of a shared file that jfl publishes.
const (
	sectionBegin = "<!-- jigflow:begin: written by jfl publish from the Playbook; edit the Playbook, not this -->\n"
	sectionEnd   = "<!-- jigflow:end -->\n"
)

// withSection is content with jfl's section replaced by section, which may
// be empty to drop it. Content without a section gets it appended.
func withSection(content, section string) string {
	if section != "" {
		section = sectionBegin + section + sectionEnd
	}
	if before, rest, ok := strings.Cut(content, sectionBegin); ok {
		if _, after, ok := strings.Cut(rest, sectionEnd); ok {
			return before + section + after
		}
	}
	switch {
	case section == "" || content == "":
		return content + section
	case strings.HasSuffix(content, "\n"):
		return content + "\n" + section
	}
	return content + "\n\n" + section
}

func readManifest(root string) (map[string][]string, error) {
	m := map[string][]string{}
	data, err := os.ReadFile(filepath.Join(root, Manifest))
	if errors.Is(err, os.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", Manifest, err)
	}
	if m == nil {
		m = map[string][]string{}
	}
	return m, nil
}

func writeManifest(root string, m map[string][]string) error {
	out, err := yaml.Marshal(m)
	if err != nil {
		return err
	}
	dst := filepath.Join(root, Manifest)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, append([]byte(manifestHeader), out...), 0o644)
}
