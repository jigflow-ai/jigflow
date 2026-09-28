package adapter

import (
	"strings"

	"github.com/jigflow-ai/jigflow/internal/engine"
)

// guidelineFiles publishes the Guidelines Skill s names next to its
// SKILL.md, in dir, under guidelines/. They are separate files so the agent
// reads each only when it needs it.
func guidelineFiles(pb *engine.Playbook, s *engine.Skill, dir string) []File {
	var files []File
	for _, g := range s.Guidelines {
		files = append(files, File{Path: dir + "/" + guidelinePath(g), Content: pb.Guidelines[g]})
	}
	return files
}

// guidelinePath is where a Skill's Guideline is published, relative to its
// SKILL.md.
func guidelinePath(name string) string { return "guidelines/" + name + ".md" }

// guidelinesSection closes a published SKILL.md with a link to each
// Guideline s names, to read only when it applies.
func guidelinesSection(s *engine.Skill) string {
	if len(s.Guidelines) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n## Guidelines\n\nRead a Guideline only when the work at hand needs it:\n\n")
	for _, g := range s.Guidelines {
		b.WriteString("- [" + g + "](" + guidelinePath(g) + ")\n")
	}
	return b.String()
}
