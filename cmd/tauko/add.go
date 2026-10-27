package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/ralihachev/tauko/internal/content"
)

// The public course gallery is examples/courses in the tauko repo.
const (
	galleryIndex   = "https://raw.githubusercontent.com/ralihachev/tauko/main/examples/index.json"
	galleryArchive = "https://github.com/ralihachev/tauko/archive/refs/heads/main.tar.gz"
)

type galleryEntry struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Cards       int    `json:"cards"`
}

// add lists the gallery, or installs a course from it (by id) or from a local folder.
func add(args []string) error {
	fl := flag.NewFlagSet("add", flag.ExitOnError)
	fl.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: tauko add              list the course gallery\n       tauko add <id>         install a course from the gallery\n       tauko add <folder>     install a course folder you have")
	}
	fl.Parse(args)
	dest := filepath.Join(dataDir(), "courses")

	switch fl.NArg() {
	case 0:
		return listGallery(envOr("TAUKO_GALLERY_INDEX", galleryIndex))
	case 1:
		name := fl.Arg(0)
		if info, err := os.Stat(name); err == nil && info.IsDir() {
			abs, err := filepath.Abs(name)
			if err != nil {
				return err
			}
			return installCourse(abs, filepath.Base(abs), dest, reloadServer)
		}
		return addFromGallery(name, envOr("TAUKO_GALLERY_ARCHIVE", galleryArchive), dest, reloadServer)
	default:
		fl.Usage()
		return errors.New("give one course id or folder")
	}
}

func listGallery(url string) error {
	var entries []galleryEntry
	if err := getJSON(url, &entries); err != nil {
		return fmt.Errorf("reading the course gallery: %w", err)
	}
	installed := map[string]bool{}
	if dirs, err := os.ReadDir(filepath.Join(dataDir(), "courses")); err == nil {
		for _, d := range dirs {
			installed[d.Name()] = true
		}
	}
	fmt.Printf("%sCourse gallery%s\n", bold, reset)
	for _, e := range entries {
		mark := " "
		if installed[e.ID] {
			mark = green + "✓" + reset
		}
		fmt.Printf("%s %-24s %s %s(%d cards)%s\n    %s%s%s\n", mark, e.ID, e.Title, dim, e.Cards, reset, dim, e.Description, reset)
	}
	fmt.Printf("\n%sinstall one: tauko add <id>   ·   make your own: tauko gen \"topic\"%s\n", dim, reset)
	return nil
}

// addFromGallery downloads the repo archive and installs examples/courses/<id> from it.
func addFromGallery(id, archiveURL, dest string, reload func() error) error {
	if strings.ContainsAny(id, `/\.`) {
		return fmt.Errorf("%q is not a course id or a folder", id)
	}
	step("Downloading " + id)
	tmp, err := os.MkdirTemp("", "tauko-add-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	n, err := extractCourse(archiveURL, id, tmp)
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("no course %q in the gallery; run `tauko add` to list them", id)
	}
	return installCourse(filepath.Join(tmp, id), id, dest, reload)
}

// extractCourse copies <repo>/examples/courses/<id>/** from a .tar.gz at url into dir/<id>.
func extractCourse(url, id, dir string) (int, error) {
	resp, err := (&http.Client{Timeout: time.Minute}).Get(url)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("download: %s", resp.Status)
	}
	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return 0, err
	}
	tr := tar.NewReader(gz)
	files := 0
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return files, nil
		}
		if err != nil {
			return files, err
		}
		// Archive paths look like tauko-main/examples/courses/<id>/decks/01.json.
		parts := strings.SplitN(path.Clean(h.Name), "/", 5)
		if len(parts) < 5 || parts[1] != "examples" || parts[2] != "courses" || parts[3] != id || h.Typeflag != tar.TypeReg {
			continue
		}
		rel := filepath.FromSlash(parts[4])
		if strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) || !strings.HasSuffix(rel, ".json") {
			continue
		}
		target := filepath.Join(dir, id, rel)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return files, err
		}
		b, err := io.ReadAll(io.LimitReader(tr, 8<<20))
		if err != nil {
			return files, err
		}
		if err := os.WriteFile(target, b, 0o644); err != nil {
			return files, err
		}
		files++
	}
}

// installCourse validates the course in srcDir next to the installed ones (so its IDs
// can't clash) and copies it into dest/name.
func installCourse(srcDir, name, dest string, reload func() error) error {
	if _, err := os.Stat(filepath.Join(dest, name)); err == nil {
		return fmt.Errorf("%s is already installed (%s)", name, filepath.Join(dest, name))
	}
	files := map[string][]byte{}
	err := fs.WalkDir(os.DirFS(srcDir), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".json") {
			return err
		}
		b, err := os.ReadFile(filepath.Join(srcDir, filepath.FromSlash(p)))
		files[p] = b
		return err
	})
	if err != nil {
		return err
	}
	var meta struct {
		ID string `json:"id"`
	}
	if raw, ok := files["course.json"]; !ok || json.Unmarshal(raw, &meta) != nil || meta.ID == "" {
		return errors.New("not a course: course.json with an id is missing (see docs/COURSES.md)")
	}

	tmp, err := os.MkdirTemp("", "tauko-check-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := writeCourse(filepath.Join(tmp, name), files); err != nil {
		return err
	}
	check := []content.Source{{FS: os.DirFS(tmp), Root: "."}}
	if _, err := os.Stat(dest); err == nil {
		check = append(check, content.Source{FS: os.DirFS(dest), Root: "."})
	}
	lib, err := content.LoadAll(check...)
	if err != nil {
		return fmt.Errorf("can't install %s: %w", name, err)
	}
	cards := 0
	for _, c := range lib.Cards {
		if c.Course == meta.ID {
			cards++
		}
	}
	if err := writeCourse(filepath.Join(dest, name), files); err != nil {
		return err
	}
	ok(fmt.Sprintf("installed %s (%d cards) to %s", name, cards, filepath.Join(dest, name)))
	if reload != nil {
		if err := reload(); err != nil {
			return err
		}
		ok("loaded into the server")
	}
	fmt.Printf("\nStudy it: press %sc%s in the tauko pane, or run %stauko course %s%s\n", bold, reset, bold, meta.ID, reset)
	return nil
}

func getJSON(url string, v any) error {
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
