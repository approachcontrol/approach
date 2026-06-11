package pane

import (
	"strings"
	"unicode"
)

// SearchText returns the haystack one item is fuzzy-matched against.
type SearchText[T any] func(item T) string

// ItemHeight returns how many visual lines an item occupies at a given content
// width.
type ItemHeight[T any] func(item T, width int) int

// Pane is a filtered, scrollable, single-selection list. It is a value type:
// mutators return a new Pane.
type Pane[T any] struct {
	items    []T
	filtered []T
	selected int
	scroll   int
	query    string
	search   SearchText[T]
	height   ItemHeight[T]
}

// New creates an empty Pane.
func New[T any](search SearchText[T], height ItemHeight[T]) Pane[T] {
	return Pane[T]{search: search, height: height}
}

// SetItems replaces the source items and refreshes the filtered view.
func (p Pane[T]) SetItems(items []T) Pane[T] {
	p.items = items
	p = p.refilter()
	p = p.clamp()
	return p
}

// SetQuery updates the fuzzy filter query and resets selection to the top.
func (p Pane[T]) SetQuery(q string) Pane[T] {
	p.query = q
	p.selected = 0
	p.scroll = 0
	p = p.refilter()
	return p.clamp()
}

// SetQueryPreserveIndex updates the fuzzy filter query while keeping the
// current cursor index when it still fits the new filtered view.
func (p Pane[T]) SetQueryPreserveIndex(q string) Pane[T] {
	p.query = q
	p = p.refilter()
	return p.clamp()
}

// Move advances the selection by delta, wrapping around, and keeps it visible.
func (p Pane[T]) Move(delta, viewHeight, viewWidth int) Pane[T] {
	if len(p.filtered) == 0 {
		return p
	}
	p.selected = ((p.selected+delta)%len(p.filtered) + len(p.filtered)) % len(p.filtered)
	return p.Reflow(viewHeight, viewWidth)
}

// ScrollBy adjusts the visual-line scroll offset without changing selection.
func (p Pane[T]) ScrollBy(delta, viewHeight, viewWidth int) Pane[T] {
	p.scroll += delta
	return p.clampScroll(viewHeight, viewWidth)
}

// SetItemHeight replaces the visual height function used for scrolling.
func (p Pane[T]) SetItemHeight(height ItemHeight[T]) Pane[T] {
	p.height = height
	return p
}

// SelectFunc selects the first filtered item matching pred.
func (p Pane[T]) SelectFunc(pred func(T) bool) Pane[T] {
	for i, item := range p.filtered {
		if pred(item) {
			p.selected = i
			return p
		}
	}
	return p
}

// ResetSelection moves the cursor and scroll offset to the top without
// changing items or query.
func (p Pane[T]) ResetSelection() Pane[T] {
	p.selected = 0
	p.scroll = 0
	return p.clamp()
}

// Selected returns the selected filtered item.
func (p Pane[T]) Selected() (T, bool) {
	var zero T
	if p.selected < 0 || p.selected >= len(p.filtered) {
		return zero, false
	}
	return p.filtered[p.selected], true
}

// Len returns the number of filtered items.
func (p Pane[T]) Len() int {
	return len(p.filtered)
}

// ItemCount returns the number of source items before filtering.
func (p Pane[T]) ItemCount() int {
	return len(p.items)
}

// Items returns the source items before filtering.
func (p Pane[T]) Items() []T {
	return p.items
}

// SelectedIndex returns the cursor position within the filtered view.
func (p Pane[T]) SelectedIndex() int {
	return p.selected
}

// Scroll returns the current visual-line scroll offset.
func (p Pane[T]) Scroll() int {
	return p.scroll
}

// Query returns the active fuzzy filter query.
func (p Pane[T]) Query() string {
	return p.query
}

// Reflow keeps the selected item visible for the current viewport dimensions.
func (p Pane[T]) Reflow(viewHeight, viewWidth int) Pane[T] {
	if viewHeight <= 0 {
		viewHeight = 1
	}
	if len(p.filtered) == 0 {
		p.scroll = 0
		return p
	}

	line := 0
	for i, item := range p.filtered {
		if i == p.selected {
			break
		}
		line += p.itemHeight(item, viewWidth)
	}
	if p.scroll > line {
		p.scroll = line
	}
	if line >= p.scroll+viewHeight {
		p.scroll = line - viewHeight + 1
	}
	return p.clampScroll(viewHeight, viewWidth)
}

// View returns the filtered items, selected index, and visual line scroll.
func (p Pane[T]) View() ([]T, int, int) {
	return p.filtered, p.selected, p.scroll
}

func (p Pane[T]) refilter() Pane[T] {
	if strings.TrimSpace(p.query) == "" {
		p.filtered = p.items
		return p
	}
	p.filtered = nil
	for _, item := range p.items {
		if p.search != nil && fuzzyMatch(p.query, p.search(item)) {
			p.filtered = append(p.filtered, item)
		}
	}
	return p
}

func (p Pane[T]) clamp() Pane[T] {
	if len(p.filtered) == 0 {
		p.selected = 0
		p.scroll = 0
		return p
	}
	if p.selected < 0 {
		p.selected = 0
	}
	if p.selected >= len(p.filtered) {
		p.selected = len(p.filtered) - 1
	}
	return p.clampScroll(1, 0)
}

func (p Pane[T]) clampScroll(viewHeight, width int) Pane[T] {
	if viewHeight <= 0 {
		viewHeight = 1
	}
	maxScroll := max(p.totalHeight(width)-viewHeight, 0)
	p.scroll = min(max(p.scroll, 0), maxScroll)
	return p
}

func (p Pane[T]) totalHeight(width int) int {
	total := 0
	for _, item := range p.filtered {
		total += p.itemHeight(item, width)
	}
	return total
}

func (p Pane[T]) itemHeight(item T, width int) int {
	if p.height == nil {
		return 1
	}
	height := p.height(item, width)
	if height < 1 {
		return 1
	}
	return height
}

func fuzzyMatch(query, target string) bool {
	query = strings.TrimSpace(query)
	if query == "" {
		return true
	}

	if fuzzyMatchRunes(query, target) {
		return true
	}

	tokens := strings.FieldsFunc(query, func(r rune) bool { return unicode.IsSpace(r) })
	if len(tokens) <= 1 {
		return false
	}
	for _, token := range tokens {
		if !fuzzyMatchRunes(token, target) {
			return false
		}
	}
	return true
}

func fuzzyMatchRunes(query, target string) bool {
	q := []rune(strings.ToLower(query))
	next := 0
	for _, r := range strings.ToLower(target) {
		if next < len(q) && q[next] == r {
			next++
		}
	}
	return next == len(q)
}
