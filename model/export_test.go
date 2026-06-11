package model

import "github.com/brian-bell/wtui/embeddedterm"

func NewRealEmbeddedTerminalForTest(term *embeddedterm.Terminal) EmbeddedTerminal {
	return realEmbeddedTerminal{term: term}
}
