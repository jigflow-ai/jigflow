package main_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// repoRoot is the repository root, relative to this package's directory,
// where go test runs.
const repoRoot = "../.."

// TestDocsDescribeEveryCommand keeps the docs site's CLI reference in step
// with jfl's usage: every command jfl lists has its own section there.
func TestDocsDescribeEveryCommand(t *testing.T) {
	r := bin.NewProject(t).Run()
	usage := r.Stdout + r.Stderr
	_, commands, ok := strings.Cut(usage, "Commands:\n")
	if !ok {
		t.Fatalf("jfl printed no Commands section:\n%s", usage)
	}
	commands, _, _ = strings.Cut(commands, "\n\n")
	names := regexp.MustCompile(`(?m)^  ([a-z]+)`).FindAllStringSubmatch(commands, -1)
	if len(names) == 0 {
		t.Fatalf("found no commands in jfl's usage:\n%s", commands)
	}

	page, err := os.ReadFile(filepath.Join(repoRoot, "site", "cli.html"))
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if !strings.Contains(string(page), `id="`+n[1]+`"`) {
			t.Errorf("site/cli.html has no section for jfl %s", n[1])
		}
	}
}

// TestDocsLinksResolve checks that every relative link in the docs site and
// the README points at a file that exists, and, within the site, at an id
// that page declares.
func TestDocsLinksResolve(t *testing.T) {
	pages, err := filepath.Glob(filepath.Join(repoRoot, "site", "*.html"))
	if err != nil || len(pages) == 0 {
		t.Fatalf("no pages in site/ (%v)", err)
	}
	href := regexp.MustCompile(`href="([^"]+)"`)
	mdLink := regexp.MustCompile(`\]\(([^)\s]+)\)`)
	check := func(from string, links []string, anchors bool) {
		for _, l := range links {
			if strings.Contains(l, "://") || strings.HasPrefix(l, "mailto:") {
				continue
			}
			file, frag, _ := strings.Cut(l, "#")
			target := from
			if file != "" {
				target = filepath.Join(filepath.Dir(from), file)
			}
			data, err := os.ReadFile(target)
			if err != nil {
				if _, statErr := os.Stat(target); statErr != nil {
					t.Errorf("%s links to %s, which doesn't exist", from, l)
				}
				continue
			}
			if anchors && frag != "" && !strings.Contains(string(data), `id="`+frag+`"`) {
				t.Errorf("%s links to %s, but %s has no id %q", from, l, target, frag)
			}
		}
	}
	for _, p := range pages {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var links []string
		for _, m := range href.FindAllStringSubmatch(string(data), -1) {
			links = append(links, m[1])
		}
		check(p, links, true)
	}
	readme := filepath.Join(repoRoot, "README.md")
	data, err := os.ReadFile(readme)
	if err != nil {
		t.Fatal(err)
	}
	var links []string
	for _, m := range mdLink.FindAllStringSubmatch(string(data), -1) {
		links = append(links, m[1])
	}
	check(readme, links, false)
}
