package model_test

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/approachcontrol/approach/flowstore"
	"github.com/approachcontrol/approach/model"
)

func TestModel_CtrlGTogglesGlobalAutoMergeOnlyAfterSaveSucceeds(t *testing.T) {
	var saves []bool
	m := newTestModel(testRepos(), model.Options{
		SaveFlowAutoMerge: func(enabled bool) error {
			saves = append(saves, enabled)
			return nil
		},
	})

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyCtrlG})
	if cmd == nil || len(saves) != 0 {
		t.Fatalf("ctrl+g command = %v saves = %v, want deferred save", cmd != nil, saves)
	}
	msg := cmd()
	if len(saves) != 1 || !saves[0] {
		t.Fatalf("saves = %v, want enable", saves)
	}
	m, _ = update(m, msg)
	_, cmd = update(m, tea.KeyMsg{Type: tea.KeyCtrlG})
	if cmd == nil {
		t.Fatal("second ctrl+g returned no save command")
	}
	_ = cmd()
	if len(saves) != 2 || saves[1] {
		t.Fatalf("saves = %v, want enable then disable", saves)
	}
}

func TestModel_CtrlGCoalescesOverlappingTogglesInOrder(t *testing.T) {
	var saves []bool
	m := newTestModel(testRepos(), model.Options{
		SaveFlowAutoMerge: func(enabled bool) error {
			saves = append(saves, enabled)
			return nil
		},
	})

	m, first := update(m, tea.KeyMsg{Type: tea.KeyCtrlG})
	m, second := update(m, tea.KeyMsg{Type: tea.KeyCtrlG})
	if first == nil || second != nil {
		t.Fatalf("overlapping toggle commands = first %v second %v, want one serialized write", first != nil, second != nil)
	}
	var followup tea.Cmd
	m, followup = update(m, first())
	if followup == nil {
		t.Fatal("first completion did not start coalesced disable")
	}
	for _, msg := range testCommandMessages(followup) {
		m, _ = update(m, msg)
	}
	if len(saves) != 2 || !saves[0] || saves[1] {
		t.Fatalf("serialized saves = %v, want enable then disable", saves)
	}

	_, next := update(m, tea.KeyMsg{Type: tea.KeyCtrlG})
	if next == nil {
		t.Fatal("settled off value did not accept a new enable toggle")
	}
	_ = next()
	if len(saves) != 3 || !saves[2] {
		t.Fatalf("next save = %v, want enable after settled off", saves)
	}
}

func TestModel_FailedGlobalAutoMergeSaveLeavesPriorValueInForce(t *testing.T) {
	m := inRightPane(newTestModel(testRepos(), model.Options{
		SaveFlowAutoMerge: func(bool) error { return errors.New("config locked") },
	}))
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyCtrlG})
	m, _ = update(m, cmd())
	if !strings.Contains(m.View(), "config locked") {
		t.Fatalf("failed save status missing from view:\n%s", m.View())
	}
}

func TestModel_GCyclesFlowAutoMergeOverride(t *testing.T) {
	record := flowWithPhaseDetails()
	current := record
	var writes []*bool
	m := newTestModel(testRepos(), model.Options{
		SetFlowAutoMerge: func(update flowstore.AutoMergeUpdate) (flowstore.FlowRecord, error) {
			if update.Enabled == nil {
				writes = append(writes, nil)
				current.AutoMerge = nil
			} else {
				value := *update.Enabled
				writes = append(writes, &value)
				current.AutoMerge = &value
			}
			return current, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{record})

	for range 3 {
		var cmd tea.Cmd
		m, cmd = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}})
		if cmd == nil {
			t.Fatal("G returned no mutation command")
		}
		m, _ = update(m, cmd())
	}
	if len(writes) != 3 || writes[0] == nil || !*writes[0] || writes[1] == nil || *writes[1] || writes[2] != nil {
		t.Fatalf("override writes = %v, want on, off, inherit", formatBoolPointers(writes))
	}
}

func TestModel_GCoalescesOverlappingOverrideCycles(t *testing.T) {
	record := flowWithPhaseDetails()
	current := record
	var writes []*bool
	m := newTestModel(testRepos(), model.Options{
		SetFlowAutoMerge: func(update flowstore.AutoMergeUpdate) (flowstore.FlowRecord, error) {
			writes = append(writes, cloneTestBoolPointer(update.Enabled))
			current.AutoMerge = cloneTestBoolPointer(update.Enabled)
			return current, nil
		},
	})
	m = flowsInRightPane(t, m, []flowstore.FlowRecord{record})

	m, first := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}})
	m, second := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}})
	if first == nil || second != nil {
		t.Fatalf("overlapping G commands = first %v second %v, want one serialized write", first != nil, second != nil)
	}
	m, followup := update(m, first())
	if followup == nil {
		t.Fatal("first override completion did not start coalesced off write")
	}
	m, _ = update(m, followup())
	if len(writes) != 2 || writes[0] == nil || !*writes[0] || writes[1] == nil || *writes[1] {
		t.Fatalf("override writes = %v, want on then off", formatBoolPointers(writes))
	}
}

func cloneTestBoolPointer(value *bool) *bool {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func formatBoolPointers(values []*bool) []string {
	formatted := make([]string, len(values))
	for i, value := range values {
		if value == nil {
			formatted[i] = "inherit"
		} else if *value {
			formatted[i] = "on"
		} else {
			formatted[i] = "off"
		}
	}
	return formatted
}
