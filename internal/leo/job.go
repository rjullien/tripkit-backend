package leo

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	jobTTL        = 15 * time.Minute
	jobRunTimeout = 10 * time.Minute
)

// JobStatus is the lifecycle of a detached Léo run.
type JobStatus string

const (
	JobRunning JobStatus = "running"
	JobDone    JobStatus = "done"
	JobError   JobStatus = "error"
)

// LoggedEvent is one SSE frame stored on the job (reconnect replay).
type LoggedEvent struct {
	Seq   int
	Event string
	Data  StreamEvent
}

// RunFunc is Hermes (or a test stub). ctx is cancelled only on explicit Cancel,
// not when an HTTP subscriber disconnects.
type RunFunc func(ctx context.Context, emit EmitFunc) error

// Job is one user message → one Hermes run. Subscribers come and go.
type Job struct {
	ID         string
	User       string
	CreatedAt  time.Time
	FinishedAt time.Time

	mu     sync.Mutex
	status JobStatus
	events []LoggedEvent
	subs   []chan LoggedEvent
	cancel context.CancelFunc
	doneCh chan struct{}
}

func (j *Job) Status() JobStatus {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.status
}

// Hub holds in-memory jobs (single replica). Lock-phone reconnects hit the same pod.
type Hub struct {
	mu   sync.Mutex
	jobs map[string]*Job
}

func NewHub() *Hub {
	return &Hub{jobs: map[string]*Job{}}
}

// Start runs fn in the background. Disconnecting an HTTP subscriber does not cancel fn.
func (h *Hub) Start(user string, fn RunFunc) *Job {
	h.gc()
	ctx, cancel := context.WithTimeout(context.Background(), jobRunTimeout)
	j := &Job{
		ID:        uuid.NewString(),
		User:      user,
		CreatedAt: time.Now(),
		status:    JobRunning,
		cancel:    cancel,
		doneCh:    make(chan struct{}),
	}
	h.mu.Lock()
	h.jobs[j.ID] = j
	h.mu.Unlock()

	j.append("meta", StreamEvent{})

	go func() {
		defer cancel()
		defer close(j.doneCh)
		err := fn(ctx, func(event string, data StreamEvent) error {
			j.append(event, data)
			if event == "done" || event == "error" {
				return nil
			}
			return ctx.Err()
		})
		if j.Status() != JobRunning {
			return
		}
		if err != nil || ctx.Err() != nil {
			code := "leo_chat_failed"
			userErr := "Échec Léo. Réessaie."
			if ctx.Err() == context.Canceled {
				code = "cancelled"
				userErr = "Annulé."
			} else if ctx.Err() == context.DeadlineExceeded {
				code = "timeout"
				userErr = "Léo met trop longtemps. Réessaie ou Dashboard."
			}
			detail := ""
			if err != nil {
				detail = err.Error()
			} else {
				detail = ctx.Err().Error()
			}
			j.append("error", StreamEvent{Error: userErr, Code: code, Detail: detail})
			return
		}
		j.append("done", StreamEvent{})
	}()
	return j
}

func (h *Hub) Get(id string) *Job {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.jobs[id]
}

func (h *Hub) gc() {
	h.mu.Lock()
	defer h.mu.Unlock()
	cutoff := time.Now().Add(-jobTTL)
	for id, j := range h.jobs {
		if j.Status() == JobRunning {
			continue
		}
		j.mu.Lock()
		fin := j.FinishedAt
		j.mu.Unlock()
		if !fin.IsZero() && fin.Before(cutoff) {
			delete(h.jobs, id)
		}
	}
}

// Cancel stops Hermes. HTTP disconnect must NOT call this.
func (j *Job) Cancel() {
	if j.cancel != nil {
		j.cancel()
	}
}

func (j *Job) append(event string, data StreamEvent) {
	j.mu.Lock()
	seq := len(j.events) + 1
	data.Seq = seq
	data.JobID = j.ID
	ev := LoggedEvent{Seq: seq, Event: event, Data: data}
	j.events = append(j.events, ev)
	if event == "done" {
		j.status = JobDone
		j.FinishedAt = time.Now()
	}
	if event == "error" {
		j.status = JobError
		j.FinishedAt = time.Now()
	}
	subs := append([]chan LoggedEvent(nil), j.subs...)
	j.mu.Unlock()
	for _, sub := range subs {
		select {
		case sub <- ev:
		default:
			// Slow subscriber: skip live; reconnect reads the log.
		}
	}
}

// Subscribe returns backlog (seq > after) then a live channel. Call unsub on HTTP disconnect.
func (j *Job) Subscribe(after int) (backlog []LoggedEvent, live <-chan LoggedEvent, unsub func()) {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, ev := range j.events {
		if ev.Seq > after {
			backlog = append(backlog, ev)
		}
	}
	ch := make(chan LoggedEvent, 256)
	j.subs = append(j.subs, ch)
	var once sync.Once
	unsub = func() {
		once.Do(func() {
			j.mu.Lock()
			defer j.mu.Unlock()
			out := j.subs[:0]
			for _, s := range j.subs {
				if s != ch {
					out = append(out, s)
				}
			}
			j.subs = out
			// Do not close(ch): append may still hold a copy and send.
		})
	}
	return backlog, ch, unsub
}

func (j *Job) Done() <-chan struct{} { return j.doneCh }
