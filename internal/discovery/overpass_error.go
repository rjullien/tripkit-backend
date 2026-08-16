package discovery

import (
	"errors"
	"net"
	"strconv"
	"strings"
	"time"
)

// OverpassError is a single failed call to one Overpass endpoint. It carries
// what the retry loop needs to decide: which endpoint failed, whether the
// failure is worth retrying, and how long the server asked us to wait.
//
// The distinction matters for the caller too: internal/nuisance turns a
// rate-limited failure and an unreachable-endpoint failure into different
// French wordings, so "Donnée indisponible" finally says *why*.
type OverpassError struct {
	Endpoint   string
	Status     int           // HTTP status, 0 for transport errors
	RetryAfter time.Duration // from the Retry-After header, 0 if absent
	Transient  bool          // retrying (here or on a mirror) may succeed
	Remark     string        // Overpass "remark" field, when the body carried one
	Err        error
}

func (e *OverpassError) Error() string {
	if e == nil {
		return "<nil>"
	}
	var b strings.Builder
	b.WriteString("overpass ")
	if e.Endpoint != "" {
		b.WriteString(shortEndpoint(e.Endpoint))
		b.WriteString(" ")
	}
	if e.Status > 0 {
		b.WriteString("HTTP ")
		b.WriteString(strconv.Itoa(e.Status))
	}
	if e.Remark != "" {
		if e.Status > 0 {
			b.WriteString(" ")
		}
		b.WriteString("remark: ")
		b.WriteString(truncate(e.Remark, 160))
	}
	if e.Err != nil {
		if e.Status > 0 || e.Remark != "" {
			b.WriteString(": ")
		}
		b.WriteString(e.Err.Error())
	}
	return b.String()
}

func (e *OverpassError) Unwrap() error { return e.Err }

// IsTransient reports whether err is a transient Overpass failure: rate limit,
// server error, server-side query timeout, network hiccup, or a body that is
// not the JSON we asked for. Anything else (notably HTTP 400, "your query is
// wrong") is deterministic and must not be retried.
func IsTransient(err error) bool {
	var oe *OverpassError
	if errors.As(err, &oe) {
		return oe.Transient
	}
	return false
}

// IsRateLimited reports whether err is Overpass shedding load (HTTP 429, or a
// remark about too many requests / slots).
func IsRateLimited(err error) bool {
	var oe *OverpassError
	if !errors.As(err, &oe) {
		return false
	}
	if oe.Status == 429 {
		return true
	}
	r := strings.ToLower(oe.Remark)
	return strings.Contains(r, "too many") || strings.Contains(r, "slot")
}

// IsTimeout reports whether err is a timeout, on either side: our HTTP deadline
// (or context deadline) or the server aborting the query (504, "query timed
// out" remark).
func IsTimeout(err error) bool {
	var oe *OverpassError
	if !errors.As(err, &oe) {
		return false
	}
	if oe.Status == 504 || oe.Status == 408 {
		return true
	}
	if strings.Contains(strings.ToLower(oe.Remark), "timed out") {
		return true
	}
	var nerr net.Error
	if errors.As(oe.Err, &nerr) && nerr.Timeout() {
		return true
	}
	return errors.Is(oe.Err, errQueryDeadline)
}

// errQueryDeadline is returned when our own budget ran out before an attempt
// could be made. It is not a server failure, but it *is* a timeout.
var errQueryDeadline = errors.New("query budget exhausted")

// statusIsTransient classifies an HTTP status. Only 400 is treated as our own
// fault (malformed Overpass QL); every other non-200 is endpoint trouble worth
// retrying elsewhere, including 403 (banned IP) and 404 (mirror with a
// different path), which a mirror rotation can fix.
func statusIsTransient(status int) bool {
	switch status {
	case 200:
		return false
	case 400:
		return false
	default:
		return true
	}
}

// parseRetryAfter reads the Retry-After header in its delay-seconds form,
// which is what Overpass sends. HTTP-date form is ignored (0 = use backoff).
func parseRetryAfter(raw string) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	secs, err := strconv.Atoi(raw)
	if err != nil || secs <= 0 {
		return 0
	}
	return time.Duration(secs) * time.Second
}

// remarkIsFailure reports whether an Overpass "remark" describes a failure.
// Overpass answers HTTP 200 with valid JSON, an empty element list and a
// remark when a query times out or is refused. Parsing that as "nothing
// nearby" is the fail-open bug this repo already fixed once (green verdict on
// a dead Overpass), so it is treated as an error instead.
func remarkIsFailure(remark string) bool {
	r := strings.ToLower(strings.TrimSpace(remark))
	if r == "" {
		return false
	}
	for _, needle := range []string{"error", "timed out", "timeout", "too many", "not enough", "aborted", "refused"} {
		if strings.Contains(r, needle) {
			return true
		}
	}
	return false
}

func shortEndpoint(raw string) string {
	s := strings.TrimPrefix(strings.TrimPrefix(raw, "https://"), "http://")
	if i := strings.Index(s, "/"); i > 0 {
		return s[:i]
	}
	return s
}

func truncate(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
