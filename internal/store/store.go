// Package store persists scheduling state, review history and quiz results in SQLite.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	fsrs "github.com/open-spaced-repetition/go-fsrs/v3"
	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS card_state (
	card_id        TEXT PRIMARY KEY,
	due            INTEGER NOT NULL,
	stability      REAL NOT NULL,
	difficulty     REAL NOT NULL,
	elapsed_days   INTEGER NOT NULL,
	scheduled_days INTEGER NOT NULL,
	reps           INTEGER NOT NULL,
	lapses         INTEGER NOT NULL,
	state          INTEGER NOT NULL,
	last_review    INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS card_state_due ON card_state(due);
CREATE TABLE IF NOT EXISTS review_log (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	card_id     TEXT NOT NULL,
	rating      INTEGER NOT NULL,
	mode        TEXT NOT NULL,
	answer      TEXT NOT NULL DEFAULT '',
	verdict     TEXT NOT NULL DEFAULT '',
	reviewed_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS review_log_at ON review_log(reviewed_at);
CREATE TABLE IF NOT EXISTS quiz_result (
	id       INTEGER PRIMARY KEY AUTOINCREMENT,
	unit     TEXT NOT NULL,
	score    INTEGER NOT NULL,
	total    INTEGER NOT NULL,
	taken_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS kv (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS retired (
	card_id    TEXT PRIMARY KEY,
	retired_at INTEGER NOT NULL
);`

// Store wraps the SQLite database and the FSRS scheduler.
type Store struct {
	db   *sql.DB
	fsrs *fsrs.FSRS
}

// Open opens (and migrates) the database at path.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	params := fsrs.DefaultParam()
	params.EnableFuzz = true
	return &Store{db: db, fsrs: fsrs.NewFSRS(params)}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// State returns the FSRS card for id, or a new card if it was never reviewed.
func (s *Store) State(id string) (fsrs.Card, bool, error) {
	var c fsrs.Card
	var due, last int64
	err := s.db.QueryRow(`SELECT due, stability, difficulty, elapsed_days, scheduled_days, reps, lapses, state, last_review
		FROM card_state WHERE card_id = ?`, id).
		Scan(&due, &c.Stability, &c.Difficulty, &c.ElapsedDays, &c.ScheduledDays, &c.Reps, &c.Lapses, &c.State, &last)
	if errors.Is(err, sql.ErrNoRows) {
		return fsrs.NewCard(), false, nil
	}
	if err != nil {
		return c, false, err
	}
	c.Due, c.LastReview = time.Unix(due, 0), time.Unix(last, 0)
	return c, true, nil
}

// Review records a rating (1–4) for a card and returns its next due time.
func (s *Store) Review(id string, rating int, mode, answer, verdict string, now time.Time) (time.Time, error) {
	if rating < 1 || rating > 4 {
		return time.Time{}, fmt.Errorf("rating must be 1-4, got %d", rating)
	}
	card, _, err := s.State(id)
	if err != nil {
		return time.Time{}, err
	}
	next := s.fsrs.Next(card, now, fsrs.Rating(rating)).Card

	tx, err := s.db.Begin()
	if err != nil {
		return time.Time{}, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO card_state (card_id, due, stability, difficulty, elapsed_days, scheduled_days, reps, lapses, state, last_review)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(card_id) DO UPDATE SET due=excluded.due, stability=excluded.stability, difficulty=excluded.difficulty,
			elapsed_days=excluded.elapsed_days, scheduled_days=excluded.scheduled_days, reps=excluded.reps,
			lapses=excluded.lapses, state=excluded.state, last_review=excluded.last_review`,
		id, next.Due.Unix(), next.Stability, next.Difficulty, next.ElapsedDays, next.ScheduledDays,
		next.Reps, next.Lapses, next.State, next.LastReview.Unix()); err != nil {
		return time.Time{}, err
	}
	if _, err := tx.Exec(`INSERT INTO review_log (card_id, rating, mode, answer, verdict, reviewed_at) VALUES (?, ?, ?, ?, ?, ?)`,
		id, rating, mode, answer, verdict, now.Unix()); err != nil {
		return time.Time{}, err
	}
	return next.Due, tx.Commit()
}

// MarkRead records that a lesson was read. Lessons are read once, not reviewed, so the
// state row is never due; the log entry keeps today's counts and the streak honest.
func (s *Store) MarkRead(id string, now time.Time) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	never := now.AddDate(100, 0, 0).Unix()
	if _, err := tx.Exec(`INSERT INTO card_state (card_id, due, stability, difficulty, elapsed_days, scheduled_days, reps, lapses, state, last_review)
		VALUES (?, ?, 0, 0, 0, 0, 1, 0, ?, ?)
		ON CONFLICT(card_id) DO UPDATE SET reps = reps + 1, last_review = excluded.last_review`,
		id, never, fsrs.Review, now.Unix()); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO review_log (card_id, rating, mode, answer, verdict, reviewed_at) VALUES (?, 3, 'lesson', '', '', ?)`,
		id, now.Unix()); err != nil {
		return err
	}
	return tx.Commit()
}

// Due returns IDs of seen cards due at or before now, oldest due first.
func (s *Store) Due(now time.Time) ([]string, error) {
	return s.ids(`SELECT card_id FROM card_state WHERE due <= ? AND card_id NOT IN (SELECT card_id FROM retired) ORDER BY due`, now.Unix())
}

// Seen returns the set of card IDs that have scheduling state.
func (s *Store) Seen() (map[string]bool, error) {
	ids, err := s.ids(`SELECT card_id FROM card_state`)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		seen[id] = true
	}
	return seen, nil
}

// Recall is how well a seen card is remembered right now.
type Recall struct {
	ID         string
	R          float64 // FSRS retrievability: estimated probability of recall now
	LastReview time.Time
}

// Recalls returns every seen card with its current retrievability.
func (s *Store) Recalls(now time.Time) ([]Recall, error) {
	rows, err := s.db.Query(`SELECT card_id, due, stability, difficulty, elapsed_days, scheduled_days, reps, lapses, state, last_review
		FROM card_state WHERE card_id NOT IN (SELECT card_id FROM retired)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Recall
	for rows.Next() {
		var id string
		var c fsrs.Card
		var due, last int64
		if err := rows.Scan(&id, &due, &c.Stability, &c.Difficulty, &c.ElapsedDays, &c.ScheduledDays, &c.Reps, &c.Lapses, &c.State, &last); err != nil {
			return nil, err
		}
		c.Due, c.LastReview = time.Unix(due, 0), time.Unix(last, 0)
		out = append(out, Recall{ID: id, R: s.fsrs.GetRetrievability(c, now), LastReview: c.LastReview})
	}
	return out, rows.Err()
}

// NewIntroducedIDsSince returns the cards whose first review happened at or after t.
func (s *Store) NewIntroducedIDsSince(t time.Time) ([]string, error) {
	return s.ids(`SELECT card_id FROM review_log GROUP BY card_id HAVING MIN(reviewed_at) >= ?`, t.Unix())
}

// NextDue returns when the next seen card becomes due (zero if none).
func (s *Store) NextDue() (time.Time, error) {
	var due sql.NullInt64
	if err := s.db.QueryRow(`SELECT MIN(due) FROM card_state`).Scan(&due); err != nil || !due.Valid {
		return time.Time{}, err
	}
	return time.Unix(due.Int64, 0), nil
}

// NewIntroducedSince counts cards whose first review happened at or after t.
func (s *Store) NewIntroducedSince(t time.Time) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM (SELECT card_id FROM review_log GROUP BY card_id HAVING MIN(reviewed_at) >= ?)`, t.Unix()).Scan(&n)
	return n, err
}

// DayStats summarises review activity.
type DayStats struct {
	ReviewsToday  int     `json:"reviews_today"`
	AccuracyToday float64 `json:"accuracy_today"`
	Streak        int     `json:"streak_days"`
	TotalReviews  int     `json:"total_reviews"`
	Learned       int     `json:"learned"` // cards in review state, plus retired ones
	Retired       int     `json:"retired"` // cards marked "I know this"
}

// Stats computes activity stats; dayStart is local midnight of today.
func (s *Store) Stats(dayStart time.Time) (DayStats, error) {
	var st DayStats
	var good sql.NullInt64
	if err := s.db.QueryRow(`SELECT COUNT(*), SUM(rating >= 3) FROM review_log WHERE reviewed_at >= ?`, dayStart.Unix()).
		Scan(&st.ReviewsToday, &good); err != nil {
		return st, err
	}
	if st.ReviewsToday > 0 {
		st.AccuracyToday = float64(good.Int64) / float64(st.ReviewsToday)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM review_log`).Scan(&st.TotalReviews); err != nil {
		return st, err
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM retired`).Scan(&st.Retired); err != nil {
		return st, err
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM card_state WHERE state = ? AND card_id NOT IN (SELECT card_id FROM retired)`, fsrs.Review).Scan(&st.Learned); err != nil {
		return st, err
	}
	st.Learned += st.Retired
	// Streak: consecutive days with at least one review, counting back from today
	// (today may still be empty without breaking the streak).
	day := dayStart
	for i := 0; ; i++ {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM review_log WHERE reviewed_at >= ? AND reviewed_at < ?`,
			day.Unix(), day.AddDate(0, 0, 1).Unix()).Scan(&n); err != nil {
			return st, err
		}
		if n == 0 && i > 0 {
			break
		}
		if n > 0 {
			st.Streak++
		}
		day = day.AddDate(0, 0, -1)
	}
	return st, nil
}

// SaveQuiz stores a finished quiz.
func (s *Store) SaveQuiz(unit string, score, total int, now time.Time) error {
	_, err := s.db.Exec(`INSERT INTO quiz_result (unit, score, total, taken_at) VALUES (?, ?, ?, ?)`, unit, score, total, now.Unix())
	return err
}

// BestQuiz returns the best quiz score per unit as a fraction.
func (s *Store) BestQuiz() (map[string]float64, error) {
	rows, err := s.db.Query(`SELECT unit, MAX(CAST(score AS REAL) / total) FROM quiz_result WHERE total > 0 GROUP BY unit`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]float64{}
	for rows.Next() {
		var u string
		var f float64
		if err := rows.Scan(&u, &f); err != nil {
			return nil, err
		}
		out[u] = f
	}
	return out, rows.Err()
}

// Get reads a kv value.
func (s *Store) Get(key string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM kv WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// Set writes a kv value.
func (s *Store) Set(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO kv (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

func (s *Store) ids(query string, args ...any) ([]string, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// Retire marks a card as known: it leaves reviews, practice and quizzes. Its schedule
// is kept, so Unretire brings it back exactly where it was.
func (s *Store) Retire(id string, now time.Time) error {
	_, err := s.db.Exec(`INSERT INTO retired (card_id, retired_at) VALUES (?, ?) ON CONFLICT(card_id) DO NOTHING`, id, now.Unix())
	return err
}

// Unretire brings a retired card back.
func (s *Store) Unretire(id string) error {
	_, err := s.db.Exec(`DELETE FROM retired WHERE card_id = ?`, id)
	return err
}

// Retired returns the set of retired card IDs.
func (s *Store) Retired() (map[string]bool, error) {
	ids, err := s.ids(`SELECT card_id FROM retired ORDER BY retired_at DESC`)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}
