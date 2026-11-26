// Package brandicon provides the embedded ColabOne software logo and derived
// browser favicon assets generated from console/resources/icon.svg.
package brandicon

import (
	"bytes"
	_ "embed"
	"encoding/base64"
	"sync"
	"unicode/utf8"
)

const colabOneFallbackIconSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64">` +
	`<rect width="64" height="64" rx="16" fill="#111827"/>` +
	`<circle cx="32" cy="32" r="14" fill="none" stroke="#ffffff" stroke-width="5"/>` +
	`<circle cx="32" cy="32" r="4.5" fill="#3b82f6"/>` +
	`</svg>`

var (
	rawIconViewBox     = []byte(`viewBox="0 0 1254 1254"`)
	trimmedIconViewBox = []byte(`viewBox="120 120 1014 1014"`)
)

//go:embed icon.svg
var colabOneEmbeddedRawSVG []byte

//go:embed favicon.svg
var colabOneEmbeddedFaviconSVG []byte

//go:embed favicon.ico
var colabOneEmbeddedFaviconICO []byte

//go:embed apple-touch-icon.png
var colabOneEmbeddedAppleTouchIconPNG []byte

var (
	colabOneTrimmedSVGOnce  sync.Once
	colabOneTrimmedSVGBytes []byte

	colabOneDataURIOnce  sync.Once
	colabOneDataURIValue string
)

// RawSVGBytes returns the embedded ColabOne master vector icon bytes
// (byte-for-byte mirror of console/resources/icon.svg).
func RawSVGBytes() []byte {
	return colabOneEmbeddedRawSVG
}

// TrimIconSVG trims the outer macOS/desktop icon gutter from ColabOne's master
// icon.svg so the white rounded tile fills inline UI logos and browser tab
// favicons edge-to-edge cleanly.
func TrimIconSVG(raw []byte) []byte {
	if len(raw) == 0 || !utf8.Valid(raw) || !bytes.Contains(raw, []byte("<svg")) {
		return []byte(colabOneFallbackIconSVG)
	}
	if bytes.Contains(raw, rawIconViewBox) {
		return bytes.Replace(raw, rawIconViewBox, trimmedIconViewBox, 1)
	}
	return raw
}

// SVGBytes returns the gutter-trimmed ColabOne vector icon SVG suitable for
// web logos and browser tab favicons.
func SVGBytes() []byte {
	colabOneTrimmedSVGOnce.Do(func() {
		if len(colabOneEmbeddedFaviconSVG) > 0 && utf8.Valid(colabOneEmbeddedFaviconSVG) && bytes.Contains(colabOneEmbeddedFaviconSVG, []byte("<svg")) {
			colabOneTrimmedSVGBytes = colabOneEmbeddedFaviconSVG
			return
		}
		colabOneTrimmedSVGBytes = TrimIconSVG(colabOneEmbeddedRawSVG)
	})
	return colabOneTrimmedSVGBytes
}

// DataURI returns the cached base64 SVG data URI for the ColabOne software logo.
func DataURI() string {
	colabOneDataURIOnce.Do(func() {
		colabOneDataURIValue = "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString(SVGBytes())
	})
	return colabOneDataURIValue
}

// DataURIFromSVGBytes builds a base64 SVG data URI from raw SVG bytes, trimming
// the desktop icon gutter when present and falling back safely on invalid input.
func DataURIFromSVGBytes(raw []byte) string {
	return "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString(TrimIconSVG(raw))
}

// FallbackDataURI returns the fallback SVG data URI used when icon bytes are invalid.
func FallbackDataURI() string {
	return "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(colabOneFallbackIconSVG))
}

// FaviconICOBytes returns the embedded multi-size (16/32/48px) ICO bytes
// rendered from the trimmed ColabOne software logo.
func FaviconICOBytes() []byte {
	return colabOneEmbeddedFaviconICO
}

// AppleTouchIconPNGBytes returns the embedded 180x180 PNG icon rendered on a
// solid white canvas for iOS and mobile home screen shortcuts.
func AppleTouchIconPNGBytes() []byte {
	return colabOneEmbeddedAppleTouchIconPNG
}
