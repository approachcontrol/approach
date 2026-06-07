package pane

import "testing"

type testItem struct {
	name  string
	lines int
}

func fixedTestPane(items []testItem) Pane[testItem] {
	return New(func(item testItem) string {
		return item.name
	}, func(testItem, int) int {
		return 1
	}).SetItems(items)
}

func TestPaneMoveWrapsAndKeepsFixedHeightSelectionVisible(t *testing.T) {
	items := []testItem{
		{name: "one"},
		{name: "two"},
		{name: "three"},
		{name: "four"},
	}
	p := fixedTestPane(items)

	p = p.Move(-1, 2, 80)
	_, selected, scroll := p.View()
	if selected != 3 {
		t.Fatalf("expected selection to wrap to 3, got %d", selected)
	}
	if scroll != 2 {
		t.Fatalf("expected scroll to show wrapped selection, got %d", scroll)
	}

	p = p.Move(1, 2, 80)
	_, selected, scroll = p.View()
	if selected != 0 {
		t.Fatalf("expected selection to wrap to 0, got %d", selected)
	}
	if scroll != 0 {
		t.Fatalf("expected scroll to return to top, got %d", scroll)
	}
}

func TestPaneQueryFiltersWithCaseInsensitiveFuzzyAndMultiTokenAnd(t *testing.T) {
	items := []testItem{
		{name: "feature/auth /repos/auth"},
		{name: "bugfix/ui /repos/web"},
		{name: "release notes /repos/docs"},
	}
	p := fixedTestPane(items)

	p = p.SetQuery("FA")
	filtered, selected, scroll := p.View()
	if len(filtered) != 1 || filtered[0].name != "feature/auth /repos/auth" {
		t.Fatalf("expected fuzzy query to match feature/auth, got %#v", filtered)
	}
	if selected != 0 || scroll != 0 {
		t.Fatalf("query should reset cursor and scroll, got selected=%d scroll=%d", selected, scroll)
	}

	p = p.SetQuery("bug web")
	filtered, _, _ = p.View()
	if len(filtered) != 1 || filtered[0].name != "bugfix/ui /repos/web" {
		t.Fatalf("expected multi-token query to require both tokens, got %#v", filtered)
	}

	p = p.SetQuery("missing")
	filtered, selected, scroll = p.View()
	if len(filtered) != 0 {
		t.Fatalf("expected no matches, got %#v", filtered)
	}
	if selected != 0 || scroll != 0 {
		t.Fatalf("empty filtered view should clamp to top, got selected=%d scroll=%d", selected, scroll)
	}
}

func TestPaneSetItemsClampsSelectionAndSelectFuncFindsFilteredItem(t *testing.T) {
	items := []testItem{
		{name: "alpha"},
		{name: "bravo"},
		{name: "charlie"},
	}
	p := fixedTestPane(items).Move(2, 2, 80)

	p = p.SetItems(items[:1])
	filtered, selected, scroll := p.View()
	if len(filtered) != 1 || selected != 0 || scroll != 0 {
		t.Fatalf("expected replacement to clamp to the only item, len=%d selected=%d scroll=%d", len(filtered), selected, scroll)
	}

	p = fixedTestPane(items).SetQuery("a").SelectFunc(func(item testItem) bool {
		return item.name == "charlie"
	})
	selectedItem, ok := p.Selected()
	if !ok || selectedItem.name != "charlie" {
		t.Fatalf("expected SelectFunc to select charlie, got item=%#v ok=%v", selectedItem, ok)
	}
	_, selected, _ = p.View()
	if selected != 2 {
		t.Fatalf("expected charlie at filtered index 2, got %d", selected)
	}
}

func TestPaneSetQueryPreserveIndexKeepsCursorWhenPossible(t *testing.T) {
	items := []testItem{
		{name: "alpha feature"},
		{name: "bravo feature"},
		{name: "charlie bugfix"},
	}
	p := fixedTestPane(items).Move(1, 2, 80)

	p = p.SetQueryPreserveIndex("feature")
	selectedItem, ok := p.Selected()
	if !ok || selectedItem.name != "bravo feature" {
		t.Fatalf("expected preserved cursor on bravo feature, got item=%#v ok=%v", selectedItem, ok)
	}
	_, selected, _ := p.View()
	if selected != 1 {
		t.Fatalf("expected selected index 1 after preserved query, got %d", selected)
	}

	p = p.SetQueryPreserveIndex("alpha")
	selectedItem, ok = p.Selected()
	if !ok || selectedItem.name != "alpha feature" {
		t.Fatalf("expected cursor to clamp to alpha feature, got item=%#v ok=%v", selectedItem, ok)
	}
}

func TestPaneItemCountReportsSourceItemsWhenFilterHasNoMatches(t *testing.T) {
	items := []testItem{
		{name: "alpha"},
		{name: "bravo"},
	}
	p := fixedTestPane(items).SetQuery("zzz")
	filtered, _, _ := p.View()

	if len(filtered) != 0 {
		t.Fatalf("expected no filtered matches, got %#v", filtered)
	}
	if p.ItemCount() != 2 {
		t.Fatalf("expected source item count 2, got %d", p.ItemCount())
	}
}

func TestPaneMoveOnEmptyAndSingleItemLists(t *testing.T) {
	var empty Pane[testItem]
	empty = empty.Move(1, 2, 80)
	if _, selected, scroll := empty.View(); selected != 0 || scroll != 0 {
		t.Fatalf("empty pane should stay at top, selected=%d scroll=%d", selected, scroll)
	}

	p := fixedTestPane([]testItem{{name: "only"}})
	p = p.Move(1, 2, 80).Move(-1, 2, 80)
	if _, selected, scroll := p.View(); selected != 0 || scroll != 0 {
		t.Fatalf("single item pane should stay at top, selected=%d scroll=%d", selected, scroll)
	}
}

func TestPaneVariableHeightScrollUsesVisualLines(t *testing.T) {
	items := []testItem{
		{name: "short", lines: 1},
		{name: "long", lines: 4},
		{name: "target", lines: 1},
	}
	p := New(func(item testItem) string {
		return item.name
	}, func(item testItem, width int) int {
		return item.lines
	}).SetItems(items)

	p = p.Move(2, 3, 80)
	_, selected, scroll := p.View()
	if selected != 2 {
		t.Fatalf("expected selected index 2, got %d", selected)
	}
	if scroll != 3 {
		t.Fatalf("expected scroll to account for prior 5 visual lines, got %d", scroll)
	}

	p = p.Reflow(6, 80)
	_, _, scroll = p.View()
	if scroll != 0 {
		t.Fatalf("larger viewport should clamp scroll when all lines fit, got %d", scroll)
	}

	p = p.Move(-1, 3, 80)
	_, selected, scroll = p.View()
	if selected != 1 {
		t.Fatalf("expected selected index 1, got %d", selected)
	}
	if scroll != 0 {
		t.Fatalf("selected row first line is visible, so scroll should stay at 0, got %d", scroll)
	}
}

func TestPaneScrollByAndSetItemHeightClampToVisualBounds(t *testing.T) {
	items := []testItem{{name: "expanded", lines: 1}}
	p := fixedTestPane(items)
	p = p.SetItemHeight(func(testItem, int) int {
		return 4
	})

	p = p.ScrollBy(2, 3, 80)
	_, selected, scroll := p.View()
	if selected != 0 {
		t.Fatalf("expected selection unchanged, got %d", selected)
	}
	if scroll != 1 {
		t.Fatalf("expected scroll clamped to total lines minus viewport, got %d", scroll)
	}
}

func TestPaneReflowClampsScrollAfterViewportShrink(t *testing.T) {
	items := []testItem{
		{name: "one", lines: 2},
		{name: "two", lines: 2},
		{name: "three", lines: 2},
	}
	p := New(func(item testItem) string {
		return item.name
	}, func(item testItem, width int) int {
		return item.lines
	}).SetItems(items)

	p = p.Move(2, 5, 80)
	_, _, scroll := p.View()
	if scroll != 0 {
		t.Fatalf("expected selected item to fit in tall viewport without scrolling, got %d", scroll)
	}

	p = p.Reflow(1, 80)
	_, selected, scroll := p.View()
	if selected != 2 {
		t.Fatalf("expected selection to remain on index 2, got %d", selected)
	}
	if scroll != 4 {
		t.Fatalf("expected shrink to scroll to selected visual line 4, got %d", scroll)
	}

	p = p.Move(10, 1, 80)
	_, _, scroll = p.View()
	if scroll < 0 || scroll > 5 {
		t.Fatalf("scroll should stay within visual bounds, got %d", scroll)
	}
}

func TestPaneReflowClampsScrollToTotalLinesMinusViewport(t *testing.T) {
	items := []testItem{
		{name: "one", lines: 2},
		{name: "two", lines: 2},
		{name: "three", lines: 2},
	}
	p := New(func(item testItem) string {
		return item.name
	}, func(item testItem, width int) int {
		return item.lines
	}).SetItems(items)

	p = p.Move(2, 1, 80)
	_, _, scroll := p.View()
	if scroll != 4 {
		t.Fatalf("expected narrow viewport to scroll to selected visual line 4, got %d", scroll)
	}

	p = p.Reflow(5, 80)
	_, _, scroll = p.View()
	if scroll != 1 {
		t.Fatalf("expected scroll to clamp to total lines minus viewport, got %d", scroll)
	}
}
