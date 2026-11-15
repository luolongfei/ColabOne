package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindRootFromPortableMacBundle(t *testing.T) {
	root := t.TempDir()
	err := os.MkdirAll(filepath.Join(root, "bridge"), 0700)
	if err != nil {
		t.Fatal(err)
	}
	err = os.WriteFile(filepath.Join(root, "bridge/main.py"), []byte("# fixture"), 0600)
	if err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(root, "ColabOne.app/Contents/MacOS/ColabOne")
	got, err := rootAt(exe, t.TempDir())
	if err != nil || got != root {
		t.Fatalf("root=%q err=%v", got, err)
	}
}
func TestExternalURLsAreRestricted(t *testing.T) {
	d := Desktop{}
	for _, url := range []string{"file:///etc/passwd", "http://example.com", "https://evil.test", "javascript:alert(1)"} {
		if d.OpenURL(url) == nil {
			t.Fatalf("accepted %s", url)
		}
	}
}
