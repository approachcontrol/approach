package model

import (
	"fmt"

	"github.com/brian-bell/wtui/ui"
)

type ViewChoice struct {
	Number int
	Mode   ui.Mode
	Label  string
}

// viewChoices is the frozen config vocabulary for default_view: the numbers
// keep their original 1-9 meanings for compatibility even though the keyboard
// now groups the five git views behind the single top-level key 1. Labels use
// the grouped form so pickers describe where each number lands.
var viewChoices = []ViewChoice{
	{Number: 1, Mode: ui.ModeWorktrees, Label: "Git — Worktrees"},
	{Number: 2, Mode: ui.ModeBranches, Label: "Git — Branches"},
	{Number: 3, Mode: ui.ModeStashes, Label: "Git — Stashes"},
	{Number: 4, Mode: ui.ModeHistory, Label: "Git — History"},
	{Number: 5, Mode: ui.ModeReflog, Label: "Git — Reflog"},
	{Number: 6, Mode: ui.ModeSessions, Label: "Sessions"},
	{Number: 7, Mode: ui.ModePlans, Label: "Plans"},
	{Number: 8, Mode: ui.ModeFlows, Label: "Flows"},
	{Number: 9, Mode: ui.ModeActiveFlows, Label: "Active Flows"},
}

func ViewChoices() []ViewChoice {
	choices := make([]ViewChoice, len(viewChoices))
	copy(choices, viewChoices)
	return choices
}

func ModeForViewNumber(number int) (ui.Mode, bool) {
	for _, choice := range viewChoices {
		if choice.Number == number {
			return choice.Mode, true
		}
	}
	return ui.ModeWorktrees, false
}

func ViewNumber(mode ui.Mode) (int, bool) {
	for _, choice := range viewChoices {
		if choice.Mode == mode {
			return choice.Number, true
		}
	}
	return 0, false
}

func ViewChoiceLabel(mode ui.Mode) string {
	for _, choice := range viewChoices {
		if choice.Mode == mode {
			return fmt.Sprintf("%d %s", choice.Number, choice.Label)
		}
	}
	return "choose view"
}
