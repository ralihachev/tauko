package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestReviewSchedules(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Date(2026, 10, 26, 10, 0, 0, 0, time.Local)

	if _, seen, _ := s.State("a"); seen {
		t.Fatal("new card reported as seen")
	}
	dueA, err := s.Review("a", 1, "type", "x", "wrong", now) // Again: back within minutes
	if err != nil {
		t.Fatal(err)
	}
	dueB, err := s.Review("b", 4, "tap", "", "", now) // Easy: days away
	if err != nil {
		t.Fatal(err)
	}
	if !dueA.Before(dueB) {
		t.Errorf("Again due %v should come before Easy due %v", dueA, dueB)
	}
	if dueB.Sub(now) < 24*time.Hour {
		t.Errorf("Easy on a new card should schedule at least a day out, got %v", dueB.Sub(now))
	}

	due, err := s.Due(now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0] != "a" {
		t.Errorf("due after 1h = %v, want [a]", due)
	}
	n, err := s.NewIntroducedSince(now.Add(-time.Hour))
	if err != nil || n != 2 {
		t.Errorf("NewIntroducedSince = %d, %v; want 2", n, err)
	}
	if _, err := s.Review("a", 5, "tap", "", "", now); err == nil {
		t.Error("rating 5 accepted")
	}

	st, err := s.Stats(time.Date(2026, 10, 26, 0, 0, 0, 0, time.Local))
	if err != nil {
		t.Fatal(err)
	}
	if st.ReviewsToday != 2 || st.Streak != 1 {
		t.Errorf("stats = %+v", st)
	}
}
