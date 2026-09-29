package adapter

import (
	"maps"
	"slices"
	"strings"

	"github.com/jigflow-ai/jigflow/internal/engine"
	"go.yaml.in/yaml/v3"
)

// claudeCodeSkills is where Claude Code finds a project's Skills, one
// directory each holding its SKILL.md.
const claudeCodeSkills = ".claude/skills"

// claudeCodeFrontmatter is the frontmatter of a Claude Code SKILL.md.
type claudeCodeFrontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description,omitempty"`
	// ModelOff makes the Skill invocable only by a person, as /<name>.
	ModelOff bool `yaml:"disable-model-invocation,omitempty"`
	// Context "fork" runs the Skill in an isolated sub-agent.
	Context string `yaml:"context,omitempty"`
}

// claudeCode publishes every Skill as a Claude Code Skill, with the active
// Personas it names, the router Skill with every active Persona, the hooks
// through which the Ledger reads token usage from Claude Code's records, and
// jfl's MCP server in the project's .mcp.json.
func claudeCode(pb *engine.Playbook, personas map[string]string) []File {
	router := claudeCodeSkills + "/" + Router
	files := []File{claudeCodeHooks(), claudeCodeMCPServer(), {
		Path:    router + "/SKILL.md",
		Content: skillMD(claudeCodeFrontmatter{Name: Router, Description: routerDescription}, routerPrompt(pb)+"\n## Personas\n\n"+personasGuide(personas, "")),
	}}
	files = append(files, personaFiles(personas, router)...)
	for _, name := range slices.Sorted(maps.Keys(pb.Skills)) {
		s := pb.Skills[name]
		bs := bindingsOf(pb, s.Name)
		fm := claudeCodeFrontmatter{
			Name:        s.Name,
			Description: description(s, bs),
			ModelOff:    s.Invocation == engine.InvokedByUser,
		}
		// A sub-agent is the Skill's, not a Binding's: only a Skill every
		// Binding isolates runs in one, and the others get advice.
		forked := isolated(bs)
		if forked {
			fm.Context = "fork"
		}
		dir := claudeCodeSkills + "/" + s.Name
		body := advice(bs, forked) + s.Prompt + personasSection(s, personas) + guidelinesSection(s)
		files = append(files, File{Path: dir + "/SKILL.md", Content: skillMD(fm, body)})
		files = append(files, skillPersonaFiles(s, personas, dir)...)
		files = append(files, guidelineFiles(pb, s, dir)...)
	}
	return files
}

// skillMD is a SKILL.md: the YAML frontmatter fm, then body.
func skillMD(fm any, body string) string {
	out, err := yaml.Marshal(fm)
	if err != nil {
		panic(err) // a frontmatter struct always marshals
	}
	var b strings.Builder
	b.WriteString("---\n")
	b.Write(out)
	b.WriteString("---\n")
	b.WriteString(body)
	return b.String()
}
