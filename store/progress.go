package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// Progress projects metadata only; reports, snapshots and logs stay in SQLite.
type Progress struct {
	Root              string     `json:"root"`
	State             string     `json:"state"`
	Iterations        int        `json:"iterations"`
	Phase             string     `json:"phase"`
	LastProgress      *time.Time `json:"last_progress"`
	IterationState    string     `json:"iteration_state"`
	IterationFinished *time.Time `json:"iteration_finished"`
	IterationError    bool       `json:"iteration_error"`
}

func (s *Store) Progress(ctx context.Context, id string, iteration int) (Progress, error) {
	path := "$.iterations[#-1]"
	if iteration > 0 {
		path = fmt.Sprintf("$.iterations[%d]", iteration-1)
	}
	var raw string
	e := s.DB.QueryRowContext(ctx, `SELECT json_object(
 'root',worktree,'state',state,
 'iterations',coalesce(json_array_length(data,'$.iterations'),0),
 'phase',coalesce(json_extract(data,'$.phase'),''),'last_progress',json_extract(data,'$.last_progress'),
 'iteration_state',coalesce(json_extract(data,?),''),'iteration_finished',json_extract(data,?),
 'iteration_error',json(CASE WHEN coalesce(json_extract(data,?),'') != '' THEN 'true' ELSE 'false' END)
 ) FROM runs WHERE id=?`, path+".state", path+".finished_at", path+".error", id).Scan(&raw)
	var p Progress
	if e != nil {
		return p, fmt.Errorf("run %q: %w", id, e)
	}
	e = json.Unmarshal([]byte(raw), &p)
	return p, e
}
