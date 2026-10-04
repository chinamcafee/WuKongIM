// Package migratecli adapts command-line arguments and JSON output to the
// migration application. It constructs no source or target infrastructure.
package migratecli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
)

type Command struct{ Verb, PlanPath, WorkspacePath, ArchivePath string }
type Execute func(context.Context, Command) (any, error)

// Run emits machine-readable results on stdout and diagnostics on stderr.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer, execute Execute) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		fmt.Fprintln(stdout, "Usage: wkcli migrate <dedupe-plan|authority|diagnose|prepare|export|export-map|import|verify> --plan /path/plan.json --workspace /path/workspace [--archive /path/archive]\n\ndedupe-plan Plan latest-message duplicate omissions and sequence impact without modifying source or target.\nauthority Audit migration-marked channel configurations and cross-replica histories without selecting or importing.\ndiagnose Census all readable sources and emit all compatibility findings without preparing a target.\nprepare  Inspect stopped original v2 nodes; select authoritative data and validate conversion.\nexport   Recheck unchanged stopped sources and publish a checksummed source archive.\nexport-map Rebuild a checksummed sequence mapping from the complete source archive.\nimport   Install a new native v3 cluster generation from the complete archive.\nverify   Independently compare stopped v3 databases with archived original data.\n\nNo command modifies or upgrades deployed v2. A prepared or exported result is not cutover approval.")
		if len(args) == 0 {
			return 2
		}
		return 0
	}
	cmd := Command{Verb: args[0]}
	if cmd.Verb != "dedupe-plan" && cmd.Verb != "authority" && cmd.Verb != "diagnose" && cmd.Verb != "prepare" && cmd.Verb != "export" && cmd.Verb != "export-map" && cmd.Verb != "import" && cmd.Verb != "verify" {
		fmt.Fprintf(stderr, "unknown migration command %q\n", cmd.Verb)
		return 2
	}
	flags := flag.NewFlagSet("wkcli migrate "+cmd.Verb, flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&cmd.PlanPath, "plan", "", "immutable migration plan JSON")
	flags.StringVar(&cmd.WorkspacePath, "workspace", "", "exclusive migration scratch directory")
	flags.StringVar(&cmd.ArchivePath, "archive", "", "source archive directory (required for export, export-map, import and verify)")
	if err := flags.Parse(args[1:]); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || cmd.PlanPath == "" || cmd.WorkspacePath == "" || (cmd.Verb != "dedupe-plan" && cmd.Verb != "authority" && cmd.Verb != "diagnose" && cmd.Verb != "prepare" && cmd.ArchivePath == "") {
		fmt.Fprintln(stderr, "migration requires --plan, --workspace, and --archive for export/export-map/import/verify")
		return 2
	}
	result, err := execute(ctx, cmd)
	if err != nil {
		// A failed preparation may include completed source-check evidence.
		// Emit it without changing the failure exit status or hiding the error.
		if result != nil {
			enc := json.NewEncoder(stdout)
			enc.SetIndent("", "  ")
			if encodeErr := enc.Encode(result); encodeErr != nil {
				fmt.Fprintln(stderr, encodeErr)
			}
		}
		fmt.Fprintln(stderr, err)
		return 1
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(result); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if result, ok := result.(interface{ CommandExitCode() int }); ok {
		return result.CommandExitCode()
	}
	return 0
}
