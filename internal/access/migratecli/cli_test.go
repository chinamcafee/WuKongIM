package migratecli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestFailedPreparationEmitsEvidenceAndRetainsFailureExit(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"prepare", "--plan", "/plan.json", "--workspace", "/workspace"}, &stdout, &stderr,
		func(context.Context, Command) (any, error) {
			return map[string]any{"status": "blocked", "cutover_ready": false}, errors.New("unresolved compatibility")
		})
	if code != 1 || !bytes.Contains(stderr.Bytes(), []byte("unresolved compatibility")) {
		t.Fatalf("failure was hidden: code=%d stderr=%s", code, stderr.String())
	}
	var result struct {
		Status       string `json:"status"`
		CutoverReady bool   `json:"cutover_ready"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result.Status != "blocked" || result.CutoverReady {
		t.Fatalf("invalid failure evidence: %s (%v)", stdout.String(), err)
	}
}

func TestFailureWithoutEvidenceDoesNotEmitSuccessJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"prepare", "--plan", "/plan.json", "--workspace", "/workspace"}, &stdout, &stderr,
		func(context.Context, Command) (any, error) { return nil, errors.New("invalid plan") })
	if code != 1 || stdout.Len() != 0 || stderr.String() != "invalid plan\n" {
		t.Fatalf("unexpected failure output: code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}
