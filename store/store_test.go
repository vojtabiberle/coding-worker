package store

import (
	"coding-worker/config"
	"coding-worker/runner"
	"coding-worker/workspace"
	"sync"
	"testing"
	"time"
)

func TestPersistenceAndExperiments(t *testing.T) {
	dir := t.TempDir()
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	p := config.Profile{Engine: "opencode", Model: "test/model", Agent: "worker", MaxSteps: 3}
	c := config.Config{Profiles: map[string]config.Profile{"a": p, "b": p}}
	if e = s.StartExperiment("repo", "comparison", []string{"a", "b"}); e != nil {
		t.Fatal(e)
	}
	if e = s.StartExperiment("repo", "duplicate", []string{"a", "b"}); e == nil {
		t.Fatal("two active experiments")
	}
	for i, want := range []string{"a", "b", "a"} {
		r := Run{ID: ID(), Workspace: workspace.Workspace{Root: "work", Repository: "repo"}, Config: config.Resolved{Name: "b", Profile: p}, State: "running", Started: time.Now()}
		if e = s.Create(&r, c, false); e != nil {
			t.Fatal(e)
		}
		if r.Config.Name != want {
			t.Fatal(r.Config.Name)
		}
		now := time.Now()
		r.Finished = &now
		r.State = "completed"
		cost := 0.5
		r.Iterations = []Iteration{{Number: 1, Started: r.Started, Finished: &now, Result: runner.Result{Metrics: runner.Metrics{Cost: &cost}}}}
		if i == 1 {
			r.Iterations[0].Result.Metrics.Cost = nil
		}
		if e = s.Save(&r); e != nil {
			t.Fatal(e)
		}
		passed := true
		if e = s.Review(Review{RunID: r.ID, Verdict: "accepted", Reviewer: "human", TestsPassed: &passed}); e != nil {
			t.Fatal(e)
		}
	}
	report, e := s.Report("repo", "comparison")
	if e != nil {
		t.Fatal(e)
	}
	if report[0].Tasks != 2 || report[0].Accepted != 2 || *report[0].Cost.Value != 0.5 || report[0].Cost.N != 2 || report[1].Cost.Value != nil || report[1].CostAccepted.Value != nil {
		t.Fatalf("%+v", report)
	}
	if e = s.StopExperiment("repo"); e != nil {
		t.Fatal(e)
	}
	s2, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s2.Close()
	runs, e := s2.List()
	if e != nil || len(runs) != 3 {
		t.Fatal(runs, e)
	}
	var count int
	s.DB.QueryRow("SELECT count(*) FROM iterations").Scan(&count)
	if count != 3 {
		t.Fatal(count)
	}
}
func TestConcurrentAssignment(t *testing.T) {
	dir := t.TempDir()
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	s2, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s2.Close()
	p := config.Profile{Engine: "opencode", Model: "test/model", Agent: "worker", MaxSteps: 1}
	c := config.Config{Profiles: map[string]config.Profile{"a": p, "b": p}}
	if e = s.StartExperiment("repo", "round", []string{"a", "b"}); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	names := make(chan string, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			db := s
			if i%2 == 0 {
				db = s2
			}
			r := Run{ID: ID(), Workspace: workspace.Workspace{Repository: "repo"}, State: "running"}
			if e := db.Create(&r, c, false); e != nil {
				errs <- e
			}
			names <- r.Config.Name
		}(i)
	}
	wg.Wait()
	close(errs)
	close(names)
	for e := range errs {
		t.Fatal(e)
	}
	counts := map[string]int{}
	for n := range names {
		counts[n]++
	}
	if counts["a"] != 10 || counts["b"] != 10 {
		t.Fatal(counts)
	}
}
