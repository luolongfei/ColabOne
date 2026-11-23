package servicebundle

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func repoRootPath(t *testing.T) string {
	t.Helper()
	// Package directory is <repo>/console/internal/servicebundle.
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	if !HasDiskBundle(root) {
		t.Fatalf("expected repo root %q to contain server/app.py", root)
	}
	return root
}

func TestEmbeddedAndDiskServiceRevisionMatchRepo(t *testing.T) {
	repoRoot := repoRootPath(t)
	embedded := NewEmbeddedBundle()
	disk := NewDiskBundle(repoRoot)

	embRev := embedded.ServiceRevision()
	diskRev := disk.ServiceRevision()

	if len(embRev) != 16 {
		t.Fatalf("expected 16-char embedded revision, got %q (len=%d)", embRev, len(embRev))
	}
	if embRev != strings.ToLower(embRev) {
		t.Fatalf("expected lowercase hex revision, got %q", embRev)
	}
	if _, err := hex.DecodeString(embRev); err != nil {
		t.Fatalf("expected valid hex revision, got %q: %v", embRev, err)
	}
	if embRev != diskRev {
		t.Fatalf("embedded revision %q does not match disk repo revision %q", embRev, diskRev)
	}
	t.Logf("service revision (embedded == disk repo): %s", embRev)
	// Verify disk bundle over embedded/ itself also matches.
	embDirRev := NewDiskBundle("embedded").ServiceRevision()
	if embDirRev != embRev {
		t.Fatalf("disk revision over embedded/ %q != embedded revision %q", embDirRev, embRev)
	}
}

func TestServiceRevisionDeterministicAlgorithm(t *testing.T) {
	root := t.TempDir()

	write := func(rel string, content []byte) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(full, content, 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	// Included files (in non-sorted write order to verify sorting).
	write("catalog/presets.json", []byte(`{"name":"中文模型"}`))
	write("server/requirements.txt", []byte("fastapi==0.1.0\n"))
	write("server/app.py", []byte("print('hello')\n"))
	write("server/__init__.py", nil)
	write("server/.hidden.py", []byte("# hidden\n"))
	write("common/files.py", []byte("# common\n"))

	// Excluded files and directories.
	write("server/README.md", []byte("ignored"))
	write("server/.py", []byte("ignored because Python Path('.py').suffix == ''"))
	write("server/UPPER.PY", []byte("ignored case-sensitive suffix"))
	write("server/nested/sub.py", []byte("ignored nested file"))
	write("extra/app.py", []byte("ignored folder"))
	if err := os.MkdirAll(filepath.Join(root, "server", "dir.py"), 0o755); err != nil {
		t.Fatalf("mkdir dir.py: %v", err)
	}

	// Compute expected SHA-256[:16] following common.files.colabone_service_revision.
	expectedOrder := []struct {
		rel  string
		body []byte
	}{
		{"server/.hidden.py", []byte("# hidden\n")},
		{"server/__init__.py", nil},
		{"server/app.py", []byte("print('hello')\n")},
		{"server/requirements.txt", []byte("fastapi==0.1.0\n")},
		{"common/files.py", []byte("# common\n")},
		{"catalog/presets.json", []byte(`{"name":"中文模型"}`)},
	}
	h := sha256.New()
	for _, item := range expectedOrder {
		h.Write([]byte(item.rel))
		h.Write([]byte{0})
		h.Write(item.body)
		h.Write([]byte{0})
	}
	want := hex.EncodeToString(h.Sum(nil))[:16]
	// Golden value produced independently by Python common.files.colabone_service_revision.
	const pythonGolden = "c534def2a08ecafa"
	if want != pythonGolden {
		t.Fatalf("test fixture hash %q != Python golden %q", want, pythonGolden)
	}

	bundle := NewDiskBundle(root)
	got := bundle.ServiceRevision()
	if got != want {
		t.Fatalf("ServiceRevision() = %q, want %q", got, want)
	}

	// Mutating a file on disk must immediately change disk bundle revision (no stale cache).
	write("catalog/presets.json", []byte(`{"name":"更新模型"}`))
	updated := bundle.ServiceRevision()
	if updated == "" || updated == got {
		t.Fatalf("expected updated revision to differ from %q, got %q", got, updated)
	}
}

func TestServiceRevisionEmptyAndEdgeCases(t *testing.T) {
	// Nil bundle.
	var nilBundle *ColabOneServiceBundle
	if got := nilBundle.ServiceRevision(); got != "" {
		t.Fatalf("nil bundle ServiceRevision() = %q, want empty", got)
	}
	if nilBundle.IsEmbedded() {
		t.Fatalf("nil bundle IsEmbedded() = true, want false")
	}
	if got := nilBundle.Root(); got != "" {
		t.Fatalf("nil bundle Root() = %q, want empty", got)
	}

	// Empty disk path.
	if got := NewDiskBundle("").ServiceRevision(); got != "" {
		t.Fatalf("empty root ServiceRevision() = %q, want empty", got)
	}

	// Empty temp directory.
	emptyDir := t.TempDir()
	if got := NewDiskBundle(emptyDir).ServiceRevision(); got != "" {
		t.Fatalf("empty dir ServiceRevision() = %q, want empty", got)
	}

	// Folder path is a regular file rather than a directory, plus non-matching files.
	fileAsFolder := t.TempDir()
	if err := os.WriteFile(filepath.Join(fileAsFolder, "server"), []byte("not a dir"), 0o644); err != nil {
		t.Fatalf("write file named server: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(fileAsFolder, "common"), 0o755); err != nil {
		t.Fatalf("mkdir common: %v", err)
	}
	if err := os.WriteFile(filepath.Join(fileAsFolder, "common", "notes.md"), []byte("skip"), 0o644); err != nil {
		t.Fatalf("write notes.md: %v", err)
	}
	if got := NewDiskBundle(fileAsFolder).ServiceRevision(); got != "" {
		t.Fatalf("non-matching dir ServiceRevision() = %q, want empty", got)
	}
}

func TestBuildZipEmbeddedAndDiskParity(t *testing.T) {
	repoRoot := repoRootPath(t)
	embedded := NewEmbeddedBundle()
	disk := NewDiskBundle(repoRoot)

	embZip, embCount, err := embedded.BuildZip()
	if err != nil {
		t.Fatalf("embedded BuildZip() error: %v", err)
	}
	diskZip, diskCount, err := disk.BuildZip()
	if err != nil {
		t.Fatalf("disk BuildZip() error: %v", err)
	}
	if embCount == 0 {
		t.Fatalf("expected non-zero embedded zip file count")
	}
	if embCount != diskCount {
		t.Fatalf("embedded file count %d != disk file count %d", embCount, diskCount)
	}

	// Embedded BuildZip must be deterministic byte-for-byte across repeated calls.
	embZip2, embCount2, err := embedded.BuildZip()
	if err != nil {
		t.Fatalf("second embedded BuildZip() error: %v", err)
	}
	if embCount2 != embCount || !bytes.Equal(embZip, embZip2) {
		t.Fatalf("expected deterministic embedded BuildZip output")
	}

	embReader, err := zip.NewReader(bytes.NewReader(embZip), int64(len(embZip)))
	if err != nil {
		t.Fatalf("open embedded zip: %v", err)
	}
	diskReader, err := zip.NewReader(bytes.NewReader(diskZip), int64(len(diskZip)))
	if err != nil {
		t.Fatalf("open disk zip: %v", err)
	}
	if len(embReader.File) != embCount {
		t.Fatalf("zip entry count %d != returned fileCount %d", len(embReader.File), embCount)
	}
	if len(diskReader.File) != diskCount {
		t.Fatalf("disk zip entry count %d != returned fileCount %d", len(diskReader.File), diskCount)
	}

	// Verify entry names, order, compression method, permissions, timestamps, and byte contents.
	extractDir := t.TempDir()
	entryNames := make([]string, 0, len(embReader.File))
	for i, ef := range embReader.File {
		df := diskReader.File[i]
		if ef.Name != df.Name {
			t.Fatalf("entry[%d] name mismatch: embedded=%q disk=%q", i, ef.Name, df.Name)
		}
		if !strings.HasPrefix(ef.Name, "colabone/") {
			t.Fatalf("entry %q missing colabone/ prefix", ef.Name)
		}
		if ef.Method != zip.Deflate {
			t.Fatalf("entry %q method=%v, want Deflate", ef.Name, ef.Method)
		}
		if ef.Modified.Year() < 1980 {
			t.Fatalf("entry %q has invalid zip timestamp %v", ef.Name, ef.Modified)
		}
		if ef.Mode().Perm() != 0o644 {
			t.Fatalf("entry %q mode=%o, want 0644", ef.Name, ef.Mode().Perm())
		}

		eBytes := readZipFile(t, ef)
		dBytes := readZipFile(t, df)
		if !bytes.Equal(eBytes, dBytes) {
			t.Fatalf("entry %q bytes differ between embedded and disk bundles", ef.Name)
		}
		entryNames = append(entryNames, ef.Name)

		// Extract to temp dir to verify extracted tree hashes to the exact same revision.
		outPath := filepath.Join(extractDir, filepath.FromSlash(ef.Name))
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			t.Fatalf("mkdir extract %s: %v", outPath, err)
		}
		if err := os.WriteFile(outPath, eBytes, 0o644); err != nil {
			t.Fatalf("write extract %s: %v", outPath, err)
		}
	}

	requiredEntries := []string{
		"colabone/server/__init__.py",
		"colabone/server/app.py",
		"colabone/server/requirements.txt",
		"colabone/server/vm_boot.py",
		"colabone/server/vm_update.py",
		"colabone/common/__init__.py",
		"colabone/common/files.py",
		"colabone/catalog/presets.json",
		"colabone/catalog/comfyui.json",
	}
	for _, req := range requiredEntries {
		if !slices.Contains(entryNames, req) {
			t.Fatalf("zip missing required entry %q (have %v)", req, entryNames)
		}
	}

	// Verify folder grouping order: all server/, then common/, then catalog/.
	folderRank := map[string]int{"server": 0, "common": 1, "catalog": 2}
	lastRank := -1
	lastNameInFolder := ""
	for _, full := range entryNames {
		parts := strings.Split(full, "/")
		if len(parts) != 3 || parts[0] != "colabone" {
			t.Fatalf("unexpected zip entry structure: %q", full)
		}
		rank, ok := folderRank[parts[1]]
		if !ok {
			t.Fatalf("unexpected folder in zip entry: %q", full)
		}
		if rank < lastRank {
			t.Fatalf("zip entries out of folder order at %q", full)
		}
		if rank == lastRank && parts[2] <= lastNameInFolder {
			t.Fatalf("zip entries not sorted within folder %s: %q after %q", parts[1], parts[2], lastNameInFolder)
		}
		lastRank = rank
		lastNameInFolder = parts[2]
	}

	extractedRev := NewDiskBundle(filepath.Join(extractDir, "colabone")).ServiceRevision()
	if extractedRev != embedded.ServiceRevision() {
		t.Fatalf("extracted revision %q != embedded revision %q", extractedRev, embedded.ServiceRevision())
	}
}

func TestBuildZipEmptyAndErrors(t *testing.T) {
	emptyDir := t.TempDir()
	zipBytes, count, err := NewDiskBundle(emptyDir).BuildZip()
	if err != nil {
		t.Fatalf("empty dir BuildZip() error: %v", err)
	}
	if count != 0 {
		t.Fatalf("empty dir BuildZip() count = %d, want 0", count)
	}
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		t.Fatalf("empty zip should still be valid zip archive: %v", err)
	}
	if len(zr.File) != 0 {
		t.Fatalf("expected 0 entries in empty zip, got %d", len(zr.File))
	}

	var nilBundle *ColabOneServiceBundle
	if _, _, err := nilBundle.BuildZip(); err == nil {
		t.Fatalf("expected error from nil bundle BuildZip()")
	}
	if _, _, err := NewDiskBundle("").BuildZip(); err == nil {
		t.Fatalf("expected error from empty-root BuildZip()")
	}
}

func TestReadCatalogAndServerFiles(t *testing.T) {
	embedded := NewEmbeddedBundle()

	// presets.json
	presetsRaw, err := embedded.ReadCatalogFile("presets.json")
	if err != nil {
		t.Fatalf("ReadCatalogFile(presets.json) error: %v", err)
	}
	var presets []map[string]any
	if err := json.Unmarshal(presetsRaw, &presets); err != nil {
		t.Fatalf("unmarshal presets.json: %v", err)
	}
	if len(presets) == 0 {
		t.Fatalf("expected non-empty presets.json array")
	}

	// comfyui.json
	comfyRaw, err := embedded.ReadCatalogFile("comfyui.json")
	if err != nil {
		t.Fatalf("ReadCatalogFile(comfyui.json) error: %v", err)
	}
	var comfy map[string]any
	if err := json.Unmarshal(comfyRaw, &comfy); err != nil {
		t.Fatalf("unmarshal comfyui.json: %v", err)
	}
	if _, ok := comfy["repo"]; !ok {
		t.Fatalf("expected comfyui.json to contain repo field")
	}

	// Other catalog files.
	for _, name := range []string{"engine.json", "cloudflared.json", "rclone.json"} {
		raw, err := embedded.ReadCatalogFile(name)
		if err != nil {
			t.Fatalf("ReadCatalogFile(%s) error: %v", name, err)
		}
		if !json.Valid(raw) {
			t.Fatalf("catalog/%s is not valid JSON", name)
		}
	}

	// Server files used during boot and hot-upgrade.
	bootRaw, err := embedded.ReadServerFile("vm_boot.py")
	if err != nil {
		t.Fatalf("ReadServerFile(vm_boot.py) error: %v", err)
	}
	if !bytes.Contains(bootRaw, []byte("colabone.zip")) {
		t.Fatalf("unexpected vm_boot.py content")
	}
	updateRaw, err := embedded.ReadServerFile("vm_update.py")
	if err != nil {
		t.Fatalf("ReadServerFile(vm_update.py) error: %v", err)
	}
	if !bytes.Contains(updateRaw, []byte("def colabone_update")) {
		t.Fatalf("unexpected vm_update.py content")
	}

	// Common file via ReadFile.
	commonRaw, err := embedded.ReadFile("common", "files.py")
	if err != nil {
		t.Fatalf("ReadFile(common, files.py) error: %v", err)
	}
	if !bytes.Contains(commonRaw, []byte("colabone_service_revision")) {
		t.Fatalf("unexpected common/files.py content")
	}

	// Missing file returns ErrNotExist.
	if _, err := embedded.ReadCatalogFile("does-not-exist.json"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("expected fs.ErrNotExist, got %v", err)
	}

	// Path traversal and invalid names / folders must be rejected.
	invalidNames := []string{"", ".", "..", "../server/app.py", "sub/presets.json", `..\app.py`, "/etc/passwd"}
	for _, bad := range invalidNames {
		if _, err := embedded.ReadCatalogFile(bad); !errors.Is(err, fs.ErrInvalid) {
			t.Fatalf("ReadCatalogFile(%q) expected fs.ErrInvalid, got %v", bad, err)
		}
	}
	if _, err := embedded.ReadFile("bridge", "provider.py"); !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("ReadFile(bridge, ...) expected fs.ErrInvalid, got %v", err)
	}

	var nilBundle *ColabOneServiceBundle
	if _, err := nilBundle.ReadCatalogFile("presets.json"); err == nil {
		t.Fatalf("expected error reading from nil bundle")
	}
}

func TestHasDiskBundleAndResolveBundle(t *testing.T) {
	repoRoot := repoRootPath(t)

	if !HasDiskBundle(repoRoot) {
		t.Fatalf("HasDiskBundle(%q) = false, want true", repoRoot)
	}
	resolved := ResolveBundle(repoRoot)
	if resolved.IsEmbedded() {
		t.Fatalf("ResolveBundle(repoRoot) unexpectedly fell back to embedded")
	}
	if resolved.Root() != filepath.Clean(repoRoot) {
		t.Fatalf("ResolveBundle(repoRoot).Root() = %q, want %q", resolved.Root(), filepath.Clean(repoRoot))
	}

	emptyDir := t.TempDir()
	if HasDiskBundle(emptyDir) {
		t.Fatalf("HasDiskBundle(emptyDir) = true, want false")
	}
	fallback := ResolveBundle(emptyDir)
	if !fallback.IsEmbedded() {
		t.Fatalf("ResolveBundle(emptyDir) should return embedded bundle")
	}
	if fallback.Root() != "" {
		t.Fatalf("embedded bundle Root() = %q, want empty", fallback.Root())
	}

	if HasDiskBundle("") {
		t.Fatalf("HasDiskBundle(\"\") = true, want false")
	}

	// server/app.py as a directory must not count as a valid disk bundle.
	dirApp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dirApp, "server", "app.py"), 0o755); err != nil {
		t.Fatalf("mkdir server/app.py: %v", err)
	}
	if HasDiskBundle(dirApp) {
		t.Fatalf("HasDiskBundle should be false when server/app.py is a directory")
	}
}

func TestAtomicWriteJSON(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "nested", "sub", "settings.json")

	type sampleConfig struct {
		Source  string `json:"source"`
		Message string `json:"message"`
		Count   int    `json:"count"`
	}
	initial := sampleConfig{
		Source:  "客户端",
		Message: "远端 GPU <ready> & 正常",
		Count:   1,
	}

	if err := AtomicWriteJSON(target, initial); err != nil {
		t.Fatalf("AtomicWriteJSON initial error: %v", err)
	}

	raw, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	// Must be indented UTF-8 without HTML escaping.
	text := string(raw)
	if !strings.Contains(text, "\n  \"source\": \"客户端\"") {
		t.Fatalf("expected 2-space indented UTF-8 JSON, got:\n%s", text)
	}
	if !strings.Contains(text, "远端 GPU <ready> & 正常") {
		t.Fatalf("expected unescaped HTML & UTF-8 characters, got:\n%s", text)
	}

	var decoded sampleConfig
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal written json: %v", err)
	}
	if decoded != initial {
		t.Fatalf("decoded=%+v, want %+v", decoded, initial)
	}

	// Overwrite atomically.
	updated := sampleConfig{
		Source:  "远端",
		Message: "热更新完成",
		Count:   2,
	}
	if err := AtomicWriteJSON(target, updated); err != nil {
		t.Fatalf("AtomicWriteJSON overwrite error: %v", err)
	}
	raw2, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read overwritten target: %v", err)
	}
	if err := json.Unmarshal(raw2, &decoded); err != nil {
		t.Fatalf("unmarshal overwritten json: %v", err)
	}
	if decoded != updated {
		t.Fatalf("decoded=%+v, want %+v", decoded, updated)
	}

	// Unmarshalable data (NaN / Inf) must return an error and NOT corrupt existing file.
	for _, bad := range []any{
		map[string]any{"v": math.NaN()},
		map[string]any{"v": math.Inf(1)},
		map[string]any{"ch": make(chan int)},
	} {
		if err := AtomicWriteJSON(target, bad); err == nil {
			t.Fatalf("expected AtomicWriteJSON to reject %#v", bad)
		}
	}
	afterBad, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read after failed write: %v", err)
	}
	if !bytes.Equal(afterBad, raw2) {
		t.Fatalf("existing file was modified on failed AtomicWriteJSON")
	}

	// No leftover temp files.
	assertNoTempFiles(t, filepath.Dir(target))

	// Rename failure (target is an existing directory) must clean up temp file.
	existingDir := filepath.Join(dir, "a-dir")
	if err := os.MkdirAll(existingDir, 0o755); err != nil {
		t.Fatalf("mkdir existingDir: %v", err)
	}
	if err := AtomicWriteJSON(existingDir, map[string]any{"ok": true}); err == nil {
		t.Fatalf("expected error when target is a directory")
	}
	assertNoTempFiles(t, dir)

	// Empty path must fail.
	if err := AtomicWriteJSON("", map[string]any{"ok": true}); err == nil {
		t.Fatalf("expected error for empty target path")
	}
}

func TestAtomicWriteJSONConcurrent(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "concurrent.json")
	if err := AtomicWriteJSON(target, map[string]int{"n": 0}); err != nil {
		t.Fatalf("seed write: %v", err)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 32)
	for g := range 4 {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := range 25 {
				payload := map[string]any{
					"worker": worker,
					"iter":   i,
					"label":  "并发写入测试",
				}
				if err := AtomicWriteJSON(target, payload); err != nil {
					errCh <- err
					return
				}
				raw, err := readFileWithWindowsRetry(target)
				if err != nil {
					errCh <- err
					return
				}
				var check map[string]any
				if err := json.Unmarshal(raw, &check); err != nil {
					errCh <- err
					return
				}
				if len(check) == 0 {
					errCh <- errors.New("decoded concurrent JSON payload is empty")
					return
				}
			}
		}(g)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent AtomicWriteJSON error: %v", err)
	}
	assertNoTempFiles(t, dir)
}

func TestColabOneRenameReplaceWindowsRetry(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "state.json")
	if err := os.WriteFile(filePath, []byte(`{"ok":true}`), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	dirPath := filepath.Join(dir, "existing-dir")
	if err := os.MkdirAll(dirPath, 0o755); err != nil {
		t.Fatalf("mkdir existing-dir: %v", err)
	}

	sharingErr := &os.LinkError{Op: "rename", Old: "tmp", New: filePath, Err: colabOneWinErrSharingViolation}
	accessErr := &os.LinkError{Op: "rename", Old: "tmp", New: filePath, Err: colabOneWinErrAccessDenied}
	lockErr := &os.LinkError{Op: "rename", Old: "tmp", New: filePath, Err: colabOneWinErrLockViolation}
	notFoundErr := &os.LinkError{Op: "rename", Old: "tmp", New: filePath, Err: colabOneWinErrFileNotFound}
	diskFullErr := &os.LinkError{Op: "rename", Old: "tmp", New: filePath, Err: syscall.Errno(112)}

	t.Run("non-windows does not retry", func(t *testing.T) {
		calls := 0
		sleeps := 0
		statCalls := 0
		err := colabOneRenameReplaceWith(
			"tmp",
			filePath,
			false,
			func(string, string) error {
				calls++
				return sharingErr
			},
			func(name string) (os.FileInfo, error) {
				statCalls++
				return os.Stat(name)
			},
			func(time.Duration) { sleeps++ },
		)
		if !errors.Is(err, sharingErr) {
			t.Fatalf("got err=%v, want %v", err, sharingErr)
		}
		if calls != 1 || sleeps != 0 || statCalls != 0 {
			t.Fatalf("calls=%d sleeps=%d statCalls=%d, want 1/0/0", calls, sleeps, statCalls)
		}
	})

	t.Run("windows immediate success skips stat and sleep", func(t *testing.T) {
		calls := 0
		sleeps := 0
		statCalls := 0
		err := colabOneRenameReplaceWith(
			"tmp",
			filePath,
			true,
			func(string, string) error {
				calls++
				return nil
			},
			func(name string) (os.FileInfo, error) {
				statCalls++
				return os.Stat(name)
			},
			func(time.Duration) { sleeps++ },
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if calls != 1 || sleeps != 0 || statCalls != 0 {
			t.Fatalf("calls=%d sleeps=%d statCalls=%d, want 1/0/0", calls, sleeps, statCalls)
		}
	})

	t.Run("windows retries transient errors until success", func(t *testing.T) {
		transientSeq := []error{sharingErr, accessErr, lockErr, notFoundErr, fs.ErrPermission}
		calls := 0
		var delays []time.Duration
		err := colabOneRenameReplaceWith(
			"tmp",
			filePath,
			true,
			func(string, string) error {
				idx := calls
				calls++
				if idx < len(transientSeq) {
					return transientSeq[idx]
				}
				return nil
			},
			os.Stat,
			func(d time.Duration) { delays = append(delays, d) },
		)
		if err != nil {
			t.Fatalf("expected recovery after transient errors, got %v", err)
		}
		if calls != len(transientSeq)+1 {
			t.Fatalf("calls=%d, want %d", calls, len(transientSeq)+1)
		}
		if len(delays) != len(transientSeq) {
			t.Fatalf("sleep count=%d, want %d", len(delays), len(transientSeq))
		}
		maxExpected := colabOneWindowsRenameMaxDelay + colabOneWindowsRenameBaseDelay
		for i, d := range delays {
			if d < colabOneWindowsRenameBaseDelay || d >= maxExpected {
				t.Fatalf("delay[%d]=%v out of bounded range [%v, %v)", i, d, colabOneWindowsRenameBaseDelay, maxExpected)
			}
		}
	})

	t.Run("windows fails fast when target is existing directory", func(t *testing.T) {
		dirAccessErr := &os.LinkError{Op: "rename", Old: "tmp", New: dirPath, Err: colabOneWinErrAccessDenied}
		calls := 0
		sleeps := 0
		err := colabOneRenameReplaceWith(
			"tmp",
			dirPath,
			true,
			func(string, string) error {
				calls++
				return dirAccessErr
			},
			os.Stat,
			func(time.Duration) { sleeps++ },
		)
		if !errors.Is(err, dirAccessErr) {
			t.Fatalf("got err=%v, want %v", err, dirAccessErr)
		}
		if calls != 1 || sleeps != 0 {
			t.Fatalf("directory target must fail fast: calls=%d sleeps=%d, want 1/0", calls, sleeps)
		}
	})

	t.Run("windows fails fast on non-transient error", func(t *testing.T) {
		calls := 0
		sleeps := 0
		err := colabOneRenameReplaceWith(
			"tmp",
			filePath,
			true,
			func(string, string) error {
				calls++
				return diskFullErr
			},
			os.Stat,
			func(time.Duration) { sleeps++ },
		)
		if !errors.Is(err, diskFullErr) {
			t.Fatalf("got err=%v, want %v", err, diskFullErr)
		}
		if calls != 1 || sleeps != 0 {
			t.Fatalf("non-transient error must fail fast: calls=%d sleeps=%d, want 1/0", calls, sleeps)
		}
	})

	t.Run("windows stops after bounded retries on persistent sharing violation", func(t *testing.T) {
		calls := 0
		sleeps := 0
		err := colabOneRenameReplaceWith(
			"tmp",
			filePath,
			true,
			func(string, string) error {
				calls++
				return sharingErr
			},
			os.Stat,
			func(time.Duration) { sleeps++ },
		)
		if !errors.Is(err, sharingErr) {
			t.Fatalf("got err=%v, want %v", err, sharingErr)
		}
		if calls != colabOneWindowsRenameMaxAttempts {
			t.Fatalf("calls=%d, want %d", calls, colabOneWindowsRenameMaxAttempts)
		}
		if sleeps != colabOneWindowsRenameMaxAttempts-1 {
			t.Fatalf("sleeps=%d, want %d", sleeps, colabOneWindowsRenameMaxAttempts-1)
		}
	})
}

func TestReadFileWithWindowsRetry(t *testing.T) {
	sharingErr := &fs.PathError{Op: "open", Path: "concurrent.json", Err: colabOneWinErrSharingViolation}
	accessErr := &fs.PathError{Op: "open", Path: "concurrent.json", Err: colabOneWinErrAccessDenied}
	invalidErr := &fs.PathError{Op: "open", Path: "concurrent.json", Err: fs.ErrInvalid}

	t.Run("non-windows does not retry", func(t *testing.T) {
		calls := 0
		sleeps := 0
		_, err := readFileWithWindowsRetryImpl(
			"concurrent.json",
			false,
			func(string) ([]byte, error) {
				calls++
				return nil, sharingErr
			},
			func(time.Duration) { sleeps++ },
		)
		if !errors.Is(err, sharingErr) {
			t.Fatalf("got err=%v, want %v", err, sharingErr)
		}
		if calls != 1 || sleeps != 0 {
			t.Fatalf("calls=%d sleeps=%d, want 1/0", calls, sleeps)
		}
	})

	t.Run("windows retries transient read sharing violations", func(t *testing.T) {
		calls := 0
		sleeps := 0
		want := []byte(`{"ok":true}`)
		got, err := readFileWithWindowsRetryImpl(
			"concurrent.json",
			true,
			func(string) ([]byte, error) {
				calls++
				switch calls {
				case 1:
					return nil, sharingErr
				case 2:
					return nil, accessErr
				default:
					return want, nil
				}
			},
			func(time.Duration) { sleeps++ },
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("got=%q, want %q", got, want)
		}
		if calls != 3 || sleeps != 2 {
			t.Fatalf("calls=%d sleeps=%d, want 3/2", calls, sleeps)
		}
	})

	t.Run("windows fails fast on non-transient read error", func(t *testing.T) {
		calls := 0
		sleeps := 0
		_, err := readFileWithWindowsRetryImpl(
			"concurrent.json",
			true,
			func(string) ([]byte, error) {
				calls++
				return nil, invalidErr
			},
			func(time.Duration) { sleeps++ },
		)
		if !errors.Is(err, invalidErr) {
			t.Fatalf("got err=%v, want %v", err, invalidErr)
		}
		if calls != 1 || sleeps != 0 {
			t.Fatalf("calls=%d sleeps=%d, want 1/0", calls, sleeps)
		}
	})

	t.Run("windows stops after bounded retries", func(t *testing.T) {
		calls := 0
		sleeps := 0
		_, err := readFileWithWindowsRetryImpl(
			"concurrent.json",
			true,
			func(string) ([]byte, error) {
				calls++
				return nil, sharingErr
			},
			func(time.Duration) { sleeps++ },
		)
		if !errors.Is(err, sharingErr) {
			t.Fatalf("got err=%v, want %v", err, sharingErr)
		}
		if calls != colabOneWindowsRenameMaxAttempts || sleeps != colabOneWindowsRenameMaxAttempts-1 {
			t.Fatalf("calls=%d sleeps=%d, want %d/%d", calls, sleeps, colabOneWindowsRenameMaxAttempts, colabOneWindowsRenameMaxAttempts-1)
		}
	})
}

func readFileWithWindowsRetry(path string) ([]byte, error) {
	return readFileWithWindowsRetryImpl(path, runtime.GOOS == "windows", os.ReadFile, time.Sleep)
}

func readFileWithWindowsRetryImpl(
	path string,
	isWindows bool,
	readFn func(string) ([]byte, error),
	sleepFn func(time.Duration),
) ([]byte, error) {
	raw, err := readFn(path)
	if err == nil || !isWindows {
		return raw, err
	}
	for attempt := 1; attempt < colabOneWindowsRenameMaxAttempts; attempt++ {
		if !colabOneIsTransientWindowsFileErr(err) {
			return nil, err
		}
		sleepFn(colabOneWindowsRetryDelay(attempt))
		raw, err = readFn(path)
		if err == nil {
			return raw, nil
		}
	}
	return nil, err
}

func TestEmbeddedBundleConcurrentAccess(t *testing.T) {
	bundle := NewEmbeddedBundle()
	expectedRev := bundle.ServiceRevision()

	var wg sync.WaitGroup
	errCh := make(chan error, 32)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 10 {
				if rev := bundle.ServiceRevision(); rev != expectedRev {
					errCh <- errors.New("revision mismatch under concurrency")
					return
				}
				if _, count, err := bundle.BuildZip(); err != nil || count == 0 {
					errCh <- errors.New("BuildZip failed under concurrency")
					return
				}
				if _, err := bundle.ReadCatalogFile("presets.json"); err != nil {
					errCh <- err
					return
				}
				if _, err := bundle.ReadServerFile("vm_boot.py"); err != nil {
					errCh <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent bundle access error: %v", err)
	}
}

func readZipFile(t *testing.T, f *zip.File) []byte {
	t.Helper()
	rc, err := f.Open()
	if err != nil {
		t.Fatalf("open zip entry %s: %v", f.Name, err)
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read zip entry %s: %v", f.Name, err)
	}
	return data
}

func assertNoTempFiles(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir %s: %v", dir, err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".save-") || strings.HasSuffix(entry.Name(), ".tmp") {
			t.Fatalf("leftover temp file in %s: %s", dir, entry.Name())
		}
	}
}
