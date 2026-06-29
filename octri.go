// Package octri is server-side error monitoring for Go backends.
//
// Add it to your live API; it reports backend errors to your Octri monitoring
// project (with original-source context per stack frame) and links each one to
// the client SDK error for the same request via the W3C `traceparent` header —
// so the dashboard shows the full client -> server stack under one trace. It
// also times requests (and any sub-spans you open) into the request waterfall.
package octri

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Config holds the reporter settings. Call Init once at startup.
type Config struct {
	URL         string // Monitoring base URL, e.g. https://monitoring.example.com
	Token       string // Ingest token for your project
	Environment string // Project environment / id (the dashboard project id)
	Release     string // Optional release identifier
}

var config *Config

// Init configures the reporter. Call once at startup before mounting Middleware.
func Init(cfg Config) {
	cfg.URL = strings.TrimRight(cfg.URL, "/")
	config = &cfg
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func newSpanID() string { return randomHex(8) }

func defStr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// ── Trace context (W3C) ──────────────────────────────────────────────────────

// Trace identifies a distributed trace and the caller's span.
type Trace struct {
	TraceID      string
	ParentSpanID string
}

var traceparentRe = regexp.MustCompile(`(?i)^00-([0-9a-f]{32})-([0-9a-f]{16})-[0-9a-f]{2}$`)

// TraceFromHeader reads the trace from a `traceparent` header, or starts a fresh one.
func TraceFromHeader(traceparent string) Trace {
	if m := traceparentRe.FindStringSubmatch(strings.TrimSpace(traceparent)); m != nil {
		return Trace{TraceID: m[1], ParentSpanID: m[2]}
	}
	return Trace{TraceID: randomHex(16)}
}

// The active span for a request is carried on the context so sub-spans nest.
type ctxKey struct{}

type spanContext struct {
	traceID string
	spanID  string
}

func withSpanContext(ctx context.Context, sc spanContext) context.Context {
	return context.WithValue(ctx, ctxKey{}, sc)
}

func currentSpan(ctx context.Context) (spanContext, bool) {
	sc, ok := ctx.Value(ctxKey{}).(spanContext)
	return sc, ok
}

// ── Frame source context ─────────────────────────────────────────────────────

const contextLines = 5

var sourceCache sync.Map // file -> []string (nil = unreadable)

func readSource(file string) []string {
	if v, ok := sourceCache.Load(file); ok {
		lines, _ := v.([]string)
		return lines
	}
	var lines []string
	if data, err := os.ReadFile(file); err == nil {
		lines = strings.Split(string(data), "\n")
	}
	sourceCache.Store(file, lines)
	return lines
}

func clip(lines []string, from, to int) []string {
	if from < 0 {
		from = 0
	}
	if to > len(lines) {
		to = len(lines)
	}
	if from >= to {
		return nil
	}
	out := make([]string, to-from)
	copy(out, lines[from:to])
	return out
}

type frame struct {
	Function    string   `json:"function,omitempty"`
	Filename    string   `json:"filename"`
	Lineno      int      `json:"lineno"`
	Colno       int      `json:"colno"`
	InApp       bool     `json:"inApp"`
	ContextLine string   `json:"contextLine,omitempty"`
	PreContext  []string `json:"preContext,omitempty"`
	PostContext []string `json:"postContext,omitempty"`
}

// buildFrames captures the current goroutine's stack (skipping `skip` internal
// frames) with original-source context. Innermost frame first, matching the
// dashboard's culprit = top frame.
func buildFrames(skip int) []frame {
	pcs := make([]uintptr, 64)
	n := runtime.Callers(skip, pcs)
	if n == 0 {
		return nil
	}
	goroot := runtime.GOROOT()
	callers := runtime.CallersFrames(pcs[:n])
	var frames []frame
	for {
		f, more := callers.Next()
		if f.File != "" {
			fr := frame{
				Function: f.Function,
				Filename: f.File,
				Lineno:   f.Line,
				InApp:    !strings.HasPrefix(f.File, goroot) && !strings.Contains(f.File, "/go/pkg/mod/"),
			}
			if lines := readSource(f.File); lines != nil && f.Line >= 1 && f.Line <= len(lines) {
				idx := f.Line - 1
				fr.ContextLine = lines[idx]
				fr.PreContext = clip(lines, idx-contextLines, idx)
				fr.PostContext = clip(lines, idx+1, idx+1+contextLines)
			}
			frames = append(frames, fr)
		}
		if !more {
			break
		}
	}
	return frames
}

// ── Reporting ────────────────────────────────────────────────────────────────

var httpClient = &http.Client{Timeout: 5 * time.Second}

func send(path string, payload any) {
	cfg := config
	if cfg == nil {
		return
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	go func() {
		req, err := http.NewRequest(http.MethodPost, cfg.URL+path, bytes.NewReader(body))
		if err != nil {
			return
		}
		req.Header.Set("content-type", "application/json")
		req.Header.Set("authorization", "Bearer "+cfg.Token)
		if resp, err := httpClient.Do(req); err == nil {
			_ = resp.Body.Close()
		}
	}()
}

type errorOptions struct {
	level      string
	method     string
	path       string
	statusCode int
}

// ErrorOption customises a CaptureError call.
type ErrorOption func(*errorOptions)

// WithHTTP records the request method, path and status with the error.
func WithHTTP(method, path string, statusCode int) ErrorOption {
	return func(o *errorOptions) {
		o.method, o.path, o.statusCode = method, path, statusCode
	}
}

// WithLevel overrides the level ("error" by default).
func WithLevel(level string) ErrorOption {
	return func(o *errorOptions) { o.level = level }
}

// CaptureError reports an error to the monitoring project (fire-and-forget),
// tagged octri.origin=server and linked to the request's trace (from ctx).
func CaptureError(ctx context.Context, err error, opts ...ErrorOption) {
	if config == nil || err == nil {
		return
	}
	o := errorOptions{level: "error"}
	for _, opt := range opts {
		opt(&o)
	}
	tr := Trace{TraceID: randomHex(16)}
	if sc, ok := currentSpan(ctx); ok {
		tr = Trace{TraceID: sc.traceID, ParentSpanID: sc.spanID}
	}
	payload := map[string]any{
		"eventId":     randomHex(8),
		"timestamp":   time.Now().UTC().Format(time.RFC3339Nano),
		"level":       o.level,
		"environment": config.Environment,
		"traceId":     tr.TraceID,
		"spanId":      randomHex(8),
		"tags":        map[string]string{"octri.origin": "server"},
		"error": map[string]any{
			"name":    fmt.Sprintf("%T", err),
			"message": err.Error(),
			"frames":  buildFrames(3),
		},
	}
	if config.Release != "" {
		payload["release"] = config.Release
	}
	if o.method != "" {
		payload["method"] = o.method
	}
	if o.path != "" {
		payload["path"] = o.path
	}
	if o.statusCode != 0 {
		payload["statusCode"] = o.statusCode
	}
	send("/ingest", payload)
}

// ── Spans (request waterfall) ────────────────────────────────────────────────

// Span is a single timed segment of a trace.
type Span struct {
	TraceID      string
	SpanID       string
	ParentSpanID string
	Name         string
	Service      string
	OperationID  string
	StartTime    time.Time
	EndTime      time.Time
	Status       string
}

// CaptureSpan reports one span to the trace store (fire-and-forget).
func CaptureSpan(s Span) {
	if config == nil {
		return
	}
	payload := map[string]any{
		"traceId":   s.TraceID,
		"spanId":    s.SpanID,
		"name":      s.Name,
		"service":   defStr(s.Service, "server"),
		"startTime": s.StartTime.UTC().Format(time.RFC3339Nano),
		"status":    defStr(s.Status, "ok"),
	}
	if s.ParentSpanID != "" {
		payload["parentSpanId"] = s.ParentSpanID
	}
	if !s.EndTime.IsZero() {
		payload["endTime"] = s.EndTime.UTC().Format(time.RFC3339Nano)
	}
	if s.OperationID != "" {
		payload["operationId"] = s.OperationID
	}
	send("/traces", payload)
}

type spanOptions struct{ op string }

// SpanOption customises a sub-span.
type SpanOption func(*spanOptions)

// Op sets a category for color-coding the waterfall, e.g. "db", "cache", "http".
func Op(op string) SpanOption {
	return func(o *spanOptions) { o.op = op }
}

// ActiveSpan is an open sub-span; call End when the work finishes.
type ActiveSpan struct {
	span  Span
	ended bool
}

// StartSpan opens a sub-span under the active request span and returns a child
// context so nested StartSpan calls nest correctly. End() reports it. Outside a
// request (no active span) it returns the context unchanged and a no-op span.
//
//	ctx, span := octri.StartSpan(ctx, "orders.list", octri.Op("db"))
//	defer span.End()
func StartSpan(ctx context.Context, name string, opts ...SpanOption) (context.Context, *ActiveSpan) {
	o := spanOptions{}
	for _, opt := range opts {
		opt(&o)
	}
	sc, ok := currentSpan(ctx)
	if config == nil || !ok {
		return ctx, &ActiveSpan{ended: true}
	}
	spanID := newSpanID()
	as := &ActiveSpan{span: Span{
		TraceID:      sc.traceID,
		SpanID:       spanID,
		ParentSpanID: sc.spanID,
		Name:         name,
		Service:      defStr(o.op, "server"),
		StartTime:    time.Now(),
		Status:       "ok",
	}}
	return withSpanContext(ctx, spanContext{traceID: sc.traceID, spanID: spanID}), as
}

// Fail marks the span as errored (reported when End is called).
func (s *ActiveSpan) Fail() {
	if s != nil {
		s.span.Status = "error"
	}
}

// End closes the span and reports it. Safe to call once; further calls are no-ops.
func (s *ActiveSpan) End() {
	if s == nil || s.ended {
		return
	}
	s.ended = true
	if s.span.TraceID == "" {
		return
	}
	s.span.EndTime = time.Now()
	CaptureSpan(s.span)
}
