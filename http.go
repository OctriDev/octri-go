package octri

import (
	"fmt"
	"net/http"
	"time"
)

// statusRecorder captures the response status for the request span.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func panicError(v any) error {
	if err, ok := v.(error); ok {
		return err
	}
	return fmt.Errorf("%v", v)
}

// Middleware times each request as a server span (a child of the client SDK span
// via the incoming `traceparent`) and reports any panic as an error linked to
// the same trace, then re-panics so your server's own recovery still runs.
//
//	octri.Init(octri.Config{URL: ..., Token: ..., Environment: ...})
//	http.ListenAndServe(":8080", octri.Middleware(mux))
//
// Sub-spans opened with octri.StartSpan(r.Context(), ...) nest under the request.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		trace := TraceFromHeader(r.Header.Get("traceparent"))
		spanID := newSpanID()
		start := time.Now()
		ctx := withSpanContext(r.Context(), spanContext{traceID: trace.TraceID, spanID: spanID})
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		defer func() {
			rv := recover()
			status := "ok"
			if rv != nil || rec.status >= 500 {
				status = "error"
			}
			if rv != nil {
				rec.status = http.StatusInternalServerError
				CaptureError(ctx, panicError(rv), WithHTTP(r.Method, r.URL.Path, http.StatusInternalServerError))
			}
			CaptureSpan(Span{
				TraceID:      trace.TraceID,
				SpanID:       spanID,
				ParentSpanID: trace.ParentSpanID,
				Name:         r.Method + " " + r.URL.Path,
				Service:      "server",
				StartTime:    start,
				EndTime:      time.Now(),
				Status:       status,
			})
			if rv != nil {
				panic(rv) // let the server's own panic handling proceed
			}
		}()

		next.ServeHTTP(rec, r.WithContext(ctx))
	})
}
