package model

import "github.com/charmbracelet/x/ansi"

func viewContent(m Model) string {
	return ansi.Strip(m.View().Content)
}
