package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"github.com/approachcontrol/approach/flowstore"
)

// runDB handles `approach db ...`. It is the only surface outside TUI startup
// that may advance the flow database schema, which is what lets every other
// command open the store as a reader or a writer and refuse to migrate.
func runDB(args []string, deps runDeps) error {
	if len(args) == 3 && isHelpArg(args[2]) {
		printDBHelp(deps.stdout)
		return nil
	}
	if len(args) < 3 {
		return fmt.Errorf("usage: approach db <inspect|migrate> [flags]")
	}
	switch args[2] {
	case "inspect":
		return runDBInspect(args[3:], deps)
	case "migrate":
		return runDBMigrate(args[3:], deps)
	default:
		return unknownCommandError(args[2], []string{"inspect", "migrate"}, dbHelpText)
	}
}

func printDBHelp(w io.Writer) {
	io.WriteString(w, dbHelpText)
}

const dbHelpText = `Usage: approach db <inspect|migrate> [flags]

Diagnose and migrate the flow database under the approach agent-artifact root.

Commands:
  inspect  Report what is in a state root and whether approach can open it.
  migrate  Advance the flow database to the schema this build writes.

Migration is not automatic. ` + "`approach flow`" + `, ` + "`approach serve`" + `, and the
session hook open the store as a reader or a writer and refuse a database that
needs migrating; this command and a TUI start are the two that migrate it.

Flags:
  inspect [--json] [--state-root PATH]
  migrate [--backup-dir PATH] [--state-root PATH]

` + "`db inspect`" + ` never refuses. A database written by a newer approach is
reported, not rejected, which is the point of having a diagnostic.

Examples:
  approach db inspect --json
  approach db inspect --state-root "$SCRATCH_ROOT"
  approach db migrate --backup-dir /var/backups/approach
`

func runDBInspect(args []string, deps runDeps) error {
	flags := flag.NewFlagSet("db inspect", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Usage = func() { printDBHelp(deps.stdout) }
	asJSON := flags.Bool("json", false, "emit JSON output")
	stateRoot := flags.String("state-root", "", "artifact state root")
	if help, err := parseCommandFlags(flags, args); help || err != nil {
		if help {
			return nil
		}
		return err
	}
	root, _, err := resolveDBStateRoot(*stateRoot, deps)
	if err != nil {
		return err
	}
	report, err := flowstore.Inspect(root)
	if err != nil {
		return err
	}
	if *asJSON {
		encoder := json.NewEncoder(deps.stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(report)
	}
	printInspectReport(deps.stdout, report)
	return nil
}

func printInspectReport(w io.Writer, report flowstore.InspectReport) {
	fmt.Fprintf(w, "path:      %s\n", report.Path)
	fmt.Fprintf(w, "tier:      %s\n", report.Tier)
	fmt.Fprintf(w, "readable:  %t\n", report.Readable)
	if report.UserVersion != nil {
		fmt.Fprintf(w, "schema:    %d (this build writes %d)\n", *report.UserVersion, flowstore.DatabaseSchemaVersion())
	}
	if report.CheckpointedUserVersion != nil {
		fmt.Fprintf(w, "schema:    %d as of the last checkpoint (a floor, not the live version)\n",
			*report.CheckpointedUserVersion)
	}
	if report.JournalMode != nil {
		fmt.Fprintf(w, "journal:   %s\n", *report.JournalMode)
	}
	if report.DirectoryMode != nil {
		fmt.Fprintf(w, "root mode: %s\n", *report.DirectoryMode)
	}
	if report.GenerationID != nil {
		fmt.Fprintf(w, "migrated:  generation %s\n", *report.GenerationID)
	}
	if report.SidecarStale != nil && *report.SidecarStale {
		fmt.Fprintln(w, "sidecar:   disagrees with the database; a migrator open will repair it")
	}
	if report.MigrationOwner != nil {
		fmt.Fprintf(w, "migrating: pid %d holds the bootstrap lock (unverified — it may be stale)\n",
			report.MigrationOwner.PID)
	}
	for _, warning := range report.Warnings {
		fmt.Fprintf(w, "warning:   %s\n", warning)
	}
	if report.Reason != nil {
		fmt.Fprintf(w, "reason:    %s\n", *report.Reason)
	}
	if report.NextAction != nil {
		fmt.Fprintf(w, "next:      %s\n", *report.NextAction)
	}
}

func runDBMigrate(args []string, deps runDeps) error {
	flags := flag.NewFlagSet("db migrate", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Usage = func() { printDBHelp(deps.stdout) }
	backupDir := flags.String("backup-dir", "", "directory for the pre-migration backup")
	stateRoot := flags.String("state-root", "", "artifact state root")
	if help, err := parseCommandFlags(flags, args); help || err != nil {
		if help {
			return nil
		}
		return err
	}
	root, explicit, err := resolveDBStateRoot(*stateRoot, deps)
	if err != nil {
		return err
	}
	cfg, err := deps.loadConfig()
	if err != nil {
		return fmt.Errorf("error loading config: %w", err)
	}
	// The one command outside TUI startup that holds RoleMigrator, and
	// therefore the one that has to honour the dev-live acknowledgement: the
	// TUI reads it from --allow-dev-live-migration, and without the environment
	// spelling here a development build could never use the sanctioned entry
	// point on a release-shaped scratch root.
	store, err := flowstore.NewStore(flowstore.StoreOptions{
		Root:                  root,
		RootExplicit:          explicit,
		Role:                  flowstore.RoleMigrator,
		BackupDir:             *backupDir,
		Presets:               cfg.Flow.Presets,
		AllowDevLiveMigration: truthyEnv(deps.getenv("APPROACH_ALLOW_DEV_LIVE_MIGRATION")),
	})
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	report, err := flowstore.Inspect(root)
	if err != nil {
		return err
	}
	version := int64(flowstore.DatabaseSchemaVersion())
	if report.UserVersion != nil {
		version = *report.UserVersion
	}
	fmt.Fprintf(deps.stdout, "flow database at %s is at schema %d\n", report.Path, version)
	for _, warning := range store.OpenDiagnostics().Warnings {
		fmt.Fprintf(deps.stderr, "approach: %s\n", warning)
	}
	return nil
}

// resolveDBStateRoot flattens the root sources the way every other command
// does, and reports whether the operator named it. `db` deliberately does not
// read APPROACH_FLOW_STATE_ROOT or APPROACH_PLAN_STATE_ROOT: migrating is an
// operator action against a root they chose, not one inherited from a launch
// environment that happened to be exported.
func resolveDBStateRoot(stateRoot string, deps runDeps) (string, bool, error) {
	if stateRoot != "" {
		return stateRoot, true, nil
	}
	if envRoot := deps.getenv("APPROACH_SESSION_STATE_ROOT"); envRoot != "" {
		return envRoot, true, nil
	}
	cfg, err := deps.loadConfig()
	if err != nil {
		return "", false, fmt.Errorf("error loading config: %w", err)
	}
	if cfg.Sessions.Root != "" {
		return cfg.Sessions.Root, false, nil
	}
	root, err := flowstore.DefaultRoot()
	if err != nil {
		return "", false, err
	}
	return root, false, nil
}
