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

var viewChoices = []ViewChoice{
	{Number: 1, Mode: ui.ModeWorktrees, Label: "worktrees"},
	{Number: 2, Mode: ui.ModeBranches, Label: "branches"},
	{Number: 3, Mode: ui.ModeStashes, Label: "stashes"},
	{Number: 4, Mode: ui.ModeHistory, Label: "history"},
	{Number: 5, Mode: ui.ModeReflog, Label: "reflog"},
	{Number: 6, Mode: ui.ModeSessions, Label: "sessions"},
	{Number: 7, Mode: ui.ModePlans, Label: "plans"},
	{Number: 8, Mode: ui.ModeFlows, Label: "flows"},
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
