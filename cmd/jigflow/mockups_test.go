package main_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTheLarapilotStylePlaybookKeepsMockupsInJigflowMockups(t *testing.T) {
	p := larapilot(t)

	if r := p.MustRun("check"); !strings.Contains(r.Stdout, "Mockups in .jigflow/mockups") {
		t.Errorf("check should say where the Mockups are:\n%s", r.Stdout)
	}
}

func TestAProjectPointsTheMockupFolderElsewhereOverridingItsBasePlaybooks(t *testing.T) {
	p := larapilot(t)
	p.Write(".jigflow/playbook.yaml", "name: shop\nextends: {builtin: larapilot}\nmockups: design/screens\n")

	if r := p.MustRun("check"); !strings.Contains(r.Stdout, "Mockups in design/screens") {
		t.Errorf("check should say the project's Mockup folder:\n%s", r.Stdout)
	}
}

func TestAPlaybookDeclaringNoMockupFolderHasNone(t *testing.T) {
	p := pocockPlaybook(t)

	if r := p.MustRun("check"); strings.Contains(r.Stdout, "Mockups") {
		t.Errorf("check should say nothing of Mockups:\n%s", r.Stdout)
	}
}

func TestTheMockupFolderMustBeInsideTheProject(t *testing.T) {
	for _, dir := range []string{"../designs", "/srv/designs", "design/../../designs", "."} {
		p := larapilot(t)
		p.Write(".jigflow/playbook.yaml", "name: shop\nextends: {builtin: larapilot}\nmockups: "+dir+"\n")

		r := p.Run("check")
		if r.ExitCode == 0 || !strings.Contains(r.Stderr, "mockups") {
			t.Errorf("check with mockups: %s = %d, want it refused:\n%s%s", dir, r.ExitCode, r.Stdout, r.Stderr)
		}
	}
}

func TestTheDashboardServesMockupsSandboxedWithTheirScriptsStylesAndImages(t *testing.T) {
	p := larapilot(t)
	p.Write(".jigflow/mockups/REQ-1/checkout.html", `<link rel="stylesheet" href="style.css"><script src="app.js"></script><img src="card.svg">`)
	p.Write(".jigflow/mockups/REQ-1/style.css", "body { color: teal; }\n")
	p.Write(".jigflow/mockups/REQ-1/app.js", "document.title = 'Checkout';\n")
	p.Write(".jigflow/mockups/REQ-1/card.svg", `<svg xmlns="http://www.w3.org/2000/svg"></svg>`)
	ui := p.StartUI()

	for file, want := range map[string]string{
		"checkout.html": "text/html",
		"style.css":     "text/css",
		"app.js":        "text/javascript",
		"card.svg":      "image/svg+xml",
	} {
		page := ui.Get("/mockups/REQ-1/" + file)
		if page.Status != http.StatusOK {
			t.Errorf("GET /mockups/REQ-1/%s: status %d\n%s", file, page.Status, page.HTML)
			continue
		}
		if got := page.Header.Get("Content-Security-Policy"); got != "sandbox allow-scripts" {
			t.Errorf("%s: Content-Security-Policy = %q, want sandbox allow-scripts", file, got)
		}
		if got := page.Header.Get("Content-Type"); !strings.HasPrefix(got, want) {
			t.Errorf("%s: Content-Type = %q, want %s", file, got, want)
		}
		if page.HTML != p.Read(".jigflow/mockups/REQ-1/"+file) {
			t.Errorf("%s served as %q", file, page.HTML)
		}
	}
}

func TestTheDashboardRefusesAnythingOutsideTheMockupFolder(t *testing.T) {
	p := larapilot(t)
	p.Write(".env", "SECRET=hunter2\n")
	p.Write(".jigflow/mockups/REQ-1/checkout.html", "<p>Checkout</p>")
	if err := os.Symlink("../../../.env", filepath.Join(p.Dir, ".jigflow/mockups/REQ-1/env.html")); err != nil {
		t.Skip("no symbolic links here:", err)
	}
	if err := os.Symlink("../..", filepath.Join(p.Dir, ".jigflow/mockups/project")); err != nil {
		t.Fatal(err)
	}
	ui := p.StartUI()

	for _, path := range []string{
		"/mockups/../.env",
		"/mockups/%2e%2e/%2e%2e/.env",
		"/mockups/REQ-1/..%2f..%2f..%2f.env",
		"/mockups/REQ-1/env.html",
		"/mockups/project/.env",
		"/mockups/REQ-1",
	} {
		page := ui.Do(ui.NewRequest(http.MethodGet, path, nil))
		if page.Status == http.StatusOK || strings.Contains(page.HTML, "hunter2") {
			t.Errorf("GET %s = %d, want it refused:\n%s", path, page.Status, page.HTML)
		}
	}
}

func TestADashboardOnAPlaybookWithNoMockupFolderServesNoMockups(t *testing.T) {
	p := pocockPlaybook(t)
	p.Write(".jigflow/mockups/S-1/screen.html", "<p>Screen</p>")
	ui := p.StartUI()

	if page := ui.Get("/mockups/S-1/screen.html"); page.Status != http.StatusNotFound {
		t.Errorf("GET a Mockup with no folder declared = %d, want 404", page.Status)
	}
}

func TestTheDesignPageListsMockupsBySubfolderEachLinkedToItsArtifact(t *testing.T) {
	p := larapilot(t)
	p.MustRun("create", "PRD", "--title", "Shop checkout", "--status", "adopt")
	p.MustRun("create", "Requirement", "--title", "Pay by card", "--field", "priority=must", "--link", "part_of=PRD-1")
	p.Write(".jigflow/mockups/REQ-1/checkout.html", "<p>Checkout</p>")
	p.Write(".jigflow/mockups/REQ-1/mobile/checkout.html", "<p>Checkout, small</p>")
	p.Write(".jigflow/mockups/REQ-1/style.css", "p { color: teal; }\n")
	p.Write(".jigflow/mockups/PRD-1/home.png", "\x89PNG\r\n")
	p.Write(".jigflow/mockups/palette/colours.html", "<p>Colours</p>")
	p.Write(".jigflow/mockups/sketch.svg", `<svg xmlns="http://www.w3.org/2000/svg"></svg>`)
	ui := p.StartUI()

	if backlog := get(t, ui, "/"); !strings.Contains(backlog, `<a href="/design"`) {
		t.Errorf("the navigation should link the Design page:\n%s", text(backlog))
	}
	page := get(t, ui, "/design")
	req := section(t, page, "REQ-1 Pay by card")
	wantText(t, req, "checkout.html", "mobile/checkout.html")
	if strings.Contains(req, "style.css") {
		t.Errorf("a Mockup's stylesheet isn't a Mockup:\n%s", req)
	}
	for _, want := range []string{`href="/artifacts/REQ-1"`, `src="/mockups/REQ-1/checkout.html"`, `sandbox="allow-scripts"`} {
		if !strings.Contains(req, want) {
			t.Errorf("REQ-1's Mockups should carry %s:\n%s", want, req)
		}
	}
	wantText(t, section(t, page, "PRD-1 Shop checkout"), "home.png")
	if strings.Index(page, "PRD-1 Shop checkout") > strings.Index(page, "REQ-1 Pay by card") {
		t.Errorf("the PRD's Mockups should come before its Requirement's, as the backlog orders them")
	}
	wantText(t, section(t, page, "Not linked to an Artifact"), "palette/colours.html", "sketch.svg")
}

func TestTheDesignPageSaysWhenThereIsNoMockupYet(t *testing.T) {
	p := larapilot(t)
	ui := p.StartUI()

	wantText(t, get(t, ui, "/design"), "No Mockup in .jigflow/mockups yet")
}

func TestWithNoMockupFolderThereIsNoDesignPage(t *testing.T) {
	p := pocockPlaybook(t)
	ui := p.StartUI()

	if backlog := get(t, ui, "/"); strings.Contains(backlog, `href="/design"`) {
		t.Errorf("the navigation shouldn't link a Design page:\n%s", text(backlog))
	}
	page := ui.Get("/design")
	if page.Status != http.StatusNotFound {
		t.Errorf("GET /design = %d, want 404", page.Status)
	}
	wantText(t, page.HTML, "declares no Mockup folder")
}

func TestAnArtifactsPageShowsTheMockupsItsBodyLinksSandboxedAndAMissingOneAsMissing(t *testing.T) {
	p := larapilot(t)
	p.MustRun("create", "PRD", "--title", "Shop checkout", "--status", "adopt")
	p.MustRun("create", "Requirement", "--title", "Pay by card", "--field", "priority=must", "--link", "part_of=PRD-1")
	p.Write(".jigflow/mockups/REQ-1/checkout.html", "<p>Checkout</p>")
	p.Write(".jigflow/mockups/REQ-1/card.png", "\x89PNG\r\n")
	p.Write(".jigflow/state/REQ-1.md", p.Read(".jigflow/state/REQ-1.md")+`
## Mockups

- [Checkout](.jigflow/mockups/REQ-1/checkout.html)
- [Card form](./.jigflow/mockups/REQ-1/card.png)
- [Receipt](.jigflow/mockups/REQ-1/receipt.html)
- [Checkout again](/mockups/REQ-1/checkout.html)
- [The README](README.md)
`)
	ui := p.StartUI()

	page := get(t, ui, "/artifacts/REQ-1")
	mockups := section(t, page, "Mockups")
	for _, want := range []string{`src="/mockups/REQ-1/checkout.html"`, `src="/mockups/REQ-1/card.png"`, `sandbox="allow-scripts"`} {
		if !strings.Contains(mockups, want) {
			t.Errorf("the Mockups section should carry %s:\n%s", want, mockups)
		}
	}
	if n := strings.Count(mockups, "<iframe"); n != 2 {
		t.Errorf("the Mockups section should preview 2 Mockups, once each, not %d:\n%s", n, mockups)
	}
	wantText(t, mockups, "REQ-1/receipt.html missing")
	if strings.Contains(mockups, "README") {
		t.Errorf("a link to a file outside the Mockup folder isn't a Mockup:\n%s", mockups)
	}
	if body := section(t, page, "Body"); !strings.Contains(body, `href="/mockups/REQ-1/checkout.html"`) {
		t.Errorf("the body's link to a Mockup should open it in the Dashboard:\n%s", body)
	}
}

func TestAnArtifactsPageLinkingNoMockupHasNoMockupsSection(t *testing.T) {
	p := larapilot(t)
	p.MustRun("create", "PRD", "--title", "Shop checkout", "--status", "adopt")
	ui := p.StartUI()

	if page := get(t, ui, "/artifacts/PRD-1"); strings.Contains(page, "<h2>Mockups</h2>") {
		t.Errorf("a page whose body links no Mockup should have no Mockups section:\n%s", text(page))
	}
}

func TestThePlaybookPageShowsTheMockupFolderAndWhereItComesFrom(t *testing.T) {
	p := larapilot(t)
	ui := p.StartUI()
	wantText(t, section(t, get(t, ui, "/playbook"), "Mockups"), ".jigflow/mockups builtin larapilot")

	p.Write(".jigflow/playbook.yaml", "name: shop\nextends: {builtin: larapilot}\nmockups: design/screens\n")
	wantText(t, section(t, get(t, ui, "/playbook"), "Mockups"), "design/screens project, overriding builtin larapilot")
}

func TestThePlaybookPageHasNoMockupFolderWhenNoneIsDeclared(t *testing.T) {
	p := pocockPlaybook(t)
	ui := p.StartUI()

	if page := get(t, ui, "/playbook"); strings.Contains(page, "<h2>Mockups</h2>") {
		t.Errorf("the Playbook page should show no Mockup folder:\n%s", text(page))
	}
}

func TestTheDesignPageUpdatesItselfWhenAMockupIsAddedOutsideJigflow(t *testing.T) {
	p := larapilot(t)
	p.Write(".jigflow/playbook.yaml", "name: shop\nextends: {builtin: larapilot}\nmockups: design/screens\n")
	ui := p.StartUI()
	if page := get(t, ui, "/design"); !listens(page) || !strings.Contains(page, `var here = "/design"`) {
		t.Errorf("the Design page should reload itself when the project changes:\n%s", page)
	}
	s := listen(t, ui)

	p.Write("design/screens/REQ-1/checkout.html", "<p>Checkout</p>")
	wantSignal(t, s, "a new Mockup")
}
