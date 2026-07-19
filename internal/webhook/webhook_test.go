package webhook

import "testing"

func TestWebhookURLForLogRemovesCredentials(t *testing.T) {
	t.Parallel()

	got := webhookURLForLog(
		"https://webhook-user:super-secret@gateway.internal/link-u-im-processor/webhook?event=msg.notify",
	)
	want := "https://gateway.internal/link-u-im-processor/webhook?event=msg.notify"
	if got != want {
		t.Fatalf("unexpected sanitized webhook URL: got %q, want %q", got, want)
	}
}

func TestWebhookURLForLogKeepsCredentialFreeURL(t *testing.T) {
	t.Parallel()

	rawURL := "https://gateway.internal/link-u-im-processor/webhook?event=msg.offline"
	if got := webhookURLForLog(rawURL); got != rawURL {
		t.Fatalf("credential-free URL changed: got %q, want %q", got, rawURL)
	}
}

func TestWebhookURLForLogRejectsMalformedURL(t *testing.T) {
	t.Parallel()

	if got := webhookURLForLog("http://%zz"); got != "<invalid-webhook-url>" {
		t.Fatalf("malformed URL was not rejected: got %q", got)
	}
}
