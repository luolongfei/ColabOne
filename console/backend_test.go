package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
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
	got, err := colabOneRootAt(exe, t.TempDir())
	if err != nil || got != root {
		t.Fatalf("root=%q err=%v", got, err)
	}
}

func TestLargeConversationReplyDoesNotHitScannerLimit(t *testing.T) {
	body := `{"id":1,"result":"` + strings.Repeat("x", 17<<20) + `"}` + "\n" + `{"id":2,"result":{"ok":true}}` + "\n"
	var ids []uint64
	err := readColabOneReplies(strings.NewReader(body), func(reply colabOneRPCReply) {
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
	if readColabOneReplies(strings.NewReader("broken protocol\n"), func(colabOneRPCReply) { t.Fatal("unexpected reply") }) == nil {
		t.Fatal("invalid protocol accepted")
	}
}
func TestExternalURLsAreRestricted(t *testing.T) {
	d := ColabOneDesktop{}
	for _, url := range []string{"file:///etc/passwd", "http://example.com", "https://evil.test", "javascript:alert(1)"} {
		if d.OpenURL(url) == nil {
			t.Fatalf("accepted %s", url)
		}
	}
}

func TestValidateCanvasURL(t *testing.T) {
	valid := "https://comfy.example.com/_colabone_auth?token=abc"
	u, err := validateColabOneCanvasURL(valid)
	if err != nil {
		t.Fatalf("expected valid canvas URL %q, got error: %v", valid, err)
	}
	if u.Hostname() != "comfy.example.com" || u.Path != "/_colabone_auth" || u.Query().Get("token") != "abc" {
		t.Fatalf("unexpected parsed URL: %+v", u)
	}

	invalid := []string{
		"",
		"http://comfy.example.com/_colabone_auth?token=abc",
		"file:///etc/passwd",
		"javascript:alert(1)",
		"https://comfy.example.com/_colabone_auth",
		"https://comfy.example.com/_colabone_auth?token=",
		"https://comfy.example.com/wrong?token=abc",
		"https://foo.trycloudflare.com/_colabone_auth?token=abc",
		"https://trycloudflare.com/_colabone_auth?token=abc",
		"https://localhost/_colabone_auth?token=abc",
		"https://127.0.0.1/_colabone_auth?token=abc",
		"https://[::1]/_colabone_auth?token=abc",
		"https:///_colabone_auth?token=abc",
	}
	for _, raw := range invalid {
		if _, err := validateColabOneCanvasURL(raw); err == nil {
			t.Fatalf("accepted invalid canvas URL %q", raw)
		}
	}
}

func TestOpenComfyCanvasRejectsInvalidTarget(t *testing.T) {
	d := ColabOneDesktop{}
	if err := d.OpenComfyCanvas(context.Background(), "shell"); err == nil {
		t.Fatal("accepted invalid canvas target \"shell\"")
	}
}

func TestExportWorkflowOutputRejectsInvalidID(t *testing.T) {
	d := ColabOneDesktop{}
	for _, id := range []string{"", "not-hex", "../etc/passwd", "ABCDEF0123456789abcdef0123456789"} {
		if _, err := d.ExportWorkflowOutput(context.Background(), id); err == nil {
			t.Fatalf("accepted invalid output id %q", id)
		}
	}
}

func TestWoff2MimeTypeRegistered(t *testing.T) {
	if got := mime.TypeByExtension(".woff2"); !strings.HasPrefix(got, "font/woff2") {
		t.Fatalf("expected .woff2 MIME type font/woff2, got %q", got)
	}
}
