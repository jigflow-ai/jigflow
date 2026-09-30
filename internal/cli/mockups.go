package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jigflow-ai/jigflow/internal/engine"
	"github.com/jigflow-ai/jigflow/internal/playbook"
)

// mockupsSandbox is the Content-Security-Policy every Mockup is served
// with: its scripts run, but in an opaque origin, so they can't read an
// open Dashboard page, its storage or its key, nor act for the person
// (ADR 0027).
const mockupsSandbox = "sandbox allow-scripts"

// serveMockup serves the file at name inside the Playbook's Mockup folder,
// sandboxed, and nothing outside it: not through "..", nor through a
// symbolic link leading out of it.
func (d *dashboard) serveMockup(w http.ResponseWriter, r *http.Request, name string) {
	w.Header().Set("Content-Security-Policy", mockupsSandbox)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	pb, _, err := d.e.load()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if pb.Mockups == "" {
		http.Error(w, "the Playbook declares no Mockup folder", http.StatusNotFound)
		return
	}
	root, err := os.OpenRoot(filepath.Join(d.e.dir, filepath.FromSlash(pb.Mockups)))
	if errors.Is(err, fs.ErrNotExist) {
		http.Error(w, "no Mockup "+name+": there is no Mockup folder "+pb.Mockups+" yet", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer root.Close()
	f, err := root.Open(filepath.FromSlash(name))
	if err != nil {
		// Not there, or outside the folder: either way not a Mockup.
		http.Error(w, "no Mockup "+name+" in "+pb.Mockups, http.StatusNotFound)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.Error(w, "no Mockup "+name+" in "+pb.Mockups, http.StatusNotFound)
		return
	}
	http.ServeContent(w, r, info.Name(), info.ModTime(), f)
}

// mockupKinds are the extensions of the files the Dashboard shows as
// Mockups: pages and pictures. The rest of the folder, such as a Mockup's
// stylesheets and scripts, is served for them, not listed.
var mockupKinds = []string{".html", ".htm", ".svg", ".png", ".jpg", ".jpeg", ".gif", ".webp", ".avif", ".pdf"}

// isMockup reports whether the file name is a Mockup, not something one uses.
func isMockup(name string) bool {
	return slices.Contains(mockupKinds, strings.ToLower(path.Ext(name)))
}

// mockupFile is a Mockup as a page shows it: its path inside the Mockup
// folder, which the Dashboard serves it at under /mockups/, and the name
// it is listed by.
type mockupFile struct {
	Path, Name string
	Missing    bool // a body links it, but the folder has no such file
}

// URL is where the Dashboard serves the Mockup.
func (f mockupFile) URL() string { return mockupURL(f.Path) }

// mockupURL is where the Dashboard serves the Mockup at path p inside the
// Mockup folder.
func mockupURL(p string) string { return "/mockups/" + (&url.URL{Path: p}).EscapedPath() }

// mockupGroup is the Mockups of the subfolder named after an Artifact.
type mockupGroup struct {
	ID, Title string
	Mockups   []mockupFile
}

// designPage lists the Mockups in the Playbook's Mockup folder: those in a
// subfolder named after an Artifact with it, the others apart.
type designPage struct {
	chrome
	Folder string
	Groups []mockupGroup
	Others []mockupFile
}

// errNoMockupFolder is why a Playbook declaring no Mockup folder has no
// Design page.
var errNoMockupFolder = fmt.Errorf("the Playbook declares no Mockup folder, so there is no Design page: a Playbook declares one with mockups in %s/playbook.yaml", playbook.Dir)

func (e *env) designView() (any, error) {
	pb, st, err := e.load()
	if err != nil {
		return nil, err
	}
	if pb.Mockups == "" {
		return nil, errNoMockupFolder
	}
	v := designPage{chrome: chrome{Playbook: pb.Name, Page: "design"}, Folder: pb.Mockups}
	files, err := mockupFiles(e.dir, pb.Mockups)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return v, nil
	}
	all, err := st.List()
	if err != nil {
		return nil, err
	}
	bySub := map[string][]mockupFile{}
	for _, f := range files {
		sub, rest, ok := strings.Cut(f, "/")
		if !ok {
			v.Others = append(v.Others, mockupFile{Path: f, Name: f})
			continue
		}
		bySub[sub] = append(bySub[sub], mockupFile{Path: f, Name: rest})
	}
	for _, a := range engine.InOrder(pb, all) {
		if mockups, ok := bySub[a.ID]; ok {
			v.Groups = append(v.Groups, mockupGroup{ID: a.ID, Title: a.Title, Mockups: mockups})
			delete(bySub, a.ID)
		}
	}
	for _, sub := range slices.Sorted(maps.Keys(bySub)) {
		for _, f := range bySub[sub] {
			v.Others = append(v.Others, mockupFile{Path: f.Path, Name: f.Path})
		}
	}
	slices.SortFunc(v.Others, func(x, y mockupFile) int { return strings.Compare(x.Path, y.Path) })
	return v, nil
}

// mockupFiles returns the paths, inside the Mockup folder dir of the
// project rooted at root, of the Mockups it holds, in lexical order; none
// when there is no such folder yet. A symbolic link isn't followed, as
// the Dashboard doesn't serve one leading out of the folder.
func mockupFiles(root, dir string) ([]string, error) {
	r, err := os.OpenRoot(filepath.Join(root, filepath.FromSlash(dir)))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer r.Close()
	var files []string
	err = fs.WalkDir(r.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() && isMockup(p) {
			files = append(files, p)
		}
		return nil
	})
	return files, err
}

// linkedMockups returns the Mockups at paths inside the Mockup folder dir,
// as a body links them, each missing when the folder has no such file.
func (e *env) linkedMockups(dir string, paths []string) ([]mockupFile, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	// With no folder yet, every one is missing.
	r, err := os.OpenRoot(filepath.Join(e.dir, filepath.FromSlash(dir)))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if err == nil {
		defer r.Close()
	}
	var files []mockupFile
	for _, p := range paths {
		f := mockupFile{Path: p, Name: p, Missing: true}
		if r != nil {
			if info, err := r.Stat(filepath.FromSlash(p)); err == nil && info.Mode().IsRegular() {
				f.Missing = false
			}
		}
		files = append(files, f)
	}
	return files, nil
}

// mockupsStay refuses a Playbook next that moves the Mockup folder of pb
// elsewhere while the folder holds a Mockup: bodies reference Mockups by
// their path from the project root, and jfl never edits a body (ADR 0030).
func (e *env) mockupsStay(pb, next *engine.Playbook) error {
	if pb.Mockups == "" || next.Mockups == pb.Mockups {
		return nil
	}
	files, err := mockupFiles(e.dir, pb.Mockups)
	if err != nil {
		return err
	}
	if len(files) > 0 {
		return fmt.Errorf("the Mockup folder %s holds %s, which Artifact bodies reference by their path from the project root, and jfl never edits a body: it stays %s while it holds any", pb.Mockups, plural(len(files), "Mockup"), pb.Mockups)
	}
	return nil
}
