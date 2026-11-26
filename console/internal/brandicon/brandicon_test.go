package brandicon

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmbeddedBrandIconMatchesMasterResourcesIcon(t *testing.T) {
	masterPath := filepath.Join("..", "..", "resources", "icon.svg")
	masterBytes, err := os.ReadFile(masterPath)
	if err != nil {
		t.Fatalf("read master icon.svg: %v", err)
	}
	if !bytes.Equal(RawSVGBytes(), masterBytes) {
		t.Fatalf("console/internal/brandicon/icon.svg is out of sync with console/resources/icon.svg; run `npm --prefix frontend run icons`")
	}

	expectedTrimmed := TrimIconSVG(masterBytes)
	if !bytes.Equal(SVGBytes(), expectedTrimmed) {
		t.Fatalf("console/internal/brandicon/favicon.svg is out of sync with trimmed console/resources/icon.svg; run `npm --prefix frontend run icons`")
	}
	if !bytes.Contains(SVGBytes(), []byte(`viewBox="120 120 1014 1014"`)) {
		t.Fatalf("trimmed SVG missing expected edge-to-edge viewBox")
	}
	if !bytes.Contains(SVGBytes(), []byte(`<title>ColabOne</title>`)) {
		t.Fatalf("trimmed SVG missing ColabOne title")
	}
}

func TestBrandIconDataURIAndFallback(t *testing.T) {
	const prefix = "data:image/svg+xml;base64,"
	uri := DataURI()
	if !strings.HasPrefix(uri, prefix) {
		t.Fatalf("DataURI missing prefix %q", prefix)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(uri, prefix))
	if err != nil {
		t.Fatalf("base64 decode DataURI: %v", err)
	}
	if !bytes.Equal(decoded, SVGBytes()) {
		t.Fatalf("decoded DataURI does not match SVGBytes")
	}

	fallback := FallbackDataURI()
	for i, invalid := range [][]byte{
		nil,
		{},
		[]byte("not an svg"),
		{0xff, 0xfe, '<', 's', 'v', 'g', '>'},
	} {
		if got := DataURIFromSVGBytes(invalid); got != fallback {
			t.Errorf("invalid input #%d did not fall back to default SVG URI", i)
		}
	}
}

func TestEmbeddedFaviconICOAndAppleTouchIconPNG(t *testing.T) {
	ico := FaviconICOBytes()
	if len(ico) < 22 {
		t.Fatalf("favicon.ico too short: %d bytes", len(ico))
	}
	if binary.LittleEndian.Uint16(ico[0:2]) != 0 || binary.LittleEndian.Uint16(ico[2:4]) != 1 {
		t.Fatalf("favicon.ico invalid header: %x", ico[:6])
	}
	count := int(binary.LittleEndian.Uint16(ico[4:6]))
	if count < 2 {
		t.Fatalf("expected multi-size favicon.ico (>=2 frames), got %d", count)
	}
	for i := 0; i < count; i++ {
		off := 6 + i*16
		size := int(binary.LittleEndian.Uint32(ico[off+8 : off+12]))
		imgOff := int(binary.LittleEndian.Uint32(ico[off+12 : off+16]))
		if imgOff < 6+count*16 || imgOff+size > len(ico) {
			t.Fatalf("ico entry #%d out of bounds: off=%d size=%d total=%d", i, imgOff, size, len(ico))
		}
		if _, err := png.DecodeConfig(bytes.NewReader(ico[imgOff : imgOff+size])); err != nil {
			t.Fatalf("ico entry #%d is not a valid PNG: %v", i, err)
		}
	}

	touchPNG := AppleTouchIconPNGBytes()
	cfg, err := png.DecodeConfig(bytes.NewReader(touchPNG))
	if err != nil {
		t.Fatalf("apple-touch-icon.png invalid PNG: %v", err)
	}
	if cfg.Width != 180 || cfg.Height != 180 {
		t.Fatalf("apple-touch-icon.png size = %dx%d, want 180x180", cfg.Width, cfg.Height)
	}
}
