package servicebundle

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/luolongfei/ColabOne/console/internal/fsutil"
)

//go:embed all:embedded
var colabOneEmbeddedFS embed.FS

var (
	colabOneServiceBundleFolders  = [...]string{"server", "common", "catalog"}
	colabOneServiceBundleSuffixes = [...]string{".py", ".json", ".txt"}

	colabOneDefaultZipModTime = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	colabOneEmbeddedSubFS        fs.FS
	colabOneEmbeddedRevisionOnce sync.Once
	colabOneEmbeddedRevision     string
)

func init() {
	sub, err := fs.Sub(colabOneEmbeddedFS, "embedded")
	if err != nil {
		panic(fmt.Sprintf("servicebundle: embedded root unavailable: %v", err))
	}
	colabOneEmbeddedSubFS = sub
}

// ColabOneServiceBundle provides access to ColabOne's packaged remote service files
// (server/, common/, catalog/) from either the compiled-in embedded filesystem or
// a disk directory override.
type ColabOneServiceBundle struct {
	fsys     fs.FS
	diskRoot string
	embedded bool
}

// NewEmbeddedBundle returns a service bundle backed by the compiled-in embedded files.
func NewEmbeddedBundle() *ColabOneServiceBundle {
	return &ColabOneServiceBundle{
		fsys:     colabOneEmbeddedSubFS,
		embedded: true,
	}
}

// NewDiskBundle returns a service bundle backed by the given disk directory.
// If root is empty, the returned bundle has no filesystem attached.
func NewDiskBundle(root string) *ColabOneServiceBundle {
	trimmed := strings.TrimSpace(root)
	if trimmed == "" {
		return &ColabOneServiceBundle{}
	}
	cleaned := filepath.Clean(trimmed)
	return &ColabOneServiceBundle{
		fsys:     os.DirFS(cleaned),
		diskRoot: cleaned,
		embedded: false,
	}
}

// HasDiskBundle reports whether root contains a remote service tree (server/app.py).
func HasDiskBundle(root string) bool {
	trimmed := strings.TrimSpace(root)
	if trimmed == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(trimmed, "server", "app.py"))
	return err == nil && info.Mode().IsRegular()
}

// ResolveBundle returns a disk bundle when root contains server/app.py, and
// falls back to the compiled-in embedded bundle otherwise.
func ResolveBundle(root string) *ColabOneServiceBundle {
	if HasDiskBundle(root) {
		return NewDiskBundle(root)
	}
	return NewEmbeddedBundle()
}

// IsEmbedded reports whether this bundle reads from the compiled-in embedded filesystem.
func (b *ColabOneServiceBundle) IsEmbedded() bool {
	return b != nil && b.embedded
}

// Root returns the disk root directory for a disk-backed bundle, or "" if embedded.
func (b *ColabOneServiceBundle) Root() string {
	if b == nil {
		return ""
	}
	return b.diskRoot
}

type colabOneBundleEntry struct {
	folder  string
	name    string
	relPath string
	modTime time.Time
}

func colabOneFileSuffix(name string) string {
	idx := strings.LastIndexByte(name, '.')
	if idx <= 0 || idx == len(name)-1 {
		return ""
	}
	return name[idx:]
}

func colabOneHasAllowedSuffix(name string) bool {
	suffix := colabOneFileSuffix(name)
	for _, allowed := range colabOneServiceBundleSuffixes {
		if suffix == allowed {
			return true
		}
	}
	return false
}

func colabOneIsAllowedFolder(folder string) bool {
	for _, allowed := range colabOneServiceBundleFolders {
		if folder == allowed {
			return true
		}
	}
	return false
}

func colabOneValidateFileName(name string) error {
	if name == "" || name == "." || name == ".." {
		return fmt.Errorf("servicebundle: invalid file name %q: %w", name, fs.ErrInvalid)
	}
	if strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("servicebundle: invalid file name %q: %w", name, fs.ErrInvalid)
	}
	if !fs.ValidPath(name) {
		return fmt.Errorf("servicebundle: invalid file name %q: %w", name, fs.ErrInvalid)
	}
	return nil
}

func colabOneListBundleEntries(fsys fs.FS) []colabOneBundleEntry {
	if fsys == nil {
		return nil
	}
	var results []colabOneBundleEntry
	for _, folder := range colabOneServiceBundleFolders {
		folderInfo, err := fs.Stat(fsys, folder)
		if err != nil || !folderInfo.IsDir() {
			continue
		}
		dirEntries, err := fs.ReadDir(fsys, folder)
		if err != nil {
			continue
		}
		names := make([]string, 0, len(dirEntries))
		for _, entry := range dirEntries {
			names = append(names, entry.Name())
		}
		slices.Sort(names)
		for _, name := range names {
			if !colabOneHasAllowedSuffix(name) {
				continue
			}
			relPath := folder + "/" + name
			info, err := fs.Stat(fsys, relPath)
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			results = append(results, colabOneBundleEntry{
				folder:  folder,
				name:    name,
				relPath: relPath,
				modTime: info.ModTime(),
			})
		}
	}
	return results
}

func colabOneComputeServiceRevision(fsys fs.FS) string {
	entries := colabOneListBundleEntries(fsys)
	hasher := sha256.New()
	count := 0
	nul := []byte{0}
	for _, item := range entries {
		data, err := fs.ReadFile(fsys, item.relPath)
		if err != nil {
			continue
		}
		_, _ = hasher.Write([]byte(item.relPath))
		_, _ = hasher.Write(nul)
		_, _ = hasher.Write(data)
		_, _ = hasher.Write(nul)
		count++
	}
	if count == 0 {
		return ""
	}
	sum := hasher.Sum(nil)
	return hex.EncodeToString(sum)[:16]
}

// ServiceRevision computes the deterministic 16-char lowercase hex SHA-256 revision
// matching common.files.colabone_service_revision byte-for-byte.
// Returns "" if no matching bundle files exist.
func (b *ColabOneServiceBundle) ServiceRevision() string {
	if b == nil || b.fsys == nil {
		return ""
	}
	if b.embedded {
		colabOneEmbeddedRevisionOnce.Do(func() {
			colabOneEmbeddedRevision = colabOneComputeServiceRevision(colabOneEmbeddedSubFS)
		})
		return colabOneEmbeddedRevision
	}
	return colabOneComputeServiceRevision(b.fsys)
}

// BuildZip creates an in-memory DEFLATE zip archive containing
// colabone/<folder>/<filename> entries in deterministic order.
func (b *ColabOneServiceBundle) BuildZip() (zipBytes []byte, fileCount int, err error) {
	if b == nil || b.fsys == nil {
		return nil, 0, errors.New("servicebundle: bundle filesystem is not configured")
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	entries := colabOneListBundleEntries(b.fsys)
	for _, item := range entries {
		data, readErr := fs.ReadFile(b.fsys, item.relPath)
		if readErr != nil {
			_ = zw.Close()
			return nil, 0, fmt.Errorf("servicebundle: read %s: %w", item.relPath, readErr)
		}
		modTime := item.modTime.UTC()
		if modTime.IsZero() || modTime.Year() < 1980 {
			modTime = colabOneDefaultZipModTime
		}
		header := &zip.FileHeader{
			Name:     "colabone/" + item.relPath,
			Method:   zip.Deflate,
			Modified: modTime,
		}
		header.SetMode(0o644)
		writer, createErr := zw.CreateHeader(header)
		if createErr != nil {
			_ = zw.Close()
			return nil, 0, fmt.Errorf("servicebundle: create zip entry %s: %w", header.Name, createErr)
		}
		if _, writeErr := writer.Write(data); writeErr != nil {
			_ = zw.Close()
			return nil, 0, fmt.Errorf("servicebundle: write zip entry %s: %w", header.Name, writeErr)
		}
		fileCount++
	}
	if closeErr := zw.Close(); closeErr != nil {
		return nil, 0, fmt.Errorf("servicebundle: finalize zip: %w", closeErr)
	}
	return buf.Bytes(), fileCount, nil
}

// ReadFile reads a single direct child file from one of the bundle folders
// ("server", "common", "catalog").
func (b *ColabOneServiceBundle) ReadFile(folder, name string) ([]byte, error) {
	if b == nil || b.fsys == nil {
		return nil, errors.New("servicebundle: bundle filesystem is not configured")
	}
	if !colabOneIsAllowedFolder(folder) {
		return nil, fmt.Errorf("servicebundle: invalid bundle folder %q: %w", folder, fs.ErrInvalid)
	}
	if err := colabOneValidateFileName(name); err != nil {
		return nil, err
	}
	relPath := folder + "/" + name
	data, err := fs.ReadFile(b.fsys, relPath)
	if err != nil {
		return nil, fmt.Errorf("servicebundle: read %s: %w", relPath, err)
	}
	return data, nil
}

// ReadCatalogFile reads a catalog JSON file by name (e.g. "presets.json", "comfyui.json").
func (b *ColabOneServiceBundle) ReadCatalogFile(name string) ([]byte, error) {
	return b.ReadFile("catalog", name)
}

// ReadServerFile reads a remote server file by name (e.g. "vm_boot.py", "vm_update.py").
func (b *ColabOneServiceBundle) ReadServerFile(name string) ([]byte, error) {
	return b.ReadFile("server", name)
}

// AtomicWriteJSON encodes data as indented UTF-8 JSON (without HTML escaping) and
// writes it atomically to path using a temp file in the same directory, fsync, and rename.
func AtomicWriteJSON(path string, data any) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("servicebundle: target path is empty")
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(data); err != nil {
		return fmt.Errorf("servicebundle: encode json: %w", err)
	}
	payload := bytes.TrimSuffix(buf.Bytes(), []byte("\n"))

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("servicebundle: create parent directory: %w", err)
	}

	tmpFile, err := os.CreateTemp(dir, ".save-*.tmp")
	if err != nil {
		return fmt.Errorf("servicebundle: create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	closed := false
	renamed := false
	defer func() {
		if !closed {
			_ = tmpFile.Close()
		}
		if !renamed {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmpFile.Write(payload); err != nil {
		return fmt.Errorf("servicebundle: write temp file: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		return fmt.Errorf("servicebundle: sync temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		closed = true
		return fmt.Errorf("servicebundle: close temp file: %w", err)
	}
	closed = true

	if err := fsutil.RenameReplace(tmpPath, path); err != nil {
		return fmt.Errorf("servicebundle: rename temp file: %w", err)
	}
	renamed = true
	return nil
}
