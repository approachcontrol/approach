package model

import (
	"fmt"
	"strings"

	"github.com/approachcontrol/approach/flowstore"
)

// conflictStatus renders the status-bar refusal for a Bead that already has a
// non-terminal Flow. It names the existing Flow's derived status and ID so the
// user can act without hunting for it.
//
// remedy is true only on the manual f/F paths. The refusal is raised while the
// user is in the Beads Ready subview, where C is not bound, so the remedy has
// to name the Flows view for it to be actionable. The epic-progression paths
// pass false: the user pressed no create key there, and an imperative remedy
// would misdirect.
func conflictStatus(existing flowstore.FlowRecord, remedy bool) string {
	// DeriveStatus returns raw constants like "needs_attention"; humanize them
	// rather than leaking underscores into the status bar. The Bead ID is
	// trimmed because the store matches Bead IDs trimmed and the column can
	// hold an untrimmed value.
	status := strings.ReplaceAll(flowstore.DeriveStatus(existing), "_", " ")
	beadID := strings.TrimSpace(existing.Bead.ID)
	if remedy {
		return fmt.Sprintf("Bead %s already has a %s flow %s: close it from the Flows view with C", beadID, status, existing.FlowID)
	}
	return fmt.Sprintf("Bead %s already has a %s flow %s", beadID, status, existing.FlowID)
}
