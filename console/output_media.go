package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"
)

const colabOneOutputMediaCloseTimeout = 2 * time.Second

func (d *ColabOneDesktop) ensureOutputMediaServer() (string, error) {
	d.mediaMu.Lock()
	defer d.mediaMu.Unlock()

	if d.mediaClosed || d.appQuitting.Load() {
		return "", newColabOneBridgeClosedError()
	}
	if d.mediaBaseURL != "" && d.mediaSrv != nil {
		return d.mediaBaseURL, nil
	}

	secretBytes := make([]byte, 32)
	if _, err := rand.Read(secretBytes); err != nil {
		return "", err
	}
	secret := hex.EncodeToString(secretBytes)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}

	prefix := "/media/" + secret + "/outputs/"
	handler := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		reqPath := r.URL.Path
		if !strings.HasPrefix(reqPath, "/media/") {
			http.NotFound(rw, r)
			return
		}
		rest := strings.TrimPrefix(reqPath, "/media/")
		slash := strings.IndexByte(rest, '/')
		if slash <= 0 {
			http.NotFound(rw, r)
			return
		}
		gotSecret := rest[:slash]
		if len(gotSecret) != len(secret) || subtle.ConstantTimeCompare([]byte(gotSecret), []byte(secret)) != 1 {
			http.NotFound(rw, r)
			return
		}
		tail := rest[slash:]
		if !strings.HasPrefix(tail, "/outputs/") {
			http.NotFound(rw, r)
			return
		}
		id := strings.TrimPrefix(tail, "/outputs/")
		if !workflowOutputIDPattern.MatchString(id) {
			http.NotFound(rw, r)
			return
		}
		if d.bridge == nil {
			http.Error(rw, colabOneT("err.bridgeNotInitialized"), http.StatusServiceUnavailable)
			return
		}
		d.bridge.serveOutputMedia(r.Context(), rw, r, id)
	})

	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}

	d.mediaLn = ln
	d.mediaSrv = srv
	d.mediaSecret = secret
	d.mediaBaseURL = "http://" + ln.Addr().String() + prefix

	go func() {
		_ = srv.Serve(ln)
	}()

	return d.mediaBaseURL, nil
}

func (d *ColabOneDesktop) closeOutputMediaServer() {
	d.closeOutputMediaServerWithTimeout(colabOneOutputMediaCloseTimeout)
}

func (d *ColabOneDesktop) closeOutputMediaServerWithTimeout(timeout time.Duration) {
	d.mediaMu.Lock()
	d.mediaClosed = true
	srv := d.mediaSrv
	ln := d.mediaLn
	d.mediaSrv = nil
	d.mediaLn = nil
	d.mediaSecret = ""
	d.mediaBaseURL = ""
	d.mediaMu.Unlock()

	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			_ = srv.Close()
		}
	}
	if ln != nil {
		_ = ln.Close()
	}
}

// OutputPreviewURL validates that the workflow output can be streamed (from
// Google Drive or the active remote GPU) and returns a loopback HTTP Range
// streaming URL so video and audio outputs preview without full-file download.
func (d *ColabOneDesktop) OutputPreviewURL(ctx context.Context, id string) (string, error) {
	cleanID := strings.ToLower(strings.TrimSpace(id))
	if !workflowOutputIDPattern.MatchString(cleanID) {
		return "", errors.New(colabOneT("err.invalidOutputID"))
	}
	if d.bridge == nil {
		return "", errors.New(colabOneT("err.bridgeNotInitialized"))
	}
	if _, err := d.bridge.resolveOutputMedia(ctx, cleanID); err != nil {
		return "", err
	}
	baseURL, err := d.ensureOutputMediaServer()
	if err != nil {
		return "", err
	}
	return baseURL + cleanID, nil
}
