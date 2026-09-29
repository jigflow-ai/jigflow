package store

import (
	"regexp"
	"strings"

	"github.com/jigflow-ai/jigflow/internal/engine"
)

// The attribution of a comment to a Persona lives in the comment's text,
// not in the Store interface or the Connector protocol (ADR 0028), so it
// shows wherever the comment does, and the Dashboard reads it back from
// there.

// CommentHeading is the heading a file-kept Artifact's comment by by gets
// in its body: it names the author, an agent session or a person, and
// after them the Persona the comment is attributed to, if any.
func CommentHeading(by engine.Actor) string {
	who := "Comment"
	if by.Agent() {
		who += " by agent session " + by.Session
	}
	if by.Persona != "" {
		who += " as " + by.Persona
	}
	return "**" + who + ":**"
}

// CommentLead is the line a tracker-kept Artifact's comment attributed to
// the Persona persona leads with, before a blank line and its text: the
// tracker names the comment's author itself.
func CommentLead(persona string) string {
	return "**As " + persona + ":**"
}

// attributed is a comment's line attributed to a Persona: a file-kept
// comment's heading, its author then the Persona's name, or a tracker-kept
// comment's lead line, the Persona's name alone.
var attributed = regexp.MustCompile(`^\*\*(?:(Comment(?: by agent session \S+)?) as|As) ([A-Za-z0-9][A-Za-z0-9._-]*):\*\*$`)

// Attribution reads back the Persona a comment is attributed to from a
// line of its text: a file-kept comment's heading, as CommentHeading
// writes it, or a tracker-kept comment's lead line, as CommentLead writes
// it. It returns the author a heading names, such as "Comment by agent
// session A", empty for a lead line, whose author the tracker names, and
// the Persona's name, or false for a line that attributes nothing. The
// Dashboard shows the Persona from it, so whoever reads the text sees the
// attribution the Store wrote.
func Attribution(line string) (author, persona string, ok bool) {
	m := attributed.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}
