package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/luolongfei/ColabOne/console/internal/controller"
)

func TestOutputPreviewURLLoopbackServerSecurityAndRangeStreaming(t *testing.T) {
	videoID := "abcdef0123456789abcdef0123456789"
	missingID := "11111111111111111111111111111111"
	videoBytes := []byte("0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ-colabone-video-stream")

	d := &ColabOneDesktop{
		bridge: &colabOneBridge{
			resolveMediaFn: func(ctx context.Context, id string) (*controller.ColabOneOutputMediaMeta, error) {
				if id != videoID {
					return nil, errors.New("作品不存在")
				}
				return &controller.ColabOneOutputMediaMeta{
					ID:        id,
					Size:      int64(len(videoBytes)),
					Mime:      "video/mp4",
					Extension: "mp4",
					Filename:  "ColabOne-abcdef01.mp4",
					Source:    "drive",
				}, nil
			},
			serveMediaFn: func(ctx context.Context, rw http.ResponseWriter, r *http.Request, id string) {
				if id != videoID {
					http.NotFound(rw, r)
					return
				}
				rw.Header().Set("Content-Type", "video/mp4")
				rw.Header().Set("Accept-Ranges", "bytes")
				rng := r.Header.Get("Range")
				if strings.HasPrefix(rng, "bytes=") {
					var start, end int
					if _, err := fmt.Sscanf(rng, "bytes=%d-%d", &start, &end); err == nil {
						if end >= len(videoBytes) {
							end = len(videoBytes) - 1
						}
						rw.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(videoBytes)))
						rw.WriteHeader(http.StatusPartialContent)
						_, _ = rw.Write(videoBytes[start : end+1])
						return
					}
				}
				rw.WriteHeader(http.StatusOK)
				_, _ = rw.Write(videoBytes)
			},
		},
	}
	defer d.closeOutputMediaServer()

	// Invalid ID rejected before starting server.
	if _, err := d.OutputPreviewURL(context.Background(), "../bad"); err == nil {
		t.Fatal("expected error for invalid output ID")
	}

	// Missing output surfaces controller error cleanly.
	if _, err := d.OutputPreviewURL(context.Background(), missingID); err == nil || !strings.Contains(err.Error(), "作品不存在") {
		t.Fatalf("expected controller error for missing output, got %v", err)
	}

	previewURL, err := d.OutputPreviewURL(context.Background(), videoID)
	if err != nil {
		t.Fatalf("OutputPreviewURL failed: %v", err)
	}
	if !strings.HasPrefix(previewURL, "http://127.0.0.1:") || !strings.HasSuffix(previewURL, "/outputs/"+videoID) {
		t.Fatalf("unexpected preview URL: %q", previewURL)
	}

	// Calling OutputPreviewURL again reuses the running loopback server.
	previewURL2, err := d.OutputPreviewURL(context.Background(), videoID)
	if err != nil || previewURL2 != previewURL {
		t.Fatalf("expected reused preview URL %q, got %q err=%v", previewURL, previewURL2, err)
	}

	// Wrong secret token must get 404.
	badTokenURL := strings.Replace(previewURL, d.mediaSecret, strings.Repeat("0", len(d.mediaSecret)), 1)
	respBad, err := http.Get(badTokenURL)
	if err != nil {
		t.Fatalf("GET badTokenURL failed: %v", err)
	}
	respBad.Body.Close()
	if respBad.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for wrong token, got %d", respBad.StatusCode)
	}

	// Invalid ID path must get 404.
	badIDURL := strings.TrimSuffix(previewURL, videoID) + "not-hex"
	respBadID, err := http.Get(badIDURL)
	if err != nil {
		t.Fatalf("GET badIDURL failed: %v", err)
	}
	respBadID.Body.Close()
	if respBadID.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for invalid ID path, got %d", respBadID.StatusCode)
	}

	// Valid Range request returns exact 206 Partial Content slice.
	req, err := http.NewRequest(http.MethodGet, previewURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Range", "bytes=10-25")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET previewURL failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("expected 206, got %d body=%q", resp.StatusCode, string(body))
	}
	if resp.Header.Get("Content-Type") != "video/mp4" || resp.Header.Get("Content-Range") != "bytes 10-25/58" {
		t.Fatalf("unexpected headers: %#v", resp.Header)
	}
	if !bytes.Equal(body, videoBytes[10:26]) {
		t.Fatalf("body=%q, want %q", string(body), string(videoBytes[10:26]))
	}
}
