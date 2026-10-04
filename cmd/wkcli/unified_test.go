package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnifiedHelpAndFamilyExitCodes(t *testing.T) {
	for _, test := range []struct {
		args []string
		code int
		want string
	}{
		{[]string{"bench", "--target", "/missing", "validate", "--help"}, 0, "--target"},
		{[]string{"--context", "unused", "bench", "send", "--help"}, 0, "--context"},
		{[]string{"bench", "--target=/missing", "validate", "--help"}, 0, "--target"},
		{[]string{"bench", "--help"}, 0, "send"},
		{[]string{"bench", "worker", "--help"}, 0, "wkcli bench worker"},
		{[]string{"bench", "report", "redact-config", "--help"}, 0, "--input"},
		{[]string{"help", "db"}, 0, "query|repl|import|export|diff"},
		{[]string{"--context-dir", t.TempDir(), "db", "--help"}, 0, "hash-slot-count"},
		{[]string{"--context-dir", t.TempDir(), "migrate", "prepare"}, 2, "migration requires"},
		{[]string{"db", "query", "--help"}, 0, "query <sql>"},
		{[]string{"db", "--help"}, 0, "hash-slot-count"},
		{[]string{"migrate", "--help"}, 0, "wkcli migrate"},
		{[]string{"migrate", "prepare", "--help"}, 0, "workspace"},
		{[]string{"migrate", "prepare"}, 2, "migration requires"},
		{[]string{"migrate", "--bad", "ignored", "prepare", "--help"}, 2, "unknown migration command"},
		{[]string{"migrate", "--plan", "/missing", "prepare", "--plan", "/missing", "--workspace", "/missing"}, 2, "unknown migration command"},
		{[]string{"bench", "report", "local-single-node-completion"}, 1, "required"},
		{[]string{"bench", "report", "local-single-node-completion", "--root", t.TempDir(), "--marker", "missing.json"}, 6, "verification failed"},
		{[]string{"db", "--hash-slot-count", "65536", "query", "show tables"}, 1, "must be <= 65535"},
	} {
		t.Run(strings.Join(test.args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := runWithIO(test.args, &stdout, &stderr)
			if code != test.code || !strings.Contains(stdout.String()+stderr.String(), test.want) {
				t.Fatalf("code=%d want=%d stdout=%q stderr=%q", code, test.code, stdout.String(), stderr.String())
			}
		})
	}
}

func TestUnifiedDatabaseImportRetainsQueryFailureAndCleanStdout(t *testing.T) {
	input := t.TempDir()
	if err := os.WriteFile(filepath.Join(input, "manifest.json"), []byte("{bad json}"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := runWithIO([]string{"db", "import", "--input", input, "--dry-run"}, &stdout, &stderr)
	if code != 2 || stdout.Len() != 0 || stderr.Len() == 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestUnifiedVersionJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runWithIO([]string{"version", "--output", "json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("code=%d: %s", code, &stderr)
	}
	var result map[string]string
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result["version"] != buildVersion || result["commit"] != buildCommit || result["build_source"] != buildSource {
		t.Fatalf("identity=%v", result)
	}
}
