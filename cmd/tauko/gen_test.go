package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fakeCourse = "Here you go:\n```json\n" + `{
  "course": {"title": "Kubernetes networking", "subject": "programming", "description": "Know how pods talk."},
  "units": [{
    "title": "Services",
    "focus": "Stable names for moving pods",
    "lessons": [{"title": "What a Service is", "body": "Pods come and go, so their IPs change. A Service gives a set of pods one stable name and virtual IP."}],
    "drills": [
      {"lesson": 1, "type": "qa", "prompt": "Which Service type is reachable only inside the cluster?", "answers": ["ClusterIP"]},
      {"type": "cloze", "prompt": "kube-proxy programs ___ or IPVS rules on each node.", "hint": "the Linux packet filter", "answers": ["iptables"]}
    ]
  }]
}` + "\n```"

func TestRunGenInstallsAVerifiedCourse(t *testing.T) {
	dir := t.TempDir()
	calls := 0
	model := func(_ context.Context, system, prompt string, files []string) (string, error) {
		calls++
		if calls == 2 {
			// The verification pass sees the draft and returns it with a fix.
			if !strings.Contains(prompt, "ClusterIP") || !strings.Contains(system, "reviewer") {
				t.Errorf("verify pass didn't get the draft")
			}
			return strings.Replace(extractJSON(fakeCourse), `"units"`, `"changes": ["added NodePort note"], "units"`, 1), nil
		}
		return fakeCourse, nil
	}
	restarted := false
	_, err := runGen(context.Background(), model, genOptions{Topic: "k8s networking", Units: 1, Cards: 2, Yes: true, Dir: dir,
		Restart: func() error { restarted = true; return nil }}) // never the real server
	if err != nil {
		t.Fatal(err)
	}
	if !restarted {
		t.Error("server not restarted after install")
	}
	if calls != 2 {
		t.Fatalf("model called %d times, want 2 (draft + verify)", calls)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "kubernetes-networking", "decks", "01-services.json"))
	if err != nil {
		t.Fatal(err)
	}
	var deck map[string]any
	if err := json.Unmarshal(raw, &deck); err != nil {
		t.Fatal(err)
	}
	drills := deck["drills"].([]any)
	if deck["id"] != "kubernetes-networking-u01" || drills[0].(map[string]any)["id"] != "kubernetes-networking-u01-q01" {
		t.Errorf("IDs not assigned by tauko: %v", deck["id"])
	}
	lessons := deck["lessons"].([]any)
	if lessons[0].(map[string]any)["id"] != "kubernetes-networking-u01-l01" || drills[0].(map[string]any)["lesson"] != "kubernetes-networking-u01-l01" {
		t.Errorf("lesson not linked: %v / %v", lessons[0], drills[0])
	}
	if _, tagged := drills[1].(map[string]any)["lesson"]; tagged {
		t.Errorf("an untagged card got a lesson")
	}
}

func TestValidateRejectsBadCloze(t *testing.T) {
	gc := &genCourse{}
	if err := json.Unmarshal([]byte(extractJSON(fakeCourse)), gc); err != nil {
		t.Fatal(err)
	}
	gc.Units[0].Drills[1].Prompt = "no blank here"
	files, err := courseFiles("k8s", gc)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateCourse(t.TempDir(), "k8s", files); err == nil || !strings.Contains(err.Error(), "___") {
		t.Errorf("validateCourse = %v, want a missing-blank error", err)
	}
}

func TestValidateRefusesExistingCourse(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "k8s"), 0o755)
	if err := validateCourse(dir, "k8s", map[string][]byte{}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("validateCourse = %v, want already-exists", err)
	}
}

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{
		"Kubernetes networking":    "kubernetes-networking",
		"Suomi B2: verbien rektio": "suomi-b2-verbien-rektio",
		"En la estación y el niño": "en-la-estacion-y-el-nino",
		"   ":                      "course",
	} {
		if got := slug(in); got != want {
			t.Errorf("slug(%q) = %q, want %q", in, got, want)
		}
	}
}
