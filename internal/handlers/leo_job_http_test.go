package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rjullien/tripkit-backend/internal/database"
	"github.com/rjullien/tripkit-backend/internal/leo"
	"github.com/rjullien/tripkit-backend/internal/middleware"
)

type sseRec struct {
	hdr  http.Header
	code int
	mu   sync.Mutex
	buf  bytes.Buffer
	n    chan struct{}
}

func newSSERec() *sseRec {
	return &sseRec{hdr: make(http.Header), n: make(chan struct{}, 8)}
}

func (s *sseRec) Header() http.Header { return s.hdr }
func (s *sseRec) WriteHeader(c int)   { s.code = c }
func (s *sseRec) Flush()              {}
func (s *sseRec) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, err := s.buf.Write(p)
	select {
	case s.n <- struct{}{}:
	default:
	}
	return n, err
}
func (s *sseRec) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func leoTestRouter(t *testing.T) (*Handler, http.Handler) {
	t.Helper()
	db, err := database.InitMemory()
	if err != nil {
		t.Fatal(err)
	}
	h := New(db)
	r := chi.NewRouter()
	r.Use(middleware.UserIdentity)
	r.Post("/leo/chat/stream", h.LeoChatStream)
	r.Get("/leo/jobs/{jobId}/stream", h.LeoJobStream)
	r.Post("/leo/jobs/{jobId}/cancel", h.LeoJobCancel)
	return h, r
}

func leoPOST(user, body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/leo/chat/stream", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Remote-User", user)
	return req
}

func waitSSE(t *testing.T, rec *sseRec, substr string, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if strings.Contains(rec.String(), substr) {
			return
		}
		select {
		case <-rec.n:
		case <-time.After(20 * time.Millisecond):
		}
	}
	t.Fatalf("timeout waiting for %q in:\n%s", substr, rec.String())
}

func parseSSEJobID(raw string) string {
	for _, block := range strings.Split(raw, "\n\n") {
		var data string
		for _, line := range strings.Split(block, "\n") {
			if strings.HasPrefix(line, "data:") {
				data = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			}
		}
		if data == "" {
			continue
		}
		var m map[string]any
		if json.Unmarshal([]byte(data), &m) != nil {
			continue
		}
		if id, ok := m["jobId"].(string); ok && id != "" {
			return id
		}
	}
	return ""
}

func maxSeq(raw string) int {
	max := 0
	for _, block := range strings.Split(raw, "\n\n") {
		var data string
		for _, line := range strings.Split(block, "\n") {
			if strings.HasPrefix(line, "data:") {
				data = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			}
		}
		if data == "" {
			continue
		}
		var m map[string]any
		if json.Unmarshal([]byte(data), &m) != nil {
			continue
		}
		switch v := m["seq"].(type) {
		case float64:
			if int(v) > max {
				max = int(v)
			}
		}
	}
	return max
}

const leoChatBody = `{"messages":[{"role":"user","content":"ping"}]}`

func TestLeoChatStream_LiveProgressWhenConnected(t *testing.T) {
	h, r := leoTestRouter(t)
	h.leoRun = func(ctx context.Context, pctx leo.PromptContext, req leo.ChatRequest, emit leo.EmitFunc) error {
		if err := emit("delta", leo.StreamEvent{Text: "Hel"}); err != nil {
			return err
		}
		if err := emit("tool", leo.StreamEvent{Tool: map[string]string{"name": "read_file"}}); err != nil {
			return err
		}
		return emit("done", leo.StreamEvent{Reply: "Hello"})
	}

	w := httptest.NewRecorder()
	r.ServeHTTP(w, leoPOST("rene", leoChatBody))
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "event: meta") {
		t.Fatalf("missing meta:\n%s", body)
	}
	if !strings.Contains(body, `"text":"Hel"`) {
		t.Fatalf("missing live delta:\n%s", body)
	}
	if !strings.Contains(body, "event: tool") {
		t.Fatalf("missing tool:\n%s", body)
	}
	if !strings.Contains(body, `"reply":"Hello"`) {
		t.Fatalf("missing done:\n%s", body)
	}
	if parseSSEJobID(body) == "" {
		t.Fatal("missing jobId")
	}
}

func TestLeoJob_DisconnectDoesNotCancelRun(t *testing.T) {
	h, r := leoTestRouter(t)
	started := make(chan struct{})
	gate := make(chan struct{})
	var runCancelled bool
	h.leoRun = func(ctx context.Context, pctx leo.PromptContext, req leo.ChatRequest, emit leo.EmitFunc) error {
		close(started)
		if err := emit("delta", leo.StreamEvent{Text: "Hel"}); err != nil {
			return err
		}
		<-gate
		if ctx.Err() != nil {
			runCancelled = true
			return ctx.Err()
		}
		return emit("done", leo.StreamEvent{Reply: "Hello"})
	}

	ctx, cancel := context.WithCancel(context.Background())
	req := leoPOST("rene", leoChatBody).WithContext(ctx)
	rec := newSSERec()
	done := make(chan struct{})
	go func() {
		r.ServeHTTP(rec, req)
		close(done)
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("run did not start")
	}
	waitSSE(t, rec, `"Hel"`, 2*time.Second)
	jobID := parseSSEJobID(rec.String())
	if jobID == "" {
		t.Fatalf("no jobId in %s", rec.String())
	}
	after := maxSeq(rec.String())
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not return after disconnect")
	}

	close(gate)
	job := h.leoJobs.Get(jobID)
	if job == nil {
		t.Fatal("job gone after disconnect")
	}
	select {
	case <-job.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("job did not finish")
	}
	if runCancelled {
		t.Fatal("Hermes run cancelled after HTTP disconnect")
	}
	if job.Status() != leo.JobDone {
		t.Fatalf("status=%s", job.Status())
	}

	req2 := httptest.NewRequest(http.MethodGet, "/leo/jobs/"+jobID+"/stream?after="+strconv.Itoa(after), nil)
	req2.Header.Set("Remote-User", "rene")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req2)
	got := w.Body.String()
	if strings.Contains(got, `"text":"Hel"`) {
		t.Fatalf("replayed already-seen delta:\n%s", got)
	}
	if !strings.Contains(got, `"reply":"Hello"`) {
		t.Fatalf("reconnect missing done:\n%s", got)
	}
}

func TestLeoJob_CancelStopsRun(t *testing.T) {
	h, r := leoTestRouter(t)
	entered := make(chan struct{})
	h.leoRun = func(ctx context.Context, pctx leo.PromptContext, req leo.ChatRequest, emit leo.EmitFunc) error {
		close(entered)
		<-ctx.Done()
		return ctx.Err()
	}

	rec := newSSERec()
	go r.ServeHTTP(rec, leoPOST("rene", leoChatBody))
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("run did not start")
	}
	waitSSE(t, rec, "event: meta", 2*time.Second)
	jobID := parseSSEJobID(rec.String())

	req := httptest.NewRequest(http.MethodPost, "/leo/jobs/"+jobID+"/cancel", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Remote-User", "rene")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("cancel status=%d %s", w.Code, w.Body.String())
	}

	job := h.leoJobs.Get(jobID)
	select {
	case <-job.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("cancel did not finish job")
	}
	if job.Status() != leo.JobError {
		t.Fatalf("status=%s", job.Status())
	}
}

func TestLeoJob_Unknown404(t *testing.T) {
	_, r := leoTestRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/leo/jobs/nope/stream", nil)
	req.Header.Set("Remote-User", "rene")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 404 {
		t.Fatalf("status=%d %s", w.Code, w.Body.String())
	}
}

func TestLeoJob_WrongUser403(t *testing.T) {
	h, r := leoTestRouter(t)
	gate := make(chan struct{})
	h.leoRun = func(ctx context.Context, pctx leo.PromptContext, req leo.ChatRequest, emit leo.EmitFunc) error {
		<-gate
		return emit("done", leo.StreamEvent{Reply: "x"})
	}
	rec := newSSERec()
	go r.ServeHTTP(rec, leoPOST("rene", leoChatBody))
	waitSSE(t, rec, "event: meta", 2*time.Second)
	jobID := parseSSEJobID(rec.String())

	req := httptest.NewRequest(http.MethodGet, "/leo/jobs/"+jobID+"/stream", nil)
	req.Header.Set("Remote-User", "nadia")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatalf("status=%d %s", w.Code, w.Body.String())
	}
	close(gate)
}

func TestLeoStatus_IncludesAllowlist(t *testing.T) {
	h, _ := leoTestRouter(t)
	r := chi.NewRouter()
	r.Get("/leo/status", h.LeoStatus)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/leo/status", nil))
	if w.Code != 200 {
		t.Fatalf("status=%d %s", w.Code, w.Body.String())
	}
	var st leo.Status
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st.DefaultModel == "" || len(st.Models) < 2 {
		t.Fatalf("status=%s", w.Body.String())
	}
	found := false
	for _, m := range st.Models {
		if m.ID == "opencode-go/deepseek-v4-pro" && m.Label != "" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing pro in %s", w.Body.String())
	}
}

func TestLeoChatStream_PassesRequestedModel(t *testing.T) {
	h, r := leoTestRouter(t)
	var got string
	h.leoRun = func(ctx context.Context, pctx leo.PromptContext, req leo.ChatRequest, emit leo.EmitFunc) error {
		got = req.Model
		return emit("done", leo.StreamEvent{Reply: "ok", Model: req.Model})
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, leoPOST("rene", `{"messages":[{"role":"user","content":"ping"}],"model":"opencode-go/deepseek-v4-pro"}`))
	if w.Code != 200 {
		t.Fatalf("status=%d %s", w.Code, w.Body.String())
	}
	if got != "opencode-go/deepseek-v4-pro" {
		t.Fatalf("run got model=%q", got)
	}
}
