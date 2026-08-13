package leo

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestHub_DisconnectDoesNotCancelRun(t *testing.T) {
	h := NewHub()
	var cancelled atomic.Bool
	started := make(chan struct{})
	job := h.Start("rene", func(ctx context.Context, emit EmitFunc) error {
		close(started)
		if err := emit("delta", StreamEvent{Text: "A"}); err != nil {
			return err
		}
		time.Sleep(60 * time.Millisecond)
		if ctx.Err() != nil {
			cancelled.Store(true)
			return ctx.Err()
		}
		return emit("done", StreamEvent{Reply: "A"})
	})

	<-started
	_, live, unsub := job.Subscribe(0)
	unsub() // phone locked — drop SSE
	select {
	case <-live:
	default:
	}

	select {
	case <-job.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("job did not finish")
	}
	if cancelled.Load() {
		t.Fatal("Hermes run was cancelled after subscriber disconnect")
	}
	if job.Status() != JobDone {
		t.Fatalf("status=%s", job.Status())
	}
}

func TestHub_ReconnectAfterGetsRemainder(t *testing.T) {
	h := NewHub()
	gate := make(chan struct{})
	first := make(chan struct{})
	job := h.Start("rene", func(ctx context.Context, emit EmitFunc) error {
		_ = emit("delta", StreamEvent{Text: "Hel"})
		close(first)
		<-gate
		_ = emit("delta", StreamEvent{Text: "lo"})
		return emit("done", StreamEvent{Reply: "Hello"})
	})
	<-first

	backlog, live, unsub := job.Subscribe(0)
	var firstDelta string
	var firstDeltaSeq int
	for _, ev := range backlog {
		if ev.Event == "delta" {
			firstDelta = ev.Data.Text
			firstDeltaSeq = ev.Seq
			break
		}
	}
	if firstDelta != "Hel" {
		t.Fatalf("backlog=%+v", backlog)
	}
	unsub()
	close(gate)
	<-job.Done()

	rest, _, unsub2 := job.Subscribe(firstDeltaSeq) // skip meta + first delta
	defer unsub2()
	var texts []string
	for _, ev := range rest {
		if ev.Event == "delta" {
			texts = append(texts, ev.Data.Text)
		}
	}
	if len(texts) != 1 || texts[0] != "lo" {
		t.Fatalf("reconnect texts=%v (live=%v)", texts, live)
	}
	if rest[len(rest)-1].Event != "done" {
		t.Fatalf("last=%s", rest[len(rest)-1].Event)
	}
}

func TestHub_CancelStopsRun(t *testing.T) {
	h := NewHub()
	entered := make(chan struct{})
	job := h.Start("rene", func(ctx context.Context, emit EmitFunc) error {
		close(entered)
		<-ctx.Done()
		return ctx.Err()
	})
	<-entered
	job.Cancel()
	select {
	case <-job.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("cancel did not finish job")
	}
	if job.Status() != JobError {
		t.Fatalf("status=%s want error (cancelled)", job.Status())
	}
}

func TestHub_GetUnknown(t *testing.T) {
	if NewHub().Get("nope") != nil {
		t.Fatal("expected nil")
	}
}

func TestHub_MetaIsFirstEvent(t *testing.T) {
	h := NewHub()
	job := h.Start("rene", func(ctx context.Context, emit EmitFunc) error {
		return emit("done", StreamEvent{Reply: "ok"})
	})
	<-job.Done()
	backlog, _, unsub := job.Subscribe(0)
	defer unsub()
	if len(backlog) < 1 || backlog[0].Event != "meta" {
		t.Fatalf("first=%+v", backlog)
	}
	if backlog[0].Data.JobID != job.ID || backlog[0].Seq != 1 {
		t.Fatalf("meta=%+v", backlog[0])
	}
}
