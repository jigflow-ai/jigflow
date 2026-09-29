package adapter

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/jigflow-ai/jigflow/internal/engine"
)

// agentsSkills is where the AGENTS.md fallback publishes the Skills, one
// directory each holding its SKILL.md, for the agent to read when told to.
const agentsSkills = ".agents/skills"

// agentsPersonas is where the AGENTS.md fallback publishes every active
// Persona, one Markdown file each: personas/ under agentsDir.
const (
	agentsDir      = ".agents"
	agentsPersonas = agentsDir + "/personas"
)

// agentsRegisterMCP tells the person how to give an agent that reads
// AGENTS.md jfl's MCP server. Codex, Cursor and the like keep their MCP
// servers in the user's own configuration, which a publish never writes.
const agentsRegisterMCP = `To give your agent jfl's MCP tools, register them once in its own settings, which jfl doesn't write:
  codex mcp add jfl -- jfl mcp
(for Codex; in another agent, add a stdio MCP server named jfl that runs jfl mcp)
`

// agentsFrontmatter is the frontmatter of a SKILL.md an agent without an
// Adapter of its own reads.
type agentsFrontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description,omitempty"`
}

// agentsMD is the fallback for any agent without an Adapter of its own: a
// section of AGENTS.md describing the Playbook, the jfl commands and the
// rules, and every Skill as a file it says when to read. Such an agent
// can't be told how to start a Skill, so Invocation Modes and every Binding
// hint are advice.
func agentsMD(pb *engine.Playbook, personas map[string]string) []File {
	files := personaFiles(personas, agentsDir)
	var skills strings.Builder
	for _, name := range slices.Sorted(maps.Keys(pb.Skills)) {
		s := pb.Skills[name]
		bs := bindingsOf(pb, s.Name)
		dir := agentsSkills + "/" + s.Name
		fm := agentsFrontmatter{Name: s.Name, Description: description(s, bs)}
		files = append(files, File{Path: dir + "/SKILL.md", Content: skillMD(fm, advice(bs, false)+s.Prompt+personasSection(s, personas)+guidelinesSection(s))})
		files = append(files, skillPersonaFiles(s, personas, dir)...)
		files = append(files, guidelineFiles(pb, s, dir)...)

		if s.Invocation == engine.InvokedByUser {
			fmt.Fprintf(&skills, "- /%s: only when a person asks for it by name. Read `%s/SKILL.md` then.\n", s.Name, dir)
			continue
		}
		d := s.Description
		if d != "" && !strings.HasSuffix(d, ".") {
			d += "."
		}
		if d != "" {
			d += " "
		}
		fmt.Fprintf(&skills, "- /%s: %sRead `%s/SKILL.md` and follow it when you run it.\n", s.Name, d, dir)
	}
	section := fmt.Sprintf(agentsSection, pb.Name, statuses(pb, "####"), skills.String()) + "\n### Personas\n\n" + personasGuide(personas, agentsDir+"/")
	return append([]File{{Path: "AGENTS.md", Merge: func(old string) (string, error) { return withSection(old, section), nil }}}, files...)
}

// agentsSection is the AGENTS.md section: the Playbook's name, what works on
// each Status, and the Skills.
const agentsSection = "## JigFlow\n\n" +
	`This project's work follows the JigFlow Playbook %q. Its Artifacts are Markdown files in .jigflow/state that only the jfl command writes. Each Artifact is in a Status of its Artifact Type; a Status bound to a Skill is agent work, one with no Binding is a person's.

### Before any jfl command

Set JFL_SESSION to one id of your own for the whole session, e.g. ` + "`export JFL_SESSION=agent-$(date +%%s)`" + `, and keep it: it is how jfl tells your session from a person and from other sessions.

### Commands

- ` + "`jfl next`" + `: the Skill to run and the Artifact to run it on, which becomes your Focus. Start here, and come back here after each piece of work.
- ` + "`jfl next --autopilot`" + `: one step of autopilot. ` + autopilot + `
- ` + "`jfl move <id> <status>`" + `: move an Artifact through a declared Transition; its Gates must pass. It claims the Artifact for your session.
- ` + "`jfl create <Type> --title <title>`" + `: create an Artifact, only into an Inbox.
- ` + "`jfl propose <file>`" + `: put forward creations and Transitions for a person to approve or reject as one unit.
- ` + "`jfl check`" + ` and ` + "`jfl simulate <Type>`" + `: validate the Playbook, and dry-run an Artifact Type.

### Rules

- Never edit an Artifact's frontmatter: only jfl writes Statuses and frontmatter. You may edit an Artifact's body.
- A Human Transition is a person's to make: propose it, never try to make it.
- Creating an Artifact into a Status with a Binding, other than an Inbox, needs a Proposal.
- Only a person may approve or reject a Proposal, or migrate Artifacts.
- Work only on what jfl next hands you, and leave alone the Artifacts other sessions claim.
- A failing Gate refuses the Transition: fix the work, never the Gate.

### What works on each Status
%s
### Skills

%s`
