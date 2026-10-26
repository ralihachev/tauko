package server

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"
)

// GalleryEntry is one course in the public gallery (examples/index.json).
type GalleryEntry struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Cards       int    `json:"cards"`
	Installed   bool   `json:"installed"`
}

// Job is a course being added in the background: generated with Claude (about a
// minute) or installed from the gallery. One runs at a time; clients poll /api/job.
type Job struct {
	Kind        string    `json:"kind"`  // "gen" or "add"
	Label       string    `json:"label"` // the topic, or the gallery id
	State       string    `json:"state"` // "running", "done" or "failed"
	Step        string    `json:"step,omitempty"`
	Error       string    `json:"error,omitempty"`
	CourseID    string    `json:"course_id,omitempty"`
	CourseTitle string    `json:"course_title,omitempty"`
	Started     time.Time `json:"started"`
}

// jobTimeout bounds a job: a generation drafts and verifies with two claude -p calls.
const jobTimeout = 20 * time.Minute

func (s *Server) handleGallery(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Gallery == nil {
		http.Error(w, "the gallery isn't available", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	entries, err := s.cfg.Gallery(ctx)
	if err != nil {
		log.Printf("gallery: %v", err)
		http.Error(w, "couldn't read the course gallery", http.StatusBadGateway)
		return
	}
	lib := s.library()
	for i := range entries {
		_, entries[i].Installed = lib.Course(entries[i].ID)
	}
	writeJSON(w, entries)
}

func (s *Server) handleAddCourse(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	id := strings.TrimSpace(req.ID)
	if s.cfg.Add == nil || s.cfg.Reload == nil {
		http.Error(w, "installing courses isn't available", http.StatusServiceUnavailable)
		return
	}
	if id == "" {
		http.Error(w, "give a course id", http.StatusBadRequest)
		return
	}
	s.startJob(w, &Job{Kind: "add", Label: id, Step: "Downloading"}, func(ctx context.Context, progress func(string)) (string, error) {
		return id, s.cfg.Add(ctx, id)
	})
}

func (s *Server) handleGenCourse(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Topic string `json:"topic"`
		Level string `json:"level"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	topic := strings.TrimSpace(req.Topic)
	if s.cfg.Gen == nil || s.cfg.Reload == nil {
		http.Error(w, "Making a course needs Claude Code (the claude command) installed", http.StatusServiceUnavailable)
		return
	}
	if topic == "" || len(topic) > 200 {
		http.Error(w, "give a topic of up to 200 characters", http.StatusBadRequest)
		return
	}
	s.startJob(w, &Job{Kind: "gen", Label: topic, Step: "Starting"}, func(ctx context.Context, progress func(string)) (string, error) {
		return s.cfg.Gen(ctx, topic, strings.TrimSpace(req.Level), progress)
	})
}

// startJob runs work in the background unless another job is running. When it
// succeeds, the server reloads its courses and makes the new one the active course.
func (s *Server) startJob(w http.ResponseWriter, job *Job, work func(ctx context.Context, progress func(string)) (string, error)) {
	s.mu.Lock()
	if s.job != nil && s.job.State == "running" {
		s.mu.Unlock()
		http.Error(w, "a course is already being added; wait for it to finish", http.StatusConflict)
		return
	}
	job.State, job.Started = "running", s.now()
	s.job = job
	snapshot := *job
	s.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), jobTimeout)
		defer cancel()
		progress := func(step string) {
			s.mu.Lock()
			s.job.Step = step
			s.mu.Unlock()
		}
		id, err := work(ctx, progress)
		title := ""
		if err == nil {
			progress("Loading the course")
			title, err = s.reload(id)
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if err != nil {
			log.Printf("%s %q: %v", job.Kind, job.Label, err)
			s.job.State, s.job.Error = "failed", err.Error()
			return
		}
		s.job.State, s.job.Step, s.job.CourseID, s.job.CourseTitle = "done", "", id, title
	}()
	writeJSON(w, snapshot)
}

// reload swaps in the courses on disk and makes id the active course.
func (s *Server) reload(id string) (string, error) {
	lib, err := s.cfg.Reload()
	if err != nil {
		return "", err
	}
	c, ok := lib.Course(id)
	if !ok {
		return "", errors.New("the course was installed but didn't load; run tauko doctor")
	}
	s.libMu.Lock()
	s.lib = lib
	s.libMu.Unlock()
	if err := s.store.Set(keyActiveCourse, id); err != nil {
		return "", err
	}
	return c.Title, nil
}

// handleGetJob reports the last job, or null when there was none.
func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	var job *Job
	if s.job != nil {
		j := *s.job
		job = &j
	}
	s.mu.Unlock()
	writeJSON(w, job)
}
