package model

import "github.com/brian-bell/wtui/embeddedterm"

func NewRealEmbeddedTerminalForTest(term *embeddedterm.Terminal) EmbeddedTerminal {
	return realEmbeddedTerminal{term: term}
}

func SetSearchActiveForTest(m Model, active bool) Model {
	m.searchActive = active
	return m
}
