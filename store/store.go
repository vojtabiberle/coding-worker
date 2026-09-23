package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"coding-worker/config"
	"coding-worker/runner"
	"coding-worker/workspace"
	_ "github.com/mattn/go-sqlite3"
)

type Run struct {
	Operation  string              `json:"operation,omitempty"`
	ID         string              `json:"run_id"`
	Origin     string              `json:"origin,omitempty"`
	Workspace  workspace.Workspace `json:"workspace"`
	Base       string              `json:"base_sha"`
	Config     config.Resolved     `json:"config"`
	Provider   string              `json:"provider"`
	Session    string              `json:"opencode_session_id"`
	Started    time.Time           `json:"started_at"`
	Finished   *time.Time          `json:"finished_at"`
	State      string              `json:"state"`
	Iterations []Iteration         `json:"iterations"`
}
type Iteration struct {
	Number   int                `json:"number"`
	Started  time.Time          `json:"started_at"`
	Finished *time.Time         `json:"finished_at"`
	Before   workspace.Snapshot `json:"before"`
	After    workspace.Snapshot `json:"after"`
	Result   runner.Result      `json:"result"`
	Error    string             `json:"error,omitempty"`
}
type Review struct {
	RunID             string `json:"run_id"`
	Verdict           string `json:"verdict" jsonschema:"External review verdict: accepted, changes_requested, or rejected. Use accepted for approval, not approved."`
	Blocker           int    `json:"blocker"`
	Major             int    `json:"major"`
	Minor             int    `json:"minor"`
	Reviewer          string `json:"reviewer"`
	Notes             string `json:"notes"`
	HiddenSuccess     *bool  `json:"hidden_e2e_success,omitempty"`
	HumanIntervention *bool  `json:"human_intervention,omitempty"`
	TestsPassed       *bool  `json:"tests_passed,omitempty"`
	Iteration         int    `json:"iteration,omitempty"`
}
type Store struct {
	DB  *sql.DB
	Dir string
}

func ID() string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func Open(dir string) (*Store, error) {
	if !filepath.IsAbs(dir) {
		return nil, fmt.Errorf("data directory must be absolute")
	}
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	path := filepath.Join(dir, "coding-worker.db")
	f, e := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	f.Close()
	db, e := sql.Open("sqlite3", (&url.URL{Scheme: "file", Path: path, RawQuery: "_busy_timeout=10000&_journal_mode=WAL&_foreign_keys=on&_txlock=immediate"}).String())
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	_, e = db.Exec(`CREATE TABLE IF NOT EXISTS runs(id TEXT PRIMARY KEY, repository TEXT NOT NULL, worktree TEXT NOT NULL, state TEXT NOT NULL, data TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS iterations(run_id TEXT NOT NULL REFERENCES runs(id), number INTEGER NOT NULL, data TEXT NOT NULL, PRIMARY KEY(run_id,number));
 CREATE TABLE IF NOT EXISTS events(id INTEGER PRIMARY KEY, run_id TEXT NOT NULL REFERENCES runs(id), iteration INTEGER NOT NULL, kind TEXT NOT NULL, at TEXT NOT NULL, data TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS experiments(id INTEGER PRIMARY KEY, repository TEXT NOT NULL, name TEXT NOT NULL, profiles TEXT NOT NULL, next INTEGER NOT NULL DEFAULT 0, active INTEGER NOT NULL DEFAULT 1, UNIQUE(repository,name));
 CREATE UNIQUE INDEX IF NOT EXISTS active_experiment ON experiments(repository) WHERE active=1;
 CREATE TABLE IF NOT EXISTS experiment_assignments(run_id TEXT PRIMARY KEY REFERENCES runs(id), experiment_id INTEGER NOT NULL REFERENCES experiments(id), profile TEXT NOT NULL);
 CREATE INDEX IF NOT EXISTS runs_worktree ON runs(worktree,state);`)
	if e != nil {
		db.Close()
		return nil, e
	}
	return &Store{db, dir}, nil
}
func (s *Store) Close() error { return s.DB.Close() }
func encode(v any) string {
	b, e := json.Marshal(v)
	if e != nil {
		panic(e)
	}
	return string(b)
}
func (s *Store) Create(r *Run, c config.Config, explicit bool) error {
	tx, e := s.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var exp, next int
	var profiles string
	if !explicit {
		e = tx.QueryRow("SELECT id,profiles,next FROM experiments WHERE repository=? AND active=1", r.Workspace.Repository).Scan(&exp, &profiles, &next)
		if e != nil && e != sql.ErrNoRows {
			return e
		}
		if e == nil {
			var names []string
			if e = json.Unmarshal([]byte(profiles), &names); e != nil {
				return e
			}
			if len(names) == 0 {
				return fmt.Errorf("experiment has no profiles")
			}
			name := names[next%len(names)]
			p, ok := c.Profiles[name]
			if !ok {
				return fmt.Errorf("experiment profile %q no longer exists", name)
			}
			if e = p.Validate(); e != nil {
				return e
			}
			r.Config = config.Resolved{Name: name, Source: "experiment", Profile: p}
			if _, e = tx.Exec("UPDATE experiments SET next=next+1 WHERE id=?", exp); e != nil {
				return e
			}
		}
	}
	r.Provider = strings.SplitN(r.Config.Profile.Model, "/", 2)[0]
	if _, e = tx.Exec("INSERT INTO runs VALUES(?,?,?,?,?)", r.ID, r.Workspace.Repository, r.Workspace.Root, r.State, encode(r)); e != nil {
		return e
	}
	if exp != 0 {
		if _, e = tx.Exec("INSERT INTO experiment_assignments VALUES(?,?,?)", r.ID, exp, r.Config.Name); e != nil {
			return e
		}
	}
	return tx.Commit()
}
func (s *Store) Save(r *Run) error {
	tx, e := s.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	res, e := tx.Exec("UPDATE runs SET state=?,data=? WHERE id=?", r.State, encode(r), r.ID)
	if e != nil {
		return e
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return fmt.Errorf("run not found")
	}
	for _, it := range r.Iterations {
		if _, e = tx.Exec("INSERT INTO iterations VALUES(?,?,?) ON CONFLICT(run_id,number) DO UPDATE SET data=excluded.data", r.ID, it.Number, encode(it)); e != nil {
			return e
		}
	}
	return tx.Commit()
}
func (s *Store) Get(id string) (Run, error) {
	var r Run
	var b string
	e := s.DB.QueryRow("SELECT data FROM runs WHERE id=?", id).Scan(&b)
	if e != nil {
		return r, fmt.Errorf("run %q: %w", id, e)
	}
	e = json.Unmarshal([]byte(b), &r)
	return r, e
}
func (s *Store) List() ([]Run, error) {
	rows, e := s.DB.Query("SELECT data FROM runs ORDER BY rowid DESC LIMIT 1000")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Run{}
	for rows.Next() {
		var b string
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		var r Run
		if e = json.Unmarshal([]byte(b), &r); e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *Store) Event(id string, iteration int, kind string, data any) error {
	_, e := s.DB.Exec("INSERT INTO events(run_id,iteration,kind,at,data) VALUES(?,?,?,?,?)", id, iteration, kind, time.Now().UTC().Format(time.RFC3339Nano), encode(data))
	return e
}
func (s *Store) Review(v Review) error {
	if v.Verdict != "accepted" && v.Verdict != "changes_requested" && v.Verdict != "rejected" {
		return fmt.Errorf("invalid review verdict %q: expected accepted, changes_requested, or rejected", v.Verdict)
	}
	if v.Blocker < 0 || v.Major < 0 || v.Minor < 0 || strings.TrimSpace(v.Reviewer) == "" {
		return fmt.Errorf("reviewer required and counts must be nonnegative")
	}
	r, e := s.Get(v.RunID)
	if e != nil {
		return e
	}
	if r.State == "running" || len(r.Iterations) == 0 {
		return fmt.Errorf("cannot review an active or empty run")
	}
	v.Iteration = len(r.Iterations)
	return s.Event(r.ID, v.Iteration, "review", v)
}
func (s *Store) Reviews(id string) ([]Review, error) {
	rows, e := s.DB.Query("SELECT data FROM events WHERE run_id=? AND kind='review' ORDER BY id", id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Review{}
	for rows.Next() {
		var b string
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		var v Review
		if e = json.Unmarshal([]byte(b), &v); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Recover is called only while holding the physical worktree lock.
func (s *Store) Recover(root string) error {
	rows, e := s.DB.Query("SELECT data FROM runs WHERE worktree=? AND state='running'", root)
	if e != nil {
		return e
	}
	var runs []Run
	for rows.Next() {
		var b string
		if e = rows.Scan(&b); e != nil {
			rows.Close()
			return e
		}
		var r Run
		if e = json.Unmarshal([]byte(b), &r); e != nil {
			rows.Close()
			return e
		}
		runs = append(runs, r)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, r := range runs {
		r.State = "interrupted"
		r.Finished = nil
		if len(r.Iterations) > 0 {
			i := &r.Iterations[len(r.Iterations)-1]
			i.Finished = nil
			i.Error = "worker process ended without finalization"
		}
		if e = s.Save(&r); e != nil {
			return e
		}
	}
	return nil
}

type Experiment struct {
	Name     string   `json:"name"`
	Profiles []string `json:"profiles"`
	Assigned int      `json:"assigned"`
	Active   bool     `json:"active"`
}

func (s *Store) StartExperiment(repo, name string, profiles []string) error {
	if strings.TrimSpace(name) == "" || len(profiles) < 2 {
		return fmt.Errorf("name and at least two profiles required")
	}
	_, e := s.DB.Exec("INSERT INTO experiments(repository,name,profiles) VALUES(?,?,?)", repo, name, encode(profiles))
	return e
}
func (s *Store) StopExperiment(repo string) error {
	_, e := s.DB.Exec("UPDATE experiments SET active=0 WHERE repository=? AND active=1", repo)
	return e
}
func (s *Store) Experiments(repo string) ([]Experiment, error) {
	rows, e := s.DB.Query("SELECT name,profiles,next,active FROM experiments WHERE repository=? ORDER BY id DESC", repo)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Experiment{}
	for rows.Next() {
		var v Experiment
		var b string
		if e = rows.Scan(&v.Name, &b, &v.Assigned, &v.Active); e != nil {
			return nil, e
		}
		if e = json.Unmarshal([]byte(b), &v.Profiles); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

type Sample struct {
	Value *float64 `json:"value"`
	N     int      `json:"n"`
}

func sample(sum float64, n int) Sample {
	if n == 0 {
		return Sample{}
	}
	v := sum / float64(n)
	return Sample{&v, n}
}

type ProfileReport struct {
	Profile      string `json:"profile"`
	Tasks        int    `json:"tasks"`
	Accepted     int    `json:"accepted"`
	FirstPass    Sample `json:"first_pass_acceptance_rate"`
	Reviews      Sample `json:"mean_review_iterations_to_acceptance"`
	Failure      Sample `json:"worker_failure_rate"`
	Tests        Sample `json:"test_pass_rate"`
	Duration     Sample `json:"mean_duration_seconds"`
	Cost         Sample `json:"mean_worker_cost"`
	CostAccepted Sample `json:"cost_per_accepted_task"`
}

func (s *Store) Report(repo, name string) ([]ProfileReport, error) {
	var eid int
	var ps string
	if e := s.DB.QueryRow("SELECT id,profiles FROM experiments WHERE repository=? AND name=?", repo, name).Scan(&eid, &ps); e != nil {
		return nil, e
	}
	var profiles []string
	if e := json.Unmarshal([]byte(ps), &profiles); e != nil {
		return nil, e
	}
	rows, e := s.DB.Query("SELECT r.data FROM runs r JOIN experiment_assignments a ON r.id=a.run_id WHERE a.experiment_id=?", eid)
	if e != nil {
		return nil, e
	}
	var runs []Run
	for rows.Next() {
		var b string
		if e = rows.Scan(&b); e != nil {
			rows.Close()
			return nil, e
		}
		var r Run
		if e = json.Unmarshal([]byte(b), &r); e != nil {
			rows.Close()
			return nil, e
		}
		runs = append(runs, r)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	out := []ProfileReport{}
	for _, p := range profiles {
		v := ProfileReport{Profile: p}
		var first, reviewSum, fail, tests, duration, cost float64
		var reviewed, complete, testN, costN, accepted, durationN int
		for _, r := range runs {
			if r.Config.Name != p {
				continue
			}
			v.Tasks++
			if r.State == "running" {
				continue
			}
			complete++
			if r.State != "completed" {
				fail++
			}
			durationKnown := len(r.Iterations) > 0
			var runDuration float64
			for _, it := range r.Iterations {
				if it.Finished == nil {
					durationKnown = false
				} else {
					runDuration += it.Finished.Sub(it.Started).Seconds()
				}
			}
			if durationKnown {
				durationN++
				duration += runDuration
			}

			known := len(r.Iterations) > 0
			var rc float64
			for _, it := range r.Iterations {
				if it.Result.Metrics.Cost == nil {
					known = false
				} else {
					rc += *it.Result.Metrics.Cost
				}
			}
			if known {
				costN++
				cost += rc
			}
			reviews, e := s.Reviews(r.ID)
			if e != nil {
				return nil, e
			}
			if len(reviews) > 0 {
				last := reviews[len(reviews)-1]
				if last.Iteration == len(r.Iterations) {
					reviewed++
					if last.Verdict == "accepted" {
						accepted++
						reviewSum += float64(last.Iteration - 1)
						if last.Iteration == 1 {
							first++
						}
					}
					if last.TestsPassed != nil {
						testN++
						if *last.TestsPassed {
							tests++
						}
					}
				}
			}
		}
		v.Accepted = accepted
		v.FirstPass = sample(first, reviewed)
		v.Reviews = sample(reviewSum, accepted)
		v.Failure = sample(fail, complete)
		v.Tests = sample(tests, testN)
		v.Duration = sample(duration, durationN)
		v.Cost = sample(cost, costN)
		if costN == complete {
			v.CostAccepted = sample(cost, accepted)
		}
		out = append(out, v)
	}
	return out, nil
}
