package scripts_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDockerPrometheusSBOMMergesSharedDependencies(t *testing.T) {
	child := filepath.Join(t.TempDir(), "child.json")
	require.NoError(t, os.WriteFile(child, []byte(`{
	  "bomFormat":"CycloneDX","metadata":{"component":{"bom-ref":"directory"}},
	  "components":[{"bom-ref":"prometheus","name":"github.com/prometheus/prometheus","version":"UNKNOWN"},{"bom-ref":"shared"},{"bom-ref":"child-lib"}],
	  "dependencies":[
	    {"ref":"directory","dependsOn":["prometheus"]},
	    {"ref":"prometheus","dependsOn":["shared"]},
	    {"ref":"shared","dependsOn":["child-lib"]}
	  ]
	}`), 0o600))
	cmd := exec.Command("jq", "--slurpfile", "child", child, "-f",
		filepath.Join(repoRoot(t), "scripts", "merge-docker-prometheus-sbom.jq"),
		"--arg", "prometheus_version", "3.14.0-wukongim.1",
		"--arg", "prometheus_revision", "upstream-commit")
	cmd.Stdin = strings.NewReader(`{
	  "bomFormat":"CycloneDX","metadata":{"component":{"bom-ref":"image"}},
	  "components":[{"bom-ref":"wukongim"},{"bom-ref":"shared"},{"bom-ref":"image-lib"}],
	  "dependencies":[{"ref":"shared","dependsOn":["image-lib"]}]
	}`)
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, string(output))
	var result struct {
		Components []struct {
			Ref        string `json:"bom-ref"`
			Version    string `json:"version"`
			PURL       string `json:"purl"`
			Properties []struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			} `json:"properties"`
		} `json:"components"`
		Dependencies []struct {
			Ref       string   `json:"ref"`
			DependsOn []string `json:"dependsOn"`
		} `json:"dependencies"`
	}
	require.NoError(t, json.Unmarshal(output, &result))
	require.Len(t, result.Components, 5)
	for _, component := range result.Components {
		if component.Ref == "prometheus" {
			require.Equal(t, "3.14.0-wukongim.1", component.Version)
			require.Equal(t, "pkg:golang/github.com/prometheus/prometheus@3.14.0-wukongim.1", component.PURL)
			require.Len(t, component.Properties, 1)
			require.Equal(t, "wukongim:prometheus:upstream-revision", component.Properties[0].Name)
			require.Equal(t, "upstream-commit", component.Properties[0].Value)
		}
	}
	require.Len(t, result.Dependencies, 2)
	for _, dependency := range result.Dependencies {
		require.NotEqual(t, "directory", dependency.Ref)
		if dependency.Ref == "shared" {
			require.ElementsMatch(t, []string{"image-lib", "child-lib"}, dependency.DependsOn)
		}
	}
}

func TestDockerPrometheusSBOMRejectsMissingChildInventory(t *testing.T) {
	child := filepath.Join(t.TempDir(), "child.json")
	require.NoError(t, os.WriteFile(child, []byte(`{"bomFormat":"CycloneDX","components":[]}`), 0o600))
	cmd := exec.Command("jq", "--slurpfile", "child", child, "-f",
		filepath.Join(repoRoot(t), "scripts", "merge-docker-prometheus-sbom.jq"),
		"--arg", "prometheus_version", "3.14.0-wukongim.1",
		"--arg", "prometheus_revision", "upstream-commit")
	cmd.Stdin = strings.NewReader(`{"components":[]}`)
	output, err := cmd.CombinedOutput()
	require.Error(t, err)
	require.Contains(t, string(output), "missing embedded Prometheus SBOM components")
}
