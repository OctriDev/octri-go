# octri (Go)

**Error and performance monitoring for Go backends.** Report errors from your
handlers with original-source context per stack frame, time every request and
any sub-span you open into a waterfall, and join each server error to the client
SDK error for the same request through the W3C `traceparent` header. In the
dashboard you see the full client and server stack under one trace.

Octri turns an OpenAPI spec into a documentation site, client SDKs for ten
languages, an MCP server your AI assistant can call, and monitoring for the
API behind them. This package is the Go monitoring runtime, and it works on
its own: a generated Octri API SDK is not required. See
[octri.dev/monitoring](https://octri.dev/monitoring).

Go 1.21 or newer. Standard library only. The Go sibling of
[`@octri/node`](https://github.com/octridev/octri-node).

## Install

```bash
go get github.com/octridev/octri-go
```

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

Errors are values in Go, so report them where they happen. Frames are captured
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
"http", and so on) color-codes the waterfall. Requests to the monitoring backend are
never traced (no feedback loop).

---

## What gets redacted

Payloads are scrubbed on the way out, so a credential that ended up in a log
line or a context object never reaches the dashboard.

Any key whose name looks like a credential (`password`, `secret`, `token`,
`apiKey`, `authorization`, `cookie`, `ssn` and the rest of the usual list) has
its value replaced with `[redacted]`, at any depth. Matching ignores case and
separators, so `api_key`, `apiKey` and `X-API-KEY` are all the same key.

Free text is swept too: the message, an error message and its stack, and
anything else you send as a string. Bearer tokens, JWTs, card numbers and email
addresses come out as `[redacted]`. A card number has to pass the Luhn check
first, so an order number or a timestamp survives.

`user` is the exception. It is the field you fill with an identity on purpose,
so `user.email` is reported exactly as you set it. Credential-shaped keys inside
it are still redacted.

Add your own key names:

```go
octri.AddScrubFields("accountNumber", "otp")
```

Or take the payload yourself, and return `nil` to drop the event:

```go
octri.SetBeforeSend(func(payload map[string]any) map[string]any {
	if payload["path"] == "/health" {
		return nil
	}
	return payload
})
```

Redaction runs after your hook, so a hook cannot leak a credential by accident.

## The rest of Octri

| Product | What it does |
|---|---|
| [API Studio](https://octri.dev/api-studio) | Your OpenAPI spec becomes a hosted documentation site with a live request playground, editable page by page. |
| [SDK Studio](https://octri.dev/sdk-studio) | The same spec becomes client libraries for ten languages, versioned and released together. |
| [MCP](https://octri.dev/mcp) | Your endpoints and docs become tools an AI assistant can call, generated from the same spec. |
| [Monitoring](https://octri.dev/monitoring) | Errors, traces, uptime and releases for the API, joined to the SDK calls that reached it. |

### Monitoring runtimes

- [Node](https://github.com/octridev/octri-node)
- [Python](https://github.com/octridev/octri-python)
- [Go](https://github.com/octridev/octri-go)
- [Ruby](https://github.com/octridev/octri-ruby)
- [Rust](https://github.com/octridev/octri-rust)
- [PHP](https://github.com/octridev/octri-php)
- [Java](https://github.com/octridev/octri-java)
- [Kotlin](https://github.com/octridev/octri-kotlin)
- [Swift](https://github.com/octridev/octri-swift)
- [Dart](https://github.com/octridev/octri-dart)

### More

- [Documentation](https://docs.octri.dev/docs)
- [Pricing](https://octri.dev/pricing)
- [Changelog](https://docs.octri.dev/changelog)

MIT licensed.
