package ui

import (
	"net/url"
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// hyperlink wraps display text that has already been made terminal-safe.
func hyperlink(label, target string) string {
	if label == "" || target == "" {
		return label
	}
	return ansi.SetHyperlink(target) + label + ansi.ResetHyperlink()
}

func hyperlinkColumn(label, target string, width int, style lipgloss.Style) string {
	label = truncateToWidth(label, width)
	cell := hyperlink(style.Render(label), target)
	if padding := width - ansi.StringWidth(label); padding > 0 {
		cell += style.Render(strings.Repeat(" ", padding))
	}
	return cell
}

func prHyperlinkTarget(raw string) string {
	raw = strings.TrimSpace(raw)
	target, err := url.Parse(raw)
	if err != nil || target.Host == "" || (target.Scheme != "http" && target.Scheme != "https") {
		return ""
	}
	return target.String()
}

func fileHyperlinkTarget(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || !filepath.IsAbs(path) {
		return ""
	}
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(filepath.Clean(path))}).String()
}

// beadHyperlinkTarget keeps the repo qualification explicit and centralized.
// The custom URI is intended for terminal handlers that know how to open a
// Bead in its repository.
func beadHyperlinkTarget(repoPath, beadID string) string {
	repoPath = strings.TrimSpace(repoPath)
	beadID = strings.TrimSpace(beadID)
	if repoPath == "" || beadID == "" || !filepath.IsAbs(repoPath) {
		return ""
	}
	query := url.Values{}
	query.Set("id", beadID)
	query.Set("repo", filepath.Clean(repoPath))
	return "bead://open?" + query.Encode()
}
