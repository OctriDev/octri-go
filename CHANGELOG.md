# Changelog

## 1.1.0

- Payloads are now scrubbed before they are sent. Values under keys that name a
  credential (`password`, `secret`, `token`, `apiKey`, `authorization`,
  `cookie`, `ssn` and the rest) are replaced with `[redacted]` at any depth, and
  free text is swept for bearer tokens, JWTs, Luhn-valid card numbers and email
  addresses.
- `AddScrubFields` adds your own key names to that list.
- `SetBeforeSend` hands you each payload before it goes out; return
  `nil` to drop the event. Redaction runs after the hook.
- The `user` field keeps the identity you set, since that is the point of it.
  Credential-shaped keys inside it are still redacted.

## 1.0.1

- Source context is now read only for frames in your own code, skips files
  over 512 KB, and keeps a bounded cache, so a deep or unusual stack cannot
  pull dependency source into a report or grow memory without limit.

## 1.0.0

First public release.

- Error reporting with original source context for each in-app stack frame.
- Request and sub-span timing, drawn as a waterfall in the dashboard.
- W3C `traceparent` propagation, so a server error links to the client SDK
  error for the same request.
- Standalone events with idempotent, best-effort delivery.
- `net/http` middleware and a `RoundTripper` wrapper for outbound calls.
