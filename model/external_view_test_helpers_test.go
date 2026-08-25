package model_test

import (
	"github.com/charmbracelet/x/ansi"

	"github.com/approachcontrol/approach/model"
)

func viewContent(m model.Model) string {
	return ansi.Strip(m.View().Content)
}
