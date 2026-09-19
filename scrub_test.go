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

// scrubServer starts an ingest stub and points the reporter at it. The returned
// function blocks until the next payload arrives.
func scrubServer(t *testing.T) func() map[string]any {
	t.Helper()
	received := make(chan map[string]any, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode event: %v", err)
		}
		received <- body
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(func() {
		server.Close()
		extraScrubKeys = nil
		SetBeforeSend(nil)
	})
	Init(Config{URL: server.URL, Environment: "project-1"})

	return func() map[string]any {
		t.Helper()
		select {
		case body := <-received:
			return body
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for event")
			return nil
		}
	}
}

func TestCredentialShapedKeysAreRedacted(t *testing.T) {
	next := scrubServer(t)
	CaptureEvent("checkout failed", EventOptions{Context: map[string]any{
		"api_key":         "sk_live_1",
		"apiKey":          "sk_live_2",
		"X-API-KEY":       "sk_live_3",
		"stripeSecretKey": "sk_live_4",
		"Authorization":   "Bearer abc",
		"refresh_token":   "rt_1",
		"cookie":          "sid=1",
		"orderId":         "A-1024",
		"author":          "ada",
	}})

	context, _ := next()["context"].(map[string]any)
	for _, key := range []string{
		"api_key", "apiKey", "X-API-KEY", "stripeSecretKey",
		"Authorization", "refresh_token", "cookie",
	} {
		if context[key] != redacted {
			t.Fatalf("%s = %#v, want redacted", key, context[key])
		}
	}
	if context["orderId"] != "A-1024" || context["author"] != "ada" {
		t.Fatalf("ordinary fields were redacted: %#v", context)
	}
}

func TestNestedAndSliceValuesAreRedacted(t *testing.T) {
	next := scrubServer(t)
	CaptureEvent("upstream rejected the call", EventOptions{Context: map[string]any{
		"upstream": map[string]any{
			"headers": []any{map[string]any{"authorization": "Bearer abc"}},
		},
	}})

	context, _ := next()["context"].(map[string]any)
	upstream, _ := context["upstream"].(map[string]any)
	headers, _ := upstream["headers"].([]any)
	first, _ := headers[0].(map[string]any)
	if first["authorization"] != redacted {
		t.Fatalf("nested header = %#v, want redacted", first["authorization"])
	}
}

func TestAddScrubFieldsIsAdditive(t *testing.T) {
	next := scrubServer(t)
	AddScrubFields("account_number")
	CaptureEvent("payout failed", EventOptions{Context: map[string]any{
		"accountNumber": "12345678",
		"orderId":       "A-1024",
	}})

	context, _ := next()["context"].(map[string]any)
	if context["accountNumber"] != redacted {
		t.Fatalf("accountNumber = %#v, want redacted", context["accountNumber"])
	}
	if context["orderId"] != "A-1024" {
		t.Fatalf("orderId = %#v", context["orderId"])
	}
}

func TestSecretsInFreeTextAreStripped(t *testing.T) {
	next := scrubServer(t)
	CaptureEvent("401 from billing: Authorization: Bearer sk_live_abc123 rejected", EventOptions{})
	message, _ := next()["message"].(string)
	if strings.Contains(message, "sk_live_abc123") || !strings.Contains(message, redacted) {
		t.Fatalf("message = %q", message)
	}

	CaptureEvent("token eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.7Hk2 expired", EventOptions{})
	if message, _ = next()["message"].(string); message != "token "+redacted+" expired" {
		t.Fatalf("message = %q", message)
	}

	CaptureEvent("no account for ada@example.com", EventOptions{})
	if message, _ = next()["message"].(string); message != "no account for "+redacted {
		t.Fatalf("message = %q", message)
	}
}

func TestCardNumbersAreStrippedButOrderNumbersAreNot(t *testing.T) {
	next := scrubServer(t)
	CaptureEvent("charge 4242 4242 4242 4242 failed for order 1234567890123", EventOptions{})

	message, _ := next()["message"].(string)
	if strings.Contains(message, "4242") {
		t.Fatalf("card number survived: %q", message)
	}
	if !strings.Contains(message, "1234567890123") {
		t.Fatalf("order number was redacted: %q", message)
	}
}

func TestErrorMessagesAreScrubbed(t *testing.T) {
	next := scrubServer(t)
	CaptureError(nil, errors.New("mail to ada@example.com bounced"))

	body := next()
	failure, _ := body["error"].(map[string]any)
	if failure["message"] != "mail to "+redacted+" bounced" {
		t.Fatalf("error message = %#v", failure["message"])
	}
}

// The identity the dashboard keys on is "id", which survives. Direct
// identifiers under the user are redacted like they are in every generated SDK.
func TestUserIDSurvivesButUserCredentialsAndIdentifiersDoNot(t *testing.T) {
	next := scrubServer(t)
	CaptureEvent("profile update failed", EventOptions{User: map[string]any{
		"id":            "u_1",
		"email":         "ada@example.com",
		"sessionToken":  "st_1",
		"customerPhone": "+1 555 0100",
	}})

	user, _ := next()["user"].(map[string]any)
	if user["id"] != "u_1" {
		t.Fatalf("user id was scrubbed: %#v", user)
	}
	for _, key := range []string{"email", "sessionToken", "customerPhone"} {
		if user[key] != redacted {
			t.Fatalf("%s = %#v, want redacted", key, user[key])
		}
	}
}

func TestIdentifierWordsInsideLongerKeysAreRedacted(t *testing.T) {
	next := scrubServer(t)
	CaptureEvent("checkout failed", EventOptions{Context: map[string]any{
		"billingAddress":      map[string]any{"line1": "1 High St"},
		"shipping_first_name": "Ada",
		"avatarUrl":           "https://cdn.example.com/a.png",
		"queryTimeMs":         12,
	}})

	ctx, _ := next()["context"].(map[string]any)
	if ctx["billingAddress"] != redacted || ctx["shipping_first_name"] != redacted {
		t.Fatalf("identifier keys survived: %#v", ctx)
	}
	if ctx["avatarUrl"] != "https://cdn.example.com/a.png" || ctx["queryTimeMs"] != float64(12) {
		t.Fatalf("innocent keys were scrubbed: %#v", ctx)
	}
}

func TestBeforeSendEditsThenRedactionRuns(t *testing.T) {
	next := scrubServer(t)
	SetBeforeSend(func(payload map[string]any) map[string]any {
		payload["context"] = map[string]any{"note": "call ada@example.com"}
		return payload
	})
	CaptureEvent("build failed", EventOptions{})

	context, _ := next()["context"].(map[string]any)
	if context["note"] != "call "+redacted {
		t.Fatalf("note = %#v", context["note"])
	}
}

func TestBeforeSendReturningNilDropsTheEvent(t *testing.T) {
	next := scrubServer(t)
	SetBeforeSend(func(payload map[string]any) map[string]any {
		if payload["message"] == "noise" {
			return nil
		}
		return payload
	})
	CaptureEvent("noise", EventOptions{})
	CaptureEvent("signal", EventOptions{})

	if message := next()["message"]; message != "signal" {
		t.Fatalf("expected the dropped event to never arrive, got %#v", message)
	}
}
