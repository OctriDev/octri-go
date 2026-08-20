# octri (Go)

Server-side error monitoring for **Go** backends. Add it to your live API and it
reports backend errors to your Octri monitoring project — with original-source
context per stack frame — and **links each one to the client SDK error for the
same request** via the W3C `traceparent` header. It also times requests (and any
sub-spans you open) into the request waterfall.

The Go sibling of [`@octri/node`](../octri-node). Standard library only.

## Setup

```go
import "github.com/octridev/octri-go"

octri.Init(octri.Config{
    URL:         "https://monitoring.example.com",
    Token:       os.Getenv("OCTRI_TOKEN"),
    Environment: "<your project id>",
    Release:     os.Getenv("GIT_SHA"), // optional
})

http.ListenAndServe(":8080", octri.Middleware(mux))
```

Hosted users can copy the project-scoped URL, token, and environment from the
Monitoring connection settings (or its API). Leave `Token` empty only when
pointing at an open self-hosted ingest endpoint. Every request carries an
idempotency key.

## Standalone events

No generated API SDK is required to send your own events:

```go
octri.CaptureEvent("checkout.completed", octri.EventOptions{
    User:    map[string]any{"id": customer.ID},
    Tags:    map[string]any{"region": "eu-west", "plan": "growth"},
    Context: map[string]any{"orderId": order.ID, "total": order.Total},
})
```

Delivery is asynchronous and best-effort. Set `EventID` to make a retried
delivery idempotent.

## Reporting an error

Errors are values in Go, so report them where they happen — frames are captured
at the call site:

```go
if err != nil {
    octri.CaptureError(ctx, err, octri.WithHTTP(r.Method, r.URL.Path, 500))
    return
}
```

`Middleware` also recovers panics, reports them, and re-panics.

## Sub-spans (where time goes)

Go can't monkey-patch, so outbound HTTP is instrumented by wrapping your client's
transport (once), and other work with a small helper:

```go
// Every request made with this client (using the handler's context) is an http span:
client := &http.Client{Transport: octri.WrapTransport(http.DefaultTransport)}
resp, err := client.Do(req.WithContext(ctx))

// Time any work as a sub-span:
err := octri.WithSpan(ctx, "orders.list", func(ctx context.Context) error {
    return db.QueryContext(ctx, sql)
}, octri.Op("db"))
```

Or manage a span directly:

```go
ctx, span := octri.StartSpan(r.Context(), "orders.list", octri.Op("db"))
rows, err := db.QueryContext(ctx, sql)
if err != nil {
    span.Fail()
}
span.Finish()
```

Pass the returned `ctx` down so nested spans nest correctly; `op` ("db", "cache",
"http", …) color-codes the waterfall. Requests to the monitoring backend are
never traced (no feedback loop).
