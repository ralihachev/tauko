package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ralihachev/tauko/internal/content"
)

const examplesDir = "../../examples/courses"

// fakeArchive serves examples/courses the way GitHub's main.tar.gz lays it out.
func fakeArchive(t *testing.T) *httptest.Server {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	err := filepath.WalkDir(examplesDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(examplesDir, p)
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		name := "tauko-main/examples/courses/" + filepath.ToSlash(rel)
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(b)), Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		_, err = tw.Write(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gz.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(buf.Bytes()) }))
	t.Cleanup(srv.Close)
	return srv
}

func TestAddFromGallery(t *testing.T) {
	srv := fakeArchive(t)
	dest := t.TempDir()
	if err := addFromGallery("go-concurrency", srv.URL, dest, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "go-concurrency", "decks", "01-channels.json")); err != nil {
		t.Errorf("course not installed: %v", err)
	}
	if err := addFromGallery("go-concurrency", srv.URL, dest, nil); err == nil || !strings.Contains(err.Error(), "already installed") {
		t.Errorf("second install = %v, want already installed", err)
	}
	if err := addFromGallery("no-such-course", srv.URL, dest, nil); err == nil || !strings.Contains(err.Error(), "no course") {
		t.Errorf("unknown course = %v", err)
	}
	if err := addFromGallery("../etc", srv.URL, dest, nil); err == nil {
		t.Error("path-like id accepted")
	}
}

func TestAddLocalFolder(t *testing.T) {
	dest := t.TempDir()
	src, _ := filepath.Abs(filepath.Join(examplesDir, "kubernetes-networking"))
	if err := installCourse(src, "kubernetes-networking", dest, nil); err != nil {
		t.Fatal(err)
	}
	// A folder without course.json is refused.
	if err := installCourse(t.TempDir(), "empty", dest, nil); err == nil || !strings.Contains(err.Error(), "course.json") {
		t.Errorf("empty folder = %v", err)
	}
}

// TestGalleryIndex keeps examples/index.json in step with the course folders.
func TestGalleryIndex(t *testing.T) {
	raw, err := os.ReadFile("../../examples/index.json")
	if err != nil {
		t.Fatal(err)
	}
	var entries []galleryEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatal(err)
	}
	lib, err := content.LoadAll(content.Source{FS: os.DirFS(examplesDir), Root: "."})
	if err != nil {
		t.Fatal(err)
	}
	dirs, _ := content.CourseDirs(os.DirFS(examplesDir), ".")
	if len(entries) != len(dirs) {
		t.Fatalf("index has %d courses, examples/courses has %d", len(entries), len(dirs))
	}
	for _, e := range entries {
		c, ok := lib.Course(e.ID)
		if !ok {
			t.Errorf("index lists %q, but no course has that id", e.ID)
			continue
		}
		if _, err := os.Stat(filepath.Join(examplesDir, e.ID, "course.json")); err != nil {
			t.Errorf("course %q must live in a folder of the same name", e.ID)
		}
		n := 0
		for _, card := range lib.Cards {
			if card.Course == e.ID {
				n++
			}
		}
		if n != e.Cards || c.Title != e.Title {
			t.Errorf("index entry %q: %d cards %q, course has %d cards %q", e.ID, e.Cards, e.Title, n, c.Title)
		}
	}
}
