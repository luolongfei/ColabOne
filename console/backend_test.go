package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
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

func TestLargeConversationReplyDoesNotHitScannerLimit(t *testing.T) {
	body := `{"id":1,"result":"` + strings.Repeat("x", 17<<20) + `"}` + "\n" + `{"id":2,"result":{"ok":true}}` + "\n"
	var ids []uint64
	err := readReplies(strings.NewReader(body), func(reply rpcReply) {
		if !json.Valid(reply.Result) {
			t.Fatal("invalid reply")
		}
		ids = append(ids, reply.ID)
	})
	if !errors.Is(err, io.EOF) || len(ids) != 2 || ids[1] != 2 {
		t.Fatalf("ids=%v err=%v", ids, err)
	}
}

func TestInvalidBridgeProtocolReturnsError(t *testing.T) {
	if readReplies(strings.NewReader("broken protocol\n"), func(rpcReply) { t.Fatal("unexpected reply") }) == nil {
		t.Fatal("invalid protocol accepted")
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
