package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

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
		return errors.New("usage: approach db <inspect|migrate|restore> [flags]")
	}
	switch args[2] {
	case "inspect":
		return runDBInspect(args[3:], deps)
	case "migrate":
		return runDBMigrate(args[3:], deps)
	case "restore":
		return runDBRestore(args[3:], deps)
	default:
		return unknownCommandError(args[2], []string{"inspect", "migrate", "restore"}, dbHelpText)
	}
}

func printDBHelp(w io.Writer) {
	io.WriteString(w, dbHelpText)
}

const dbHelpText = `Usage: approach db <inspect|migrate|restore> [flags]

Diagnose, migrate, and restore the flow database under the approach
agent-artifact root.

Commands:
  inspect  Report what is in a state root and whether approach can open it.
  migrate  Advance the flow database to the schema this build writes.
  restore  Put a verified pre-migration backup back in place.

Migration is not automatic. ` + "`approach flow`" + `, ` + "`approach serve`" + `, and the
session hook open the store as a reader or a writer and refuse a database that
needs migrating; this command and a TUI start are the two that migrate it.

Flags:
  inspect [--json] [--state-root PATH]
  migrate [--backup-dir PATH] [--state-root PATH]
  restore --backup PATH [--force] [--json] [--state-root PATH]

` + "`db inspect`" + ` never refuses. A database written by a newer approach is
reported, not rejected, which is the point of having a diagnostic.

Examples:
  approach db inspect --json
  approach db inspect --state-root "$SCRATCH_ROOT"
  approach db migrate --backup-dir /var/backups/approach
  approach db restore --backup ~/.local/state/approach/sessions/v1/backups/approach.db-...db

` + "`db restore`" + ` refuses while any process holds the database open, and
refuses a backup that is not the one the current generation was migrated from
unless ` + "`--force`" + ` acknowledges it. It copies the database it replaces into
backups/ first, so a restore is itself reversible.
`

// parseDBLeafFlags parses one `db` subcommand's flags and refuses anything
// left over. Both halves matter before the store opens: `help` as a bare word
// reaches Parse as a positional rather than flag.ErrHelp, and Go stops flag
// processing at the FIRST non-flag token, so `db migrate oops --state-root X`
// would otherwise migrate the default root instead of X.
func parseDBLeafFlags(flags *flag.FlagSet, args []string, deps runDeps) (handled bool, err error) {
	if len(args) == 1 && isHelpArg(args[0]) {
		printDBHelp(deps.stdout)
		return true, nil
	}
	if help, err := parseCommandFlags(flags, args); help || err != nil {
		return help, err
	}
	if flags.NArg() > 0 {
		return false, fmt.Errorf("unexpected argument %q\n\n%s", flags.Arg(0), dbHelpText)
	}
	return false, nil
}

func runDBInspect(args []string, deps runDeps) error {
	flags := flag.NewFlagSet("db inspect", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Usage = func() { printDBHelp(deps.stdout) }
	asJSON := flags.Bool("json", false, "emit JSON output")
	stateRoot := flags.String("state-root", "", "artifact state root")
	if handled, err := parseDBLeafFlags(flags, args, deps); handled || err != nil {
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
	if handled, err := parseDBLeafFlags(flags, args, deps); handled || err != nil {
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

func runDBRestore(args []string, deps runDeps) error {
	flags := flag.NewFlagSet("db restore", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Usage = func() { printDBHelp(deps.stdout) }
	backup := flags.String("backup", "", "backup to restore")
	force := flags.Bool("force", false, "restore a backup from another generation")
	asJSON := flags.Bool("json", false, "emit JSON output")
	stateRoot := flags.String("state-root", "", "artifact state root")
	if handled, err := parseDBLeafFlags(flags, args, deps); handled || err != nil {
		return err
	}
	// Checked before the root is resolved so a bare `db restore` prints usage
	// rather than opening anything.
	if strings.TrimSpace(*backup) == "" {
		return fmt.Errorf("db restore requires --backup PATH\n\n%s", dbHelpText)
	}
	root, _, err := resolveDBStateRoot(*stateRoot, deps)
	if err != nil {
		return err
	}
	result, err := flowstore.Restore(flowstore.RestoreOptions{
		Root:       root,
		BackupPath: *backup,
		Force:      *force,
	})
	if err != nil {
		return err
	}
	if *asJSON {
		encoder := json.NewEncoder(deps.stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}
	fmt.Fprintf(deps.stdout, "restored:  %s\n", result.Path)
	fmt.Fprintf(deps.stdout, "from:      %s\n", result.RestoredFrom)
	fmt.Fprintf(deps.stdout, "schema:    %d (this build writes %d)\n",
		result.UserVersion, flowstore.DatabaseSchemaVersion())
	fmt.Fprintf(deps.stdout, "generation: %s\n", result.GenerationID)
	if result.PreRestoreBackup != "" {
		fmt.Fprintf(deps.stdout, "replaced:  copied to %s\n", result.PreRestoreBackup)
	}
	if result.Forced {
		fmt.Fprintln(deps.stderr, "approach: --force restored a backup from another generation")
	}
	if result.UserVersion < int64(flowstore.DatabaseSchemaVersion()) {
		fmt.Fprintf(deps.stdout, "next:      run 'approach db migrate' to bring it to schema %d\n",
			flowstore.DatabaseSchemaVersion())
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
