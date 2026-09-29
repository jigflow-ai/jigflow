package main_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestAPersonCommentsOnAFileArtifactFromItsPage(t *testing.T) {
	p := pocockPlaybook(t)
	p.MustRun("create", "Spec", "--title", "Password reset by email")
	ui := p.StartUI()

	form := section(t, get(t, ui, "/artifacts/S-1"), "Comment")
	page := submit(t, ui, form, "Comment", url.Values{"text": {"Tokens should expire after an hour."}})
	if page.Status != http.StatusOK {
		t.Fatalf("commenting: status %d\n%s", page.Status, text(page.HTML))
	}
	wantText(t, section(t, page.HTML, "Done"), "commented on S-1")
	wantText(t, section(t, page.HTML, "Body"), "Comment:", "Tokens should expire after an hour.")

	if got := p.Read(".jigflow/state/S-1.md"); !strings.HasSuffix(got, "**Comment:**\n\nTokens should expire after an hour.\n") {
		t.Errorf("the comment should be appended to the body, signed as a person:\n%s", got)
	}
	show := p.MustRun("show", "S-1").Stdout
	if !strings.Contains(show, "Tokens should expire after an hour.") || strings.Contains(show, "agent session") {
		t.Errorf("jfl show should print the person's comment:\n%s", show)
	}
}

func TestAPersonsCommentOnATrackerArtifactIsAddedInTheTracker(t *testing.T) {
	p := trackerPlaybook(t)
	setTracker(t, p, map[string]any{"next": 42, "items": []map[string]any{
		{"id": "41", "title": "Add login page", "labels": []string{"ready-for-agent"}, "body": "## Acceptance\n\n- [ ] a form"},
	}})
	ui := p.StartUI()

	form := section(t, get(t, ui, "/artifacts/T-41"), "Comment")
	page := submit(t, ui, form, "Comment", url.Values{"text": {"Use the design system's inputs.\r\n\r\nNot raw ones."}})
	if page.Status != http.StatusOK {
		t.Fatalf("commenting: status %d\n%s", page.Status, text(page.HTML))
	}
	wantText(t, section(t, page.HTML, "Comments"), "Use the design system's inputs.", "Not raw ones.")

	if got, want := item(t, p, "41").Comments, []string{"Use the design system's inputs.\n\nNot raw ones."}; len(got) != 1 || got[0] != want[0] {
		t.Errorf("the tracker should keep the person's comment, with no agent marker: %q, want %q", got, want)
	}
	if body := item(t, p, "41").Body; body != "## Acceptance\n\n- [ ] a form" {
		t.Errorf("a tracker's body is left as it is:\n%s", body)
	}
	if show := p.MustRun("show", "T-41").Stdout; !strings.Contains(show, "## Comment 1\n\nUse the design system's inputs.\n\nNot raw ones.") {
		t.Errorf("jfl show should print the person's comment:\n%s", show)
	}
}

func TestCommentingNeedsTheLinkJflUiPrinted(t *testing.T) {
	p := pocockPlaybook(t)
	p.MustRun("create", "Spec", "--title", "Password reset by email")
	before := p.Read(".jigflow/state/S-1.md")
	ui := p.StartUI()

	page := ui.Curl(ui.NewRequest(http.MethodGet, "/artifacts/S-1", nil))
	looking := section(t, page.HTML, "Comment")
	if strings.Contains(looking, "<form") {
		t.Errorf("the page offers a comment form to a program that never opened the link:\n%s", looking)
	}
	wantText(t, looking, "open the link jfl ui printed in the terminal where you started it")

	form := url.Values{"text": {"Approved by me, honest."}}
	if page := ui.Curl(ui.NewRequest(http.MethodPost, "/artifacts/S-1/comment", form)); page.Status != http.StatusForbidden {
		t.Errorf("commenting without the link: status %d, want 403", page.Status)
	}
	req := ui.NewRequest(http.MethodPost, "/artifacts/S-1/comment", form)
	req.Header.Set("Origin", "http://attacker.example")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	if page := ui.Do(req); page.Status != http.StatusForbidden {
		t.Errorf("a comment posted from another site: status %d, want 403", page.Status)
	}
	if got := p.Read(".jigflow/state/S-1.md"); got != before {
		t.Errorf("a refused comment changed S-1:\n%s", got)
	}
}

func TestADashboardAnAgentSessionStartedOffersNoCommentAndRefusesOne(t *testing.T) {
	p := pocockPlaybook(t)
	p.MustRun("create", "Spec", "--title", "Password reset by email")
	before := p.Read(".jigflow/state/S-1.md")
	ui := p.StartUIInSession("A")

	looking := section(t, get(t, ui, "/artifacts/S-1"), "Comment")
	if strings.Contains(looking, "<form") {
		t.Errorf("a Dashboard agent session A started offers a comment form:\n%s", looking)
	}
	wantText(t, looking, "agent session A started this Dashboard")
	if page := ui.Post("/artifacts/S-1/comment", url.Values{"text": {"Looks good to me."}}); page.Status != http.StatusForbidden {
		t.Errorf("commenting in a Dashboard an agent started: status %d, want 403", page.Status)
	}
	if got := p.Read(".jigflow/state/S-1.md"); got != before {
		t.Errorf("a refused comment changed S-1:\n%s", got)
	}
}

func TestARefusedCommentSaysWhyOnThePageAndKeepsWhatWasTyped(t *testing.T) {
	p := pocockPlaybook(t)
	p.MustRun("create", "Spec", "--title", "Password reset by email")
	ui := p.StartUI()

	blank := ui.Post("/artifacts/S-1/comment", url.Values{"text": {"  \r\n "}})
	if blank.Status != http.StatusConflict {
		t.Errorf("an empty comment: status %d, want 409", blank.Status)
	}
	wantText(t, section(t, blank.HTML, "Not done"), "a comment needs some text")

	// Edited outside jfl, S-1's frontmatter no longer matches its hash:
	// jfl comment refuses to rewrite it, as in a terminal.
	p.Write(".jigflow/state/S-1.md", strings.Replace(p.Read(".jigflow/state/S-1.md"), "status: ready-for-agent", "status: ticketed", 1))
	page := ui.Post("/artifacts/S-1/comment", url.Values{"text": {"Tokens should expire after an hour."}})
	if page.Status != http.StatusConflict {
		t.Errorf("a comment jfl comment refuses: status %d, want 409\n%s", page.Status, text(page.HTML))
	}
	terminal := p.Run("comment", "S-1", "Tokens should expire after an hour.")
	if terminal.ExitCode == 0 {
		t.Fatalf("jfl comment should refuse S-1 edited outside jfl:\n%s", terminal.Stdout)
	}
	wantText(t, section(t, page.HTML, "Not done"), strings.TrimPrefix(strings.TrimSpace(terminal.Stderr), "jfl comment: "))
	if !strings.Contains(section(t, page.HTML, "Comment"), "Tokens should expire after an hour.</textarea>") {
		t.Errorf("the refused comment should stay in the form:\n%s", section(t, page.HTML, "Comment"))
	}
}

func TestCommentingOnAnUnknownArtifactSaysThereIsNone(t *testing.T) {
	p := pocockPlaybook(t)
	ui := p.StartUI()

	page := ui.Post("/artifacts/S-9/comment", url.Values{"text": {"Hello?"}})
	if page.Status != http.StatusNotFound {
		t.Errorf("commenting on S-9, which doesn't exist: status %d, want 404", page.Status)
	}
	wantText(t, page.HTML, "there is no Artifact S-9")
}
