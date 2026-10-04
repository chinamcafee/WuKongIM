package scripts_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv3"
	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
)

func TestMigrationRehearsalExampleSingleNodeCluster(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts/migration/plan.example.json"))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := migration.ReadPlan(bytes.NewReader(body), "a888f89533d0e7d1b2030e06504ca97f1ad891d4")
	if err != nil {
		t.Fatal(err)
	}
	if err := migrationv3.ValidatePlan(context.Background(), plan.Target); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationRehearsalDryRunIsolation(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	root := t.TempDir()
	for _, dir := range []string{"bundle", "source/node1"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	payload := []byte("dry-run fixture; never executed")
	sum := sha256.Sum256(payload)
	for _, name := range []string{"wkcli", "wukongim"} {
		if err := os.WriteFile(filepath.Join(root, "bundle", name), payload, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	plan := `{"sources":[{"data_dir":"/source/node1"}],"target":{"nodes":[{"data_dir":"/targets/1"}]}}`
	planPath := filepath.Join(root, "plan.json")
	if err := os.WriteFile(planPath, []byte(plan), 0o600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "output")
	args := []string{filepath.Join(repoRoot(t), "scripts/migration/rehearse-offline.py"), "--dry-run",
		"--plan", planPath, "--bundle", filepath.Join(root, "bundle"), "--source-root", filepath.Join(root, "source"),
		"--output", output, "--image", "sha256:" + strings.Repeat("a", 64),
		"--wkcli-sha256", hex.EncodeToString(sum[:]), "--wukongim-sha256", hex.EncodeToString(sum[:])}
	body, err := exec.Command(python, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("dry run: %v: %s", err, body)
	}
	var commands map[string][]string
	if err := json.Unmarshal(body, &commands); err != nil {
		t.Fatal(err)
	}
	if len(commands) != 5 {
		t.Fatalf("missing phases: %s", body)
	}
	for phase, command := range commands {
		joined := strings.Join(command, " ")
		for _, required := range []string{"docker create", "--entrypoint /bundle/wkcli", "migrate ", "--network none", "--pull=never", "--read-only", "--workspace /scratch/workspace"} {
			if !strings.Contains(joined, required) {
				t.Fatalf("%s missing %q", phase, required)
			}
		}
		if phase == "prepare" || phase == "export" {
			if !strings.Contains(joined, ",dst=/source,readonly") || !strings.Contains(joined, "/prepare-work,dst=/scratch") {
				t.Fatalf("source/producer isolation: %s", joined)
			}
		} else if strings.Contains(joined, "dst=/source") || strings.Contains(joined, "/prepare-work") || !strings.Contains(joined, ",dst=/archive,readonly") {
			t.Fatalf("archive consumer exposes source or producer state: %s", joined)
		}
	}
	if !strings.Contains(strings.Join(commands["verify"], " "), "/verify-work,dst=/scratch") {
		t.Fatal("verification shares an import workspace")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("dry run created output")
	}
	if err := os.Mkdir(output, 0o700); err != nil {
		t.Fatal(err)
	}
	if body, err := exec.Command(python, args...).CombinedOutput(); err == nil || !strings.Contains(string(body), "output must not exist") {
		t.Fatalf("existing output was accepted: %v: %s", err, body)
	}
	if err := os.Remove(output); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, plan, flag, value, expected string }{
		{"traversal", strings.ReplaceAll(plan, "/source/node1", "/source/../node1"), "", "", "strictly under /source"},
		{"nested-target", strings.ReplaceAll(plan, "/targets/1", "/targets/nested/1"), "", "", "immediate children"},
		{"source-overlap", plan, "--output", filepath.Join(root, "source", "new"), "overlaps an input"},
		{"mutable-image", plan, "--image", "runtime:latest", "immutable image ID"},
		{"wrong-binary", plan, "--wkcli-sha256", strings.Repeat("0", 64), "approved package digest"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(planPath, []byte(tc.plan), 0o600); err != nil {
				t.Fatal(err)
			}
			modified := append([]string(nil), args...)
			for i := range modified {
				if tc.flag != "" && modified[i] == tc.flag {
					modified[i+1] = tc.value
				}
			}
			body, err := exec.Command(python, modified...).CombinedOutput()
			if err == nil || !strings.Contains(string(body), tc.expected) {
				t.Fatalf("unsafe input accepted: %v: %s", err, body)
			}
			if _, err := os.Stat(output); !os.IsNotExist(err) {
				t.Fatal("rejected input created output")
			}
		})
	}
}
