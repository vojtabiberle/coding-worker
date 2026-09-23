package worker

import (
	"coding-worker/store"
	"time"
)

func (a *App) phase(r *store.Run, phase string) error {
	now := time.Now().UTC()
	r.Phase = phase
	r.LastProgress = &now
	return a.Store.Save(r)
}

type citationError struct{ message string }

func (e *citationError) Error() string { return e.message }
