package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brian-bell/wtui/internal/version"
	"github.com/brian-bell/wtui/model"
	"github.com/brian-bell/wtui/scanner"
)

func main() {
	versionFlag := flag.Bool("version", false, "print version and exit")
	flag.BoolVar(versionFlag, "v", false, "print version and exit")
	flag.Parse()

	if *versionFlag {
		fmt.Println(version.String())
		return
	}

	root := os.Getenv("WORKTREE_ROOT")

	repos, err := scanner.Scan(scanner.ScanOptions{Root: root})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error scanning repos: %v\n", err)
		os.Exit(1)
	}

	p := tea.NewProgram(model.New(repos), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
