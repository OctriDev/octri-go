package octri

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

type panicJSON struct{}

func (panicJSON) MarshalJSON() ([]byte, error) {
	panic("serialization panic")
}

type panickingError struct{}

func (panickingError) Error() string {
	panic("error string panic")
}

func TestCaptureEventStandalone(t *testing.T) {
	type captured struct {
		Header http.Header
		Body   map[string]any
	}
	received := make(chan captured, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode event: %v", err)
		}
		received <- captured{Header: r.Header.Clone(), Body: body}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	Init(Config{URL: server.URL, Environment: "project-1"})
	CaptureEvent("checkout.completed", EventOptions{
		EventID: "event-123",
		Tags:    map[string]any{"plan": "growth"},
		Context: map[string]any{"orderId": "order-1"},
	})

	select {
	case event := <-received:
		if got := event.Header.Get("Idempotency-Key"); got != "event-123" {
			t.Fatalf("idempotency key = %q", got)
		}
		if got := event.Header.Get("Authorization"); got != "" {
			t.Fatalf("unexpected authorization header %q", got)
		}
		if event.Body["message"] != "checkout.completed" || event.Body["environment"] != "project-1" {
			t.Fatalf("unexpected payload: %#v", event.Body)
		}
		tags, _ := event.Body["tags"].(map[string]any)
		if tags["octri.origin"] != "standalone" || tags["plan"] != "growth" {
			t.Fatalf("unexpected tags: %#v", tags)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestTraceFromHeaderTrimsAndRejectsZeroIdentifiers(t *testing.T) {
	valid := TraceFromHeader("  00-4BF92F3577B34DA6A3CE929D0E0E4736-00F067AA0BA902B7-01  ")
	if valid.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" || valid.ParentSpanID != "00f067aa0ba902b7" {
		t.Fatalf("unexpected valid trace: %#v", valid)
	}
	for _, header := range []string{
		"00-00000000000000000000000000000000-00f067aa0ba902b7-01",
		"00-4bf92f3577b34da6a3ce929d0e0e4736-0000000000000000-01",
	} {
		fresh := TraceFromHeader(header)
		if len(fresh.TraceID) != 32 || allZeros(fresh.TraceID) || fresh.ParentSpanID != "" {
			t.Fatalf("zero identifier was accepted: %#v", fresh)
		}
	}
}

func TestEventIDsAreNonBlankSafeAndBounded(t *testing.T) {
	if got := safeEventID("  event-123  "); got != "event-123" {
		t.Fatalf("normalized event id = %q", got)
	}
	for _, value := range []string{"", " \t ", "bad\r\nX: true", strings.Repeat("x", 257)} {
		got := safeEventID(value)
		if len(got) != 32 || !safeIdempotencyKey(got) {
			t.Fatalf("unsafe event id %q resolved to %q", value, got)
		}
	}
	if httpClient.Timeout != requestTimeout {
		t.Fatalf("HTTP timeout = %v, want %v", httpClient.Timeout, requestTimeout)
	}
}

func TestSourceContextIsLimitedToInAppFrames(t *testing.T) {
	frames := buildFrames(1)
	inApp := false
	for _, fr := range frames {
		if fr.InApp && fr.ContextLine != "" {
			inApp = true
		}
		if !fr.InApp && (fr.ContextLine != "" || len(fr.PreContext) > 0) {
			t.Fatalf("dependency frame carried source: %s", fr.Filename)
		}
	}
	if !inApp {
		t.Fatal("this test file is in-app, so its frame should carry source")
	}
}

func TestUnsafeAuthAndInvalidSpansAreSuppressed(t *testing.T) {
	originalClient := httpClient
	originalConfig := config
	requests := make(chan *http.Request, 1)
	httpClient = &http.Client{
		Timeout: requestTimeout,
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			requests <- request
			return nil, errors.New("stop")
		}),
	}
	defer func() {
		httpClient = originalClient
		config = originalConfig
	}()

	Init(Config{URL: "https://monitoring.example.com", Environment: "project-1", Token: "bad\r\nX: true"})
	CaptureEvent("suppressed", EventOptions{})
	select {
	case <-requests:
		t.Fatal("unsafe authorization token was sent")
	case <-time.After(50 * time.Millisecond):
	}

	Init(Config{URL: "https://monitoring.example.com", Environment: "project-1"})
	now := time.Now()
	for _, span := range []Span{
		{TraceID: "", SpanID: "span-1", Name: "test", StartTime: now},
		{TraceID: "trace-1", SpanID: "span-1", Name: " ", StartTime: now},
		{TraceID: "trace-1", SpanID: "span-1", Name: "test"},
		{TraceID: strings.Repeat("0", 32), SpanID: strings.Repeat("0", 16), Name: "test", StartTime: now},
	} {
		CaptureSpan(span)
	}
	select {
	case request := <-requests:
		t.Fatalf("invalid span was sent: %s", request.URL)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestReportingContainsSerializationAndTransportPanics(t *testing.T) {
	originalClient := httpClient
	originalConfig := config
	defer func() {
		httpClient = originalClient
		config = originalConfig
	}()

	Init(Config{URL: "https://monitoring.example.com", Environment: "project-1"})
	CaptureEvent("bad payload", EventOptions{Context: map[string]any{"value": panicJSON{}}})
	CaptureError(nil, panickingError{})

	called := make(chan struct{})
	httpClient = &http.Client{
		Timeout: requestTimeout,
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			close(called)
			panic("transport panic")
		}),
	}
	CaptureEvent("transport", EventOptions{})
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("transport was not called")
	}
	// Give the reporting goroutine time to reach its recovery boundary.
	time.Sleep(10 * time.Millisecond)
}
