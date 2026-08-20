package scanner

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSamePathTreatsSymlinkAndTargetAsEqual(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if !samePath(target, link) {
		t.Fatalf("samePath(%q, %q) = false, want true", target, link)
	}
}

func TestSamePathTreatsHardlinkedSpellingsAsEqual(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	if err := os.WriteFile(a, []byte("same"), 0o644); err != nil {
		t.Fatal(err)
	}
	b := filepath.Join(dir, "b")
	if err := os.Link(a, b); err != nil {
		t.Fatal(err)
	}
	if !samePath(a, b) {
		t.Fatalf("samePath(%q, %q) = false, want true for hardlinked spellings", a, b)
	}
}
