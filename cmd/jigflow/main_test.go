package main_test

import (
	"debug/elf"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/jigflow-ai/jigflow/internal/clitest"
)

var bin *clitest.Binary

// fakeConnector is the path of the fake Connector executable, which keeps
// its tracker in a JSON file and logs every request it gets.
var fakeConnector string

// githubConnector is the path of the GitHub Issues Connector, shipped
// alongside jfl.
var githubConnector string

func TestMain(m *testing.M) {
	var err error
	bin, err = clitest.Build("github.com/jigflow-ai/jigflow/cmd/jigflow")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if fakeConnector, err = bin.BuildHelper("github.com/jigflow-ai/jigflow/internal/clitest/fakeconnector", "fakeconnector"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		bin.Cleanup()
		os.Exit(1)
	}
	if githubConnector, err = bin.BuildHelper("github.com/jigflow-ai/jigflow/cmd/jfl-connector-github", "jfl-connector-github"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		bin.Cleanup()
		os.Exit(1)
	}
	code := m.Run()
	bin.Cleanup()
	os.Exit(code)
}

func TestBothNamesRunTheSameBinary(t *testing.T) {
	p := bin.NewProject(t)
	for _, exe := range []string{bin.Jigflow, bin.Jfl} {
		r := p.RunAs(exe, "version")
		if r.ExitCode != 0 {
			t.Fatalf("%s version exited %d: %s", exe, r.ExitCode, r.Stderr)
		}
		if !strings.HasPrefix(r.Stdout, "jigflow ") {
			t.Errorf("%s version printed %q, want it to start with %q", exe, r.Stdout, "jigflow ")
		}
	}
}

func TestBinaryIsStatic(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("static-link check reads ELF headers")
	}
	f, err := elf.Open(bin.Jigflow)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, prog := range f.Progs {
		if prog.Type == elf.PT_INTERP {
			t.Fatal("binary has a dynamic loader (PT_INTERP); want a static executable")
		}
	}
	if libs, _ := f.ImportedLibraries(); len(libs) > 0 {
		t.Fatalf("binary links shared libraries: %v", libs)
	}
}
