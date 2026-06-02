package ui

import "github.com/brian-bell/wtui/gitquery"

// renderBranchPane is a test-only convenience wrapper around
// renderBranchPaneSelected with selection/scroll disabled and no repo path.
// It is kept out of the production binary because nothing in production uses it.
func renderBranchPane(rows []gitquery.BranchRow, width, height int) []string {
	return renderBranchPaneSelected(rows, 0, 0, width, height, "")
}
