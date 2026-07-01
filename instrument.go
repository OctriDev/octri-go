package octri

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// WrapTransport wraps an http.RoundTripper so every outbound request made with a
// context that carries the active request span becomes an "http" sub-span. Set
// it on your http.Client once:
//
//	client := &http.Client{Transport: octri.WrapTransport(http.DefaultTransport)}
//	resp, err := client.Do(req.WithContext(ctx)) // ctx from the request handler
//
// Requests to the monitoring backend are never traced (no feedback loop).
func WrapTransport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &roundTripper{base: base}
}

type roundTripper struct {
	base http.RoundTripper
}

func (rt *roundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	sc, ok := currentSpan(req.Context())
	if !ok || config == nil || isMonitoringURL(req.URL) {
		return rt.base.RoundTrip(req)
	}

	spanID := newSpanID()
	start := time.Now()
	resp, err := rt.base.RoundTrip(req)

	status := "ok"
	if err != nil || (resp != nil && resp.StatusCode >= 500) {
		status = "error"
	}
	CaptureSpan(Span{
		TraceID:      sc.traceID,
		SpanID:       spanID,
		ParentSpanID: sc.spanID,
		Name:         req.Method + " " + req.URL.Host + req.URL.Path,
		Service:      "http",
		StartTime:    start,
		EndTime:      time.Now(),
		Status:       status,
	})
	return resp, err
}

func isMonitoringURL(u *url.URL) bool {
	if config == nil || u == nil {
		return false
	}
	mon, err := url.Parse(config.URL)
	if err != nil {
		return false
	}
	return u.Host == mon.Host
}

// WithSpan times fn as a sub-span under the active request span (from ctx),
// marking it errored if fn returns a non-nil error. A convenience over
// StartSpan/Finish:
//
//	err := octri.WithSpan(ctx, "orders.list", func(ctx context.Context) error {
//	    return db.QueryContext(ctx, sql)
//	}, octri.Op("db"))
func WithSpan(ctx context.Context, name string, fn func(context.Context) error, opts ...SpanOption) error {
	ctx, span := StartSpan(ctx, name, opts...)
	err := fn(ctx)
	if err != nil {
		span.Fail()
	}
	span.Finish()
	return err
}
