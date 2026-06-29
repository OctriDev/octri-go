# octri (Go)

Server-side error monitoring for **Go** backends. Add it to your live API and it
reports backend errors to your Octri monitoring project — with original-source
context per stack frame — and **links each one to the client SDK error for the
same request** via the W3C `traceparent` header. It also times requests (and any
sub-spans you open) into the request waterfall.

The Go sibling of [`@octri/node`](../octri-node). Standard library only.

## Setup

```go
import "github.com/octri/octri-go"

octri.Init(octri.Config{
    URL:         "https://monitoring.example.com",
    Token:       os.Getenv("OCTRI_TOKEN"),
    Environment: "<your project id>",
    Release:     os.Getenv("GIT_SHA"), // optional
})

http.ListenAndServe(":8080", octri.Middleware(mux))
```

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

```go
ctx, span := octri.StartSpan(r.Context(), "orders.list", octri.Op("db"))
rows, err := db.QueryContext(ctx, sql)
if err != nil {
    span.Fail()
}
span.End()
```

Pass the returned `ctx` down so nested `StartSpan` calls nest correctly; `op`
("db", "cache", "http", …) color-codes the waterfall.
