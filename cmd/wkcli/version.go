package main

import (
	"encoding/json"
	"fmt"

	"github.com/WuKongIM/WuKongIM/cmd/wkcli/internal/command"
	"github.com/spf13/cobra"
)

var (
	buildVersion = "dev"
	buildCommit  = "unknown"
	buildSource  = "source"
)

// newVersionCommand reports the same release identity as the packaged server.
func newVersionCommand(deps command.Deps) *cobra.Command {
	output := "text"
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print version and source identity",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			switch output {
			case "json":
				return json.NewEncoder(deps.Stdout).Encode(struct {
					Version     string `json:"version"`
					Commit      string `json:"commit"`
					BuildSource string `json:"build_source"`
				}{buildVersion, buildCommit, buildSource})
			case "text":
				_, err := fmt.Fprintf(deps.Stdout, "wkcli %s (commit %s, %s)\n", buildVersion, buildCommit, buildSource)
				return err
			default:
				return fmt.Errorf("--output must be text or json")
			}
		},
	}
	cmd.Flags().StringVar(&output, "output", output, "Output format: text or json")
	return cmd
}
