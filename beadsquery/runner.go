package beadsquery

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Runner is the seam between Beads query orchestration and the bd CLI.
type Runner interface {
	Run(dir string, args ...string) (string, error)
}

const (
	defaultBDOutputLimit = 32 << 20
	defaultBDErrorLimit  = 1 << 20
)

type execRunner struct {
	maxOutputBytes int
}

type cappedBuffer struct {
	buffer   bytes.Buffer
	limit    int
	exceeded bool
}

var defaultRunner Runner = execRunner{}

// DefaultRunner is used by package-level query functions. Tests should prefer
// an injected runner through NewQuerier.
var DefaultRunner Runner = defaultRunner

// Querier orchestrates Beads queries over an injected Runner.
type Querier struct {
	runner Runner
}

// NewQuerier constructs a Querier over r. A nil runner uses the built-in bd
// executable adapter.
func NewQuerier(r Runner) *Querier {
	if r == nil {
		r = defaultRunner
	}
	return &Querier{runner: r}
}

func defaultQuery() *Querier {
	return NewQuerier(DefaultRunner)
}

func (r execRunner) Run(dir string, args ...string) (string, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolving bd directory: %w", err)
	}
	commandArgs := make([]string, 0, len(args)+2)
	// bd resolves -C before its user-level db fallback, setting BEADS_DIR to
	// the repo's effective (redirect-aware) Beads directory.
	commandArgs = append(commandArgs, "-C", absDir)
	commandArgs = append(commandArgs, args...)
	cmd := exec.Command("bd", commandArgs...)
	cmd.Dir = absDir
	cmd.Env, err = isolatedBeadsEnvironment(os.Environ())
	if err != nil {
		return "", err
	}
	limit := r.maxOutputBytes
	if limit <= 0 {
		limit = defaultBDOutputLimit
	}
	out := cappedBuffer{limit: limit}
	stderr := cappedBuffer{limit: defaultBDErrorLimit}
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	err = cmd.Run()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			if msg := strings.TrimSpace(stderr.buffer.String()); msg != "" {
				return "", fmt.Errorf("%s: %w", msg, err)
			}
		}
		return "", err
	}
	if out.exceeded {
		return "", fmt.Errorf("bd output exceeded %d-byte limit", limit)
	}
	return out.buffer.String(), nil
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	remaining := b.limit - b.buffer.Len()
	if remaining < 0 {
		remaining = 0
	}
	writeLen := len(p)
	if writeLen > remaining {
		writeLen = remaining
		b.exceeded = true
	}
	if writeLen > 0 {
		_, _ = b.buffer.Write(p[:writeLen])
	}
	// Report the full write so os/exec keeps draining stdout after the cap.
	return len(p), nil
}

func isolatedBeadsEnvironment(env []string) ([]string, error) {
	filtered := make([]string, 0, len(env))
	for _, entry := range env {
		originalName, value, _ := strings.Cut(entry, "=")
		name := strings.ToUpper(originalName)
		if strings.HasPrefix(name, "BEADS_") || strings.HasPrefix(name, "BD_") {
			switch name {
			case "BD_JSON_ENVELOPE",
				"BD_DISABLE_EVENT_FLUSH",
				"BD_DISABLE_METRICS",
				"BD_OTEL_LOGS_URL",
				"BD_OTEL_METRICS_URL",
				"BD_OTEL_STDOUT",
				"BEADS_CREDENTIALS_FILE",
				"BEADS_DOLT_PASSWORD",
				"BEADS_DOLT_SERVER_TLS",
				"BEADS_DOLT_SERVER_USER":
				// These control output, telemetry, or authentication, not target selection.
			default:
				continue
			}
		}
		if name == "BEADS_CREDENTIALS_FILE" && value != "" && !filepath.IsAbs(value) {
			absValue, err := filepath.Abs(value)
			if err != nil {
				return nil, fmt.Errorf("resolving BEADS_CREDENTIALS_FILE: %w", err)
			}
			entry = originalName + "=" + absValue
		}
		filtered = append(filtered, entry)
	}
	return filtered, nil
}
