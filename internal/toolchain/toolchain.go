// Package toolchain detects a project's toolchain from the files at its
// root, so that jfl init can propose Gates and a starter Guideline that fit
// it without a language pack (ADR 0006). It reads files and runs nothing.
package toolchain

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// The names of the Gates jfl init proposes commands for. A Playbook that
// declares a Gate by one of these names only gets the project's command.
const (
	Tests = "tests"
	Lint  = "lint"
)

// Toolchain is one toolchain a project uses, and the file that shows it.
type Toolchain struct {
	Name string // e.g. Go
	File string // e.g. go.mod
}

func (t Toolchain) String() string { return fmt.Sprintf("%s (%s)", t.Name, t.File) }

// Gate is a command the project checks its work with, under the name of
// the Gate it fits.
type Gate struct {
	Name string
	Cmd  string
}

// Detected is what a project's files show of how it is built and written.
type Detected struct {
	Toolchains []Toolchain
	// Gates holds at most one command per Gate name, tests first, from the
	// first toolchain that has one: the project's own Makefile before any
	// language's tools.
	Gates []Gate
	// Conventions are the files that configure how the project's code is
	// written: formatters, linters, compilers.
	Conventions []string
	// Docs are the documents that say how to work on the project.
	Docs []string
}

// detector recognises one toolchain by a file at the project's root, and
// reads the commands it checks work with.
type detector struct {
	name  string
	files []string // any one of them shows the toolchain
	gates func(root, file string) map[string]string
}

var detectors = []detector{
	{name: "Make", files: []string{"Makefile", "makefile", "GNUmakefile"}, gates: makeGates},
	{name: "Go", files: []string{"go.mod"}, gates: fixed(map[string]string{Tests: "go test ./...", Lint: "go vet ./..."})},
	{name: "Rust", files: []string{"Cargo.toml"}, gates: fixed(map[string]string{Tests: "cargo test", Lint: "cargo clippy -- -D warnings"})},
	{name: "Python", files: []string{"pyproject.toml", "setup.py", "requirements.txt"}, gates: pythonGates},
	{name: "Node.js", files: []string{"package.json"}, gates: nodeGates},
	{name: "PHP", files: []string{"composer.json"}, gates: phpGates},
}

// conventionFiles are the files, by glob at the project's root, that
// configure how code is written.
var conventionFiles = []string{
	".editorconfig",
	".golangci.y*ml", ".golangci.toml",
	"rustfmt.toml", ".rustfmt.toml", "clippy.toml",
	"ruff.toml", ".ruff.toml", ".flake8", "mypy.ini",
	".eslintrc*", "eslint.config.*", ".prettierrc*", "prettier.config.*", "biome.json", "tsconfig.json",
	"pint.json", "phpstan.neon*", "phpcs.xml*", ".php-cs-fixer*.php",
	".rubocop.yml",
}

// docFiles are the documents, by path from the project's root, that say
// how to work on it.
var docFiles = []string{"CONTRIBUTING.md", "docs/CONTRIBUTING.md", "CONTEXT.md", "docs/adr"}

// Detect reads what the files at the project's root show.
func Detect(root string) (Detected, error) {
	var d Detected
	gates := map[string]string{}
	for _, det := range detectors {
		for _, f := range det.files {
			if !exists(filepath.Join(root, f)) {
				continue
			}
			d.Toolchains = append(d.Toolchains, Toolchain{Name: det.name, File: f})
			for name, cmd := range det.gates(root, f) {
				if _, ok := gates[name]; !ok {
					gates[name] = cmd
				}
			}
			break
		}
	}
	for _, name := range []string{Tests, Lint} {
		if cmd, ok := gates[name]; ok {
			d.Gates = append(d.Gates, Gate{Name: name, Cmd: cmd})
		}
	}
	for _, pattern := range conventionFiles {
		matches, err := filepath.Glob(filepath.Join(root, pattern))
		if err != nil {
			return d, err
		}
		for _, m := range matches {
			d.Conventions = append(d.Conventions, filepath.Base(m))
		}
	}
	for _, f := range docFiles {
		if exists(filepath.Join(root, f)) {
			d.Docs = append(d.Docs, f)
		}
	}
	return d, nil
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func fixed(gates map[string]string) func(string, string) map[string]string {
	return func(string, string) map[string]string { return gates }
}

// makeTarget matches a Makefile rule's target at the start of a line.
func makeTarget(name string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^` + name + `\s*:([^=]|$)`)
}

// makeGates are make test and make lint, for the targets the Makefile has.
func makeGates(root, file string) map[string]string {
	data, err := os.ReadFile(filepath.Join(root, file))
	if err != nil {
		return nil
	}
	gates := map[string]string{}
	for _, name := range []string{"test", "lint"} {
		if makeTarget(name).Match(data) {
			gates[map[string]string{"test": Tests, "lint": Lint}[name]] = "make " + name
		}
	}
	return gates
}

// pythonGates are pytest, and Ruff when the project configures it.
func pythonGates(root, _ string) map[string]string {
	gates := map[string]string{Tests: "pytest"}
	pyproject, _ := os.ReadFile(filepath.Join(root, "pyproject.toml"))
	if exists(filepath.Join(root, "ruff.toml")) || exists(filepath.Join(root, ".ruff.toml")) || strings.Contains(string(pyproject), "[tool.ruff") {
		gates[Lint] = "ruff check ."
	}
	return gates
}

// npmDefaultTest is the test script npm init writes, which only fails.
const npmDefaultTest = `echo "Error: no test specified" && exit 1`

// nodeGates are the package's test and lint scripts, run by the package
// manager its lockfile shows.
func nodeGates(root, file string) map[string]string {
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	data, err := os.ReadFile(filepath.Join(root, file))
	if err != nil || json.Unmarshal(data, &pkg) != nil {
		return nil
	}
	pm := "npm"
	for _, lock := range [][2]string{{"pnpm-lock.yaml", "pnpm"}, {"yarn.lock", "yarn"}, {"bun.lock", "bun"}, {"bun.lockb", "bun"}} {
		if exists(filepath.Join(root, lock[0])) {
			pm = lock[1]
			break
		}
	}
	gates := map[string]string{}
	if s, ok := pkg.Scripts["test"]; ok && s != npmDefaultTest {
		gates[Tests] = pm + " test"
	}
	if _, ok := pkg.Scripts["lint"]; ok {
		gates[Lint] = pm + " run lint"
	}
	return gates
}

// phpGates are Composer's test script, or else Pest or PHPUnit, and Pint or
// PHPStan when the project configures them.
func phpGates(root, file string) map[string]string {
	var pkg struct {
		Scripts    map[string]any    `json:"scripts"`
		RequireDev map[string]string `json:"require-dev"`
	}
	data, err := os.ReadFile(filepath.Join(root, file))
	if err != nil || json.Unmarshal(data, &pkg) != nil {
		return nil
	}
	gates := map[string]string{}
	switch {
	case pkg.Scripts["test"] != nil:
		gates[Tests] = "composer test"
	case pkg.RequireDev["pestphp/pest"] != "":
		gates[Tests] = "vendor/bin/pest"
	case exists(filepath.Join(root, "phpunit.xml")) || exists(filepath.Join(root, "phpunit.xml.dist")):
		gates[Tests] = "vendor/bin/phpunit"
	}
	switch {
	case pkg.Scripts["lint"] != nil:
		gates[Lint] = "composer lint"
	case exists(filepath.Join(root, "pint.json")) || pkg.RequireDev["laravel/pint"] != "":
		gates[Lint] = "vendor/bin/pint --test"
	case exists(filepath.Join(root, "phpstan.neon")) || exists(filepath.Join(root, "phpstan.neon.dist")):
		gates[Lint] = "vendor/bin/phpstan analyse"
	}
	return gates
}

// Guideline is a starter Guideline for the project: what d shows of how it
// is built, checked and written, for the person to edit into what matters
// there. It is empty when d shows nothing.
func (d Detected) Guideline() string {
	if len(d.Toolchains)+len(d.Conventions)+len(d.Docs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Conventions\n\nFollow how this repository is already written. jfl init wrote this starter from the files it found; edit it to say what matters here.\n")
	if len(d.Toolchains) > 0 {
		b.WriteString("\n## Toolchain\n\n")
		for _, t := range d.Toolchains {
			fmt.Fprintf(&b, "- %s\n", t)
		}
	}
	if len(d.Gates) > 0 {
		b.WriteString("\n## Checks\n\nThe Playbook's Gates run these before a Transition; run them yourself before asking for one, and fix what they report:\n\n")
		for _, g := range d.Gates {
			fmt.Fprintf(&b, "- %s: `%s`\n", g.Name, g.Cmd)
		}
	}
	if len(d.Conventions) > 0 {
		b.WriteString("\n## Configured conventions\n\nThese files configure how code here is written. Follow them, and don't restate or override them:\n\n")
		for _, f := range d.Conventions {
			fmt.Fprintf(&b, "- `%s`\n", f)
		}
	}
	if len(d.Docs) > 0 {
		b.WriteString("\n## Read first\n\n")
		for _, f := range d.Docs {
			fmt.Fprintf(&b, "- `%s`\n", f)
		}
	}
	b.WriteString("\n## Style\n\nMatch the code around your change: its naming, structure, error handling, comments and tests. Add no dependency, tool or pattern the repository doesn't already use unless the work asks for it.\n")
	return b.String()
}
