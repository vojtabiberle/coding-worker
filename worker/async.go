package worker

import (
	"context"
	"fmt"
	"os"
	"sync"
	"syscall"
)

type asyncKey struct{}

// Async detaches execution from an individual MCP request, not from server lifetime.
func Async(ctx context.Context) context.Context { return context.WithValue(ctx, asyncKey{}, true) }

type jobs struct {
	mu      sync.Mutex
	wg      sync.WaitGroup
	closing bool
	cancels map[*os.File]context.CancelFunc
}

func (a *App) launch(lock *os.File, run func(context.Context, *os.File)) error {
	a.jobs.mu.Lock()
	defer a.jobs.mu.Unlock()
	if a.jobs.closing {
		return fmt.Errorf("server is shutting down")
	}
	fd, _, errno := syscall.Syscall(syscall.SYS_FCNTL, lock.Fd(), syscall.F_DUPFD_CLOEXEC, 0)
	if errno != 0 {
		return errno
	}
	held := os.NewFile(fd, lock.Name())
	ctx, cancel := context.WithCancel(context.Background())
	if a.jobs.cancels == nil {
		a.jobs.cancels = make(map[*os.File]context.CancelFunc)
	}
	a.jobs.cancels[held] = cancel
	a.jobs.wg.Add(1)
	go func() {
		defer a.jobs.wg.Done()
		defer held.Close()
		defer cancel()
		defer func() { a.jobs.mu.Lock(); delete(a.jobs.cancels, held); a.jobs.mu.Unlock() }()
		run(ctx, held)
	}()
	return nil
}

// StopJobs cancels active work and waits for final persistence before closing SQLite.
func (a *App) StopJobs() {
	a.jobs.mu.Lock()
	a.jobs.closing = true
	for _, cancel := range a.jobs.cancels {
		cancel()
	}
	a.jobs.mu.Unlock()
	a.jobs.wg.Wait()
}
