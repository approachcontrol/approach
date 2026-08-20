package gitquery

import (
	"fmt"
	"strconv"
	"strings"
)

const maxQueryWarnings = 20

// QueryWarning identifies one failed best-effort enrichment while preserving
// the primary Git query's usable rows.
type QueryWarning struct {
	Operation string
	Subject   string
	Cause     error
}

// PartialQueryError accompanies usable worktree or branch rows whose optional
// Git metadata could not be fully resolved.
type PartialQueryError struct {
	Warnings []QueryWarning
}

func (e *PartialQueryError) Error() string {
	if e == nil {
		return ""
	}
	limit := min(3, len(e.Warnings))
	parts := make([]string, 0, limit+1)
	for _, warning := range e.Warnings[:limit] {
		parts = append(parts, fmt.Sprintf("%s for %s: %s", warning.Operation, diagnosticSubject(warning.Subject), safeDiagnosticText(warning.Cause.Error(), 160)))
	}
	if len(e.Warnings) > limit {
		parts = append(parts, fmt.Sprintf("… %d more", len(e.Warnings)-limit))
	}
	return fmt.Sprintf("%d Git metadata warning(s): %s", len(e.Warnings), strings.Join(parts, "; "))
}

func (e *PartialQueryError) Unwrap() []error {
	if e == nil {
		return nil
	}
	causes := make([]error, 0, len(e.Warnings))
	for _, warning := range e.Warnings {
		if warning.Cause != nil {
			causes = append(causes, warning.Cause)
		}
	}
	return causes
}

// AsPartialQuery classifies a standalone partial-query diagnostic without
// requiring callers to match its human-readable text.
func AsPartialQuery(err error) (*PartialQueryError, bool) {
	partial, ok := err.(*PartialQueryError)
	if !ok || partial == nil || len(partial.Warnings) == 0 {
		return nil, false
	}
	for _, warning := range partial.Warnings {
		if warning.Operation == "" || warning.Cause == nil {
			return nil, false
		}
	}
	return partial, true
}

type queryWarnings []QueryWarning

func (w *queryWarnings) add(operation, subject string, cause error) {
	if cause == nil || len(*w) >= maxQueryWarnings {
		return
	}
	*w = append(*w, QueryWarning{Operation: operation, Subject: subject, Cause: cause})
}

func (w queryWarnings) err() error {
	if len(w) == 0 {
		return nil
	}
	return &PartialQueryError{Warnings: append([]QueryWarning(nil), w...)}
}

func diagnosticSubject(subject string) string {
	if strings.TrimSpace(subject) == "" {
		return strconv.QuoteToASCII(subject)
	}
	return safeDiagnosticText(subject, 120)
}

func safeDiagnosticText(value string, limit int) string {
	var b strings.Builder
	count := 0
	for _, r := range value {
		if count >= limit {
			b.WriteRune('…')
			break
		}
		if strconv.IsPrint(r) {
			b.WriteRune(r)
		} else {
			fmt.Fprintf(&b, "\\u%04X", r)
		}
		count++
	}
	return b.String()
}
