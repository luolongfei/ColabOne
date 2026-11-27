package vault

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"unicode/utf16"

	"github.com/egoist/mygo"
)

// spyBackend wraps colabOneMemorySecretBackend with call counters and error injection.
type spyBackend struct {
	inner     *colabOneMemorySecretBackend
	mu        sync.Mutex
	getCalls  map[string]int
	setCalls  map[string]int
	delCalls  map[string]int
	maxStored int
	failGet   func(key string) error
	failSet   func(key string, value []byte) error
	failDel   func(key string) error
}

func newSpyBackend() *spyBackend {
	return &spyBackend{
		inner:    newColabOneMemorySecretBackend(),
		getCalls: make(map[string]int),
		setCalls: make(map[string]int),
		delCalls: make(map[string]int),
	}
}

func (s *spyBackend) Get(key string) ([]byte, error) {
	s.mu.Lock()
	s.getCalls[key]++
	failFn := s.failGet
	s.mu.Unlock()
	if failFn != nil {
		if err := failFn(key); err != nil {
			return nil, err
		}
	}
	return s.inner.Get(key)
}

func (s *spyBackend) Set(key string, value []byte) error {
	s.mu.Lock()
	s.setCalls[key]++
	if len(value) > s.maxStored {
		s.maxStored = len(value)
	}
	failFn := s.failSet
	s.mu.Unlock()
	if failFn != nil {
		if err := failFn(key, value); err != nil {
			return err
		}
	}
	return s.inner.Set(key, value)
}

func (s *spyBackend) Delete(key string) error {
	s.mu.Lock()
	s.delCalls[key]++
	failFn := s.failDel
	s.mu.Unlock()
	if failFn != nil {
		if err := failFn(key); err != nil {
			return err
		}
	}
	return s.inner.Delete(key)
}

func (s *spyBackend) keys() []string {
	s.inner.mu.RLock()
	defer s.inner.mu.RUnlock()
	out := make([]string, 0, len(s.inner.data))
	for k := range s.inner.data {
		out = append(out, k)
	}
	return out
}

func (s *spyBackend) getCount(key string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getCalls[key]
}

func encodeUTF16LE(s string) []byte {
	units := utf16.Encode([]rune(s))
	out := make([]byte, len(units)*2)
	for i, u := range units {
		out[2*i] = byte(u)
		out[2*i+1] = byte(u >> 8)
	}
	return out
}

func TestVaultBasicCRUDAndTypedGetters(t *testing.T) {
	v := NewMemoryVault()

	// Missing key behavior.
	if raw, ok, err := v.GetRaw("missing"); err != nil || ok || raw != nil {
		t.Fatalf("GetRaw(missing) = (%v, %v, %v), want (nil, false, nil)", raw, ok, err)
	}
	var dst map[string]string
	if ok, err := v.GetJSON("missing", &dst); err != nil || ok {
		t.Fatalf("GetJSON(missing) = (%v, %v), want (false, nil)", ok, err)
	}
	if val, ok := v.GetAny("missing"); ok || val != nil {
		t.Fatalf("GetAny(missing) = (%v, %v), want (nil, false)", val, ok)
	}
	if got := v.GetString("missing", "fallback"); got != "fallback" {
		t.Fatalf("GetString(missing) = %q, want %q", got, "fallback")
	}
	if got := v.GetMap("missing"); got != nil {
		t.Fatalf("GetMap(missing) = %v, want nil", got)
	}
	if got := v.GetStringSlice("missing"); got != nil {
		t.Fatalf("GetStringSlice(missing) = %v, want nil", got)
	}

	// String storage, including empty string vs null.
	if err := v.Set("assign_proxy", "socks5://alice:secret@127.0.0.1:1080"); err != nil {
		t.Fatalf("Set string failed: %v", err)
	}
	if got := v.GetString("assign_proxy", "default"); got != "socks5://alice:secret@127.0.0.1:1080" {
		t.Fatalf("GetString = %q", got)
	}
	if err := v.Set("empty_str", ""); err != nil {
		t.Fatalf("Set empty string failed: %v", err)
	}
	if got := v.GetString("empty_str", "default"); got != "" {
		t.Fatalf("GetString(empty_str) = %q, want empty string", got)
	}
	if err := v.Set("null_val", nil); err != nil {
		t.Fatalf("Set nil failed: %v", err)
	}
	if got := v.GetString("null_val", "default"); got != "default" {
		t.Fatalf("GetString(null_val) = %q, want %q", got, "default")
	}
	if val, ok := v.GetAny("null_val"); !ok || val != nil {
		t.Fatalf("GetAny(null_val) = (%v, %v), want (nil, true)", val, ok)
	}

	// Map storage & defensive copy semantics.
	profile := map[string]any{"email": "user@example.com", "sub": "sub-123"}
	if err := v.Set("google_profile", profile); err != nil {
		t.Fatalf("Set map failed: %v", err)
	}
	gotMap := v.GetMap("google_profile")
	if gotMap["email"] != "user@example.com" || gotMap["sub"] != "sub-123" {
		t.Fatalf("GetMap = %#v", gotMap)
	}
	gotMap["email"] = "mutated@example.com"
	if again := v.GetMap("google_profile"); again["email"] != "user@example.com" {
		t.Fatalf("cache was mutated via returned map: %#v", again)
	}

	// GetRaw defensive copy.
	raw1, ok, err := v.GetRaw("google_profile")
	if err != nil || !ok {
		t.Fatalf("GetRaw failed: ok=%v err=%v", ok, err)
	}
	raw1[0] = 'X'
	raw2, ok, err := v.GetRaw("google_profile")
	if err != nil || !ok || raw2[0] != '{' {
		t.Fatalf("cache was mutated via GetRaw slice: %q", string(raw2))
	}

	// String slice storage.
	eps := []string{"ep-1", "ep-2"}
	if err := v.Set("runtime_session_ids", eps); err != nil {
		t.Fatalf("Set slice failed: %v", err)
	}
	if got := v.GetStringSlice("runtime_session_ids"); !reflect.DeepEqual(got, eps) {
		t.Fatalf("GetStringSlice = %#v, want %#v", got, eps)
	}

	// Typed struct via GetJSON.
	type cred struct {
		Token        string `json:"token"`
		RefreshToken string `json:"refresh_token"`
	}
	wantCred := cred{Token: "tok-1", RefreshToken: "ref-2"}
	if err := v.Set("colab", wantCred); err != nil {
		t.Fatalf("Set struct failed: %v", err)
	}
	var gotCred cred
	if ok, err := v.GetJSON("colab", &gotCred); err != nil || !ok || gotCred != wantCred {
		t.Fatalf("GetJSON = (%v, %v, %+v), want (true, nil, %+v)", ok, err, gotCred, wantCred)
	}

	// Type mismatch returns default/nil safely.
	if got := v.GetString("colab", "fallback"); got != "fallback" {
		t.Fatalf("GetString on object = %q, want fallback", got)
	}
	if got := v.GetMap("assign_proxy"); got != nil {
		t.Fatalf("GetMap on string = %v, want nil", got)
	}
	if got := v.GetStringSlice("colab"); got != nil {
		t.Fatalf("GetStringSlice on object = %v, want nil", got)
	}

	// Overwrite and Delete.
	if err := v.Set("cloud_project", "proj-1"); err != nil {
		t.Fatalf("Set cloud_project failed: %v", err)
	}
	if err := v.Set("cloud_project", "proj-2"); err != nil {
		t.Fatalf("Overwrite cloud_project failed: %v", err)
	}
	if got := v.GetString("cloud_project", ""); got != "proj-2" {
		t.Fatalf("Overwritten value = %q, want proj-2", got)
	}
	if err := v.Delete("cloud_project"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if got := v.GetString("cloud_project", "gone"); got != "gone" {
		t.Fatalf("After delete GetString = %q, want gone", got)
	}
	// Deleting missing key is idempotent.
	if err := v.Delete("never_existed"); err != nil {
		t.Fatalf("Delete(missing) failed: %v", err)
	}
}

func TestVaultValidationAndInvalidJSON(t *testing.T) {
	v := NewMemoryVault()

	if err := v.Set("", "x"); err == nil {
		t.Fatal("Set empty key should fail")
	}
	if err := v.Set("bad\x00key", "x"); err == nil {
		t.Fatal("Set NUL key should fail")
	}
	if err := v.Delete(""); err == nil {
		t.Fatal("Delete empty key should fail")
	}
	if _, _, err := v.GetRaw(""); err == nil {
		t.Fatal("GetRaw empty key should fail")
	}
	if err := v.Set("nan", math.NaN()); err == nil {
		t.Fatal("Set(NaN) should fail JSON marshaling")
	}
	if err := v.Set("inf", math.Inf(1)); err == nil {
		t.Fatal("Set(+Inf) should fail JSON marshaling")
	}
}

func TestVaultCachingSemantics(t *testing.T) {
	spy := newSpyBackend()
	_ = spy.inner.Set("preexisting", []byte(`"cached-val"`))
	v := NewColabOneVault(spy)

	// Missing key should hit backend once, then serve negative cache.
	for i := 0; i < 4; i++ {
		if _, ok, err := v.GetRaw("absent"); err != nil || ok {
			t.Fatalf("iter %d: unexpected result ok=%v err=%v", i, ok, err)
		}
	}
	if got := spy.getCount("absent"); got != 1 {
		t.Fatalf("backend Get count for absent = %d, want 1", got)
	}

	// Preexisting key hits backend once, then cache.
	for i := 0; i < 4; i++ {
		if got := v.GetString("preexisting", ""); got != "cached-val" {
			t.Fatalf("iter %d: got %q, want cached-val", i, got)
		}
	}
	if got := spy.getCount("preexisting"); got != 1 {
		t.Fatalf("backend Get count for preexisting = %d, want 1", got)
	}

	// Set populates cache so subsequent Get does not hit backend.
	if err := v.Set("fresh", "v1"); err != nil {
		t.Fatalf("Set failed: %v", err)
	}
	before := spy.getCount("fresh")
	if got := v.GetString("fresh", ""); got != "v1" {
		t.Fatalf("got %q, want v1", got)
	}
	if got := spy.getCount("fresh"); got != before {
		t.Fatalf("Get after Set hit backend unexpectedly (%d -> %d)", before, got)
	}

	// Delete updates cache to negative so subsequent Get does not hit backend.
	if err := v.Delete("fresh"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	beforeDel := spy.getCount("fresh")
	if _, ok, err := v.GetRaw("fresh"); err != nil || ok {
		t.Fatalf("GetRaw after delete = (%v, %v)", ok, err)
	}
	if got := spy.getCount("fresh"); got != beforeDel {
		t.Fatalf("Get after Delete hit backend unexpectedly (%d -> %d)", beforeDel, got)
	}

	// Transient backend errors must NOT be cached.
	var failOnce atomic.Bool
	failOnce.Store(true)
	_ = spy.inner.Set("flaky", []byte(`"recovered"`))
	spy.mu.Lock()
	spy.failGet = func(key string) error {
		if key == "flaky" && failOnce.CompareAndSwap(true, false) {
			return errors.New("keychain temporarily locked")
		}
		return nil
	}
	spy.mu.Unlock()

	if _, ok, err := v.GetRaw("flaky"); err == nil || ok {
		t.Fatalf("first GetRaw(flaky) should return transient error, got ok=%v err=%v", ok, err)
	}
	if got := v.GetString("flaky", ""); got != "recovered" {
		t.Fatalf("second GetString(flaky) = %q, want recovered", got)
	}
}

func TestVaultLargeSecretChunkingAndCleanup(t *testing.T) {
	spy := newSpyBackend()
	v := NewColabOneVault(spy)

	// Build a >7KB payload containing multi-byte UTF-8 Chinese and emoji that cross 2400-byte boundaries.
	largePayload := map[string]any{
		"token": strings.Repeat("tok-中文🚀-", 550), // > 6KB UTF-8
		"notes": strings.Repeat("a", 2500),
		"items": []string{"ep-1", "ep-2", "ep-3"},
	}
	encodedLarge, err := json.Marshal(largePayload)
	if err != nil {
		t.Fatalf("marshal largePayload: %v", err)
	}
	if len(encodedLarge) <= 5*1024 {
		t.Fatalf("expected >5KB payload, got %d bytes", len(encodedLarge))
	}

	if err := v.Set("runtime_session", largePayload); err != nil {
		t.Fatalf("Set largePayload failed: %v", err)
	}

	// Every single write to backend must respect mygo.MaxSecretSize (2560).
	if spy.maxStored > mygo.MaxSecretSize {
		t.Fatalf("backend received %d bytes, exceeds mygo.MaxSecretSize (%d)", spy.maxStored, mygo.MaxSecretSize)
	}

	// Verify manifest stored at main key and exact chunks stored at #chunk:<gen>:<i>.
	manifestRaw, err := spy.inner.Get("runtime_session")
	if err != nil {
		t.Fatalf("manifest missing in backend: %v", err)
	}
	expectedChunks := (len(encodedLarge) + colabOneVaultChunkSize - 1) / colabOneVaultChunkSize
	gotManifest, isChunked := parseChunkManifest(manifestRaw)
	if !isChunked || gotManifest.Chunks != expectedChunks || gotManifest.Gen != 1 {
		t.Fatalf("manifest = %q -> (%+v, %v), want ({Chunks:%d Gen:1}, true)", string(manifestRaw), gotManifest, isChunked, expectedChunks)
	}

	// Read back from a brand-new cold-cache vault sharing the same backend.
	coldVault := NewColabOneVault(spy)
	gotMap := coldVault.GetMap("runtime_session")
	if gotMap == nil || gotMap["token"] != largePayload["token"] || gotMap["notes"] != largePayload["notes"] {
		t.Fatalf("cold vault reassembled payload mismatch")
	}

	// Overwrite from a cold vault with a smaller 2-chunk payload (~3500 bytes):
	// new generation 2 must be committed and ALL gen-1 chunks must be deleted!
	mediumPayload := map[string]any{"token": strings.Repeat("m", 3500)}
	coldWriter := NewColabOneVault(spy)
	if err := coldWriter.Set("runtime_session", mediumPayload); err != nil {
		t.Fatalf("overwrite with medium payload failed: %v", err)
	}
	manifestRaw2, err := spy.inner.Get("runtime_session")
	if err != nil {
		t.Fatalf("manifest missing after overwrite: %v", err)
	}
	gotManifest2, isChunked2 := parseChunkManifest(manifestRaw2)
	if !isChunked2 || gotManifest2.Chunks != 2 || gotManifest2.Gen != 2 {
		t.Fatalf("manifest after overwrite = %q -> (%+v, %v), want ({Chunks:2 Gen:2}, true)", string(manifestRaw2), gotManifest2, isChunked2)
	}
	for i := 0; i < expectedChunks; i++ {
		if _, err := spy.inner.Get(colabOneChunkKey("runtime_session", 1, i)); !errors.Is(err, ErrNotFound) {
			t.Fatalf("obsolete gen-1 chunk %d was not deleted (err=%v)", i, err)
		}
	}
	if keys := spy.keys(); len(keys) != 3 {
		t.Fatalf("expected manifest + 2 gen-2 chunks, got %v", keys)
	}
	if got := NewColabOneVault(spy).GetMap("runtime_session"); got["token"] != mediumPayload["token"] {
		t.Fatalf("medium payload mismatch after cold overwrite")
	}

	// Overwrite chunked secret with small unchunked secret: ALL old chunks must be deleted!
	smallPayload := map[string]any{"token": "tiny"}
	if err := coldWriter.Set("runtime_session", smallPayload); err != nil {
		t.Fatalf("overwrite with small payload failed: %v", err)
	}
	if keys := spy.keys(); len(keys) != 1 || keys[0] != "runtime_session" {
		t.Fatalf("expected only main key remaining, got %v", keys)
	}
	if got := NewColabOneVault(spy).GetMap("runtime_session"); got["token"] != "tiny" {
		t.Fatalf("small payload mismatch")
	}

	// Re-write a >5KB chunked secret, then Delete from a cold vault: ALL keys must be removed!
	if err := v.Set("runtime_session", largePayload); err != nil {
		t.Fatalf("re-set large payload failed: %v", err)
	}
	coldDeleter := NewColabOneVault(spy)
	if err := coldDeleter.Delete("runtime_session"); err != nil {
		t.Fatalf("cold Delete failed: %v", err)
	}
	if keys := spy.keys(); len(keys) != 0 {
		t.Fatalf("orphan keys remained after chunked Delete: %v", keys)
	}
}

func TestVaultChunkBoundariesAndErrors(t *testing.T) {
	// Exact boundary sizes: 2400 (single), 2401 (2 chunks), 4800 (2 chunks), 4801 (3 chunks).
	for _, totalBytes := range []int{2400, 2401, 4800, 4801} {
		spy := newSpyBackend()
		v := NewColabOneVault(spy)
		// JSON string encoding adds 2 quote bytes.
		val := strings.Repeat("x", totalBytes-2)
		if err := v.Set("edge", val); err != nil {
			t.Fatalf("size %d: Set failed: %v", totalBytes, err)
		}
		if got := NewColabOneVault(spy).GetString("edge", ""); got != val {
			t.Fatalf("size %d: round-trip mismatch (got len %d, want %d)", totalBytes, len(got), len(val))
		}
		wantKeyCount := 1
		if totalBytes > colabOneVaultChunkSize {
			wantKeyCount = 1 + (totalBytes+colabOneVaultChunkSize-1)/colabOneVaultChunkSize
		}
		if gotKeys := len(spy.keys()); gotKeys != wantKeyCount {
			t.Fatalf("size %d: backend key count = %d, want %d", totalBytes, gotKeys, wantKeyCount)
		}
	}

	// Missing chunk returns ErrNotFound-wrapped error and does not poison cache.
	spy := newSpyBackend()
	v := NewColabOneVault(spy)
	big := strings.Repeat("z", 5500)
	if err := v.Set("broken", big); err != nil {
		t.Fatalf("Set broken failed: %v", err)
	}
	_ = spy.inner.Delete(colabOneChunkKey("broken", 1, 1))

	reader := NewColabOneVault(spy)
	if _, ok, err := reader.GetRaw("broken"); err == nil || !errors.Is(err, ErrNotFound) || ok {
		t.Fatalf("expected missing chunk ErrNotFound, got ok=%v err=%v", ok, err)
	}

	// Partial write failure cleans up newly written chunks.
	spyFail := newSpyBackend()
	spyFail.failSet = func(key string, _ []byte) error {
		if key == colabOneChunkKey("partial", 1, 2) {
			return errors.New("disk full")
		}
		return nil
	}
	vFail := NewColabOneVault(spyFail)
	if err := vFail.Set("partial", strings.Repeat("p", 6000)); err == nil {
		t.Fatal("expected Set failure on chunk 2")
	}
	if keys := spyFail.keys(); len(keys) != 0 {
		t.Fatalf("expected orphan chunks cleaned up on failed write, remaining: %v", keys)
	}
}

func TestParseChunkManifestEdgeCases(t *testing.T) {
	cases := []struct {
		raw  string
		want colabOneChunkManifest
		ok   bool
	}{
		{`{"__colabone_chunks__":3}`, colabOneChunkManifest{Chunks: 3, Gen: 0}, true},
		{` { "__colabone_chunks__" : 12 } `, colabOneChunkManifest{Chunks: 12, Gen: 0}, true},
		{`{"__colabone_chunks__":4,"gen":7}`, colabOneChunkManifest{Chunks: 4, Gen: 7}, true},
		{`{"__colabone_chunks__":4,"gen":0}`, colabOneChunkManifest{Chunks: 4, Gen: 0}, true},
		{`{"__colabone_chunks__":4,"gen":-1}`, colabOneChunkManifest{}, false},
		{`{"__colabone_chunks__":4,"gen":1.5}`, colabOneChunkManifest{}, false},
		{`{"gen":2}`, colabOneChunkManifest{}, false},
		{`{"__colabone_chunks__":0}`, colabOneChunkManifest{}, false},
		{`{"__colabone_chunks__":-2}`, colabOneChunkManifest{}, false},
		{`{"__colabone_chunks__":999999}`, colabOneChunkManifest{}, false},
		{`{"__colabone_chunks__":2,"other":1}`, colabOneChunkManifest{}, false},
		{`{"__colabone_chunks__":2} extra`, colabOneChunkManifest{}, false},
		{`{"__colabone_chunks__":2.5}`, colabOneChunkManifest{}, false},
		{`{}`, colabOneChunkManifest{}, false},
		{`"__colabone_chunks__"`, colabOneChunkManifest{}, false},
		{`{"token":"abc"}`, colabOneChunkManifest{}, false},
	}
	for _, tc := range cases {
		got, ok := parseChunkManifest([]byte(tc.raw))
		if got != tc.want || ok != tc.ok {
			t.Errorf("parseChunkManifest(%q) = (%+v, %v), want (%+v, %v)", tc.raw, got, ok, tc.want, tc.ok)
		}
	}
}

func TestDecodeUTF16LE(t *testing.T) {
	jsonStr := `{"email":"用户@example.com","token":"ya29.test-🚀"}`

	// Plain UTF-16LE (standard Python keyring WinVaultKeyring encoding).
	decoded, err := decodeUTF16LE(encodeUTF16LE(jsonStr))
	if err != nil {
		t.Fatalf("decodeUTF16LE failed: %v", err)
	}
	if string(decoded) != jsonStr {
		t.Fatalf("decodeUTF16LE = %q, want %q", string(decoded), jsonStr)
	}

	// UTF-16LE with BOM (0xFF 0xFE) and trailing NULs.
	withBOMAndNUL := append([]byte{0xFF, 0xFE}, encodeUTF16LE(jsonStr)...)
	withBOMAndNUL = append(withBOMAndNUL, 0x00, 0x00, 0x00, 0x00)
	decoded, err = decodeUTF16LE(withBOMAndNUL)
	if err != nil {
		t.Fatalf("decodeUTF16LE(BOM+NUL) failed: %v", err)
	}
	if string(decoded) != jsonStr {
		t.Fatalf("decodeUTF16LE(BOM+NUL) = %q, want %q", string(decoded), jsonStr)
	}

	// Empty and NUL-only blobs return nil, nil.
	if b, err := decodeUTF16LE(nil); err != nil || b != nil {
		t.Fatalf("decodeUTF16LE(nil) = (%v, %v), want (nil, nil)", b, err)
	}
	if b, err := decodeUTF16LE([]byte{0, 0, 0, 0}); err != nil || b != nil {
		t.Fatalf("decodeUTF16LE(NULs) = (%v, %v), want (nil, nil)", b, err)
	}

	// Odd-length blob must return an error.
	if _, err := decodeUTF16LE([]byte{0x7B, 0x00, 0x7D}); err == nil {
		t.Fatal("decodeUTF16LE(odd) expected error, got nil")
	}
}

func TestLegacyWindowsMigrationAndCleanup(t *testing.T) {
	type credEntry struct {
		blob     []byte
		userName string
	}
	legacyStore := map[string]credEntry{
		"colab@ColabOne": {
			blob:     encodeUTF16LE(`{"token":"legacy-tok","refresh_token":"legacy-ref"}`),
			userName: "colab",
		},
		"ColabOne": {
			blob:     encodeUTF16LE(`"legacy-project-id"`),
			userName: "cloud_project",
		},
	}

	readLegacyTarget := func(target string) ([]byte, string, bool, error) {
		e, ok := legacyStore[target]
		if !ok {
			return nil, "", false, nil
		}
		return e.blob, e.userName, true, nil
	}
	deleteLegacyTarget := func(target string) error {
		delete(legacyStore, target)
		return nil
	}

	spy := newSpyBackend()
	v := NewColabOneVault(spy)
	v.legacyReader = func(service, key string) ([]byte, bool, error) {
		return colabOneResolveLegacyWinCredential(service, key, readLegacyTarget)
	}
	v.legacyDeleter = func(service, key string) error {
		return colabOneRemoveLegacyWinCredential(service, key, readLegacyTarget, deleteLegacyTarget)
	}

	// 1. Primary legacy target key@ColabOne is read, decoded, migrated into backend, and cleaned from legacy store.
	gotColab := v.GetMap("colab")
	if gotColab == nil || gotColab["token"] != "legacy-tok" || gotColab["refresh_token"] != "legacy-ref" {
		t.Fatalf("migrated colab = %#v", gotColab)
	}
	// Verify migrated UTF-8 JSON is now stored in the primary backend.
	migratedRaw, err := spy.inner.Get("colab")
	if err != nil {
		t.Fatalf("expected migrated entry in backend: %v", err)
	}
	if !bytes.Equal(migratedRaw, []byte(`{"token":"legacy-tok","refresh_token":"legacy-ref"}`)) {
		t.Fatalf("migrated raw bytes = %q", string(migratedRaw))
	}
	if _, exists := legacyStore["colab@ColabOne"]; exists {
		t.Fatal("expected migrated legacy colab@ColabOne entry removed after successful backend write")
	}

	// 2. Deleting "other_key" must NOT match or delete bare "ColabOne" (UserName is "cloud_project").
	if got := v.GetString("other_key", "missing"); got != "missing" {
		t.Fatalf("other_key unexpectedly matched bare ColabOne entry: %q", got)
	}
	if err := v.Delete("other_key"); err != nil {
		t.Fatalf("Delete(other_key) failed: %v", err)
	}
	if _, exists := legacyStore["ColabOne"]; !exists {
		t.Fatal("Delete(other_key) wrongly removed bare ColabOne owned by cloud_project")
	}

	// 3. Fallback legacy target "ColabOne" matches ONLY when UserName == key, migrates, and cleans legacy store.
	if got := v.GetString("cloud_project", ""); got != "legacy-project-id" {
		t.Fatalf("fallback legacy cloud_project = %q, want legacy-project-id", got)
	}
	if _, exists := legacyStore["ColabOne"]; exists {
		t.Fatal("expected migrated legacy bare ColabOne entry removed after successful backend write")
	}

	// 4. A cold vault WITHOUT legacyReader can now read both migrated keys straight from backend!
	cold := NewColabOneVault(spy)
	if cold.GetMap("colab")["token"] != "legacy-tok" {
		t.Fatalf("cold vault failed to read migrated colab")
	}
	if cold.GetString("cloud_project", "") != "legacy-project-id" {
		t.Fatalf("cold vault failed to read migrated cloud_project")
	}

	// 5. Deleting "colab" and "cloud_project" (plus an unmigrated legacy entry) removes all entries so they never resurrect.
	legacyStore["unmigrated@ColabOne"] = credEntry{
		blob:     encodeUTF16LE(`"legacy-unmigrated"`),
		userName: "unmigrated",
	}
	if err := v.Delete("unmigrated"); err != nil {
		t.Fatalf("Delete(unmigrated) failed: %v", err)
	}
	if err := v.Delete("colab"); err != nil {
		t.Fatalf("Delete(colab) failed: %v", err)
	}
	if err := v.Delete("cloud_project"); err != nil {
		t.Fatalf("Delete(cloud_project) failed: %v", err)
	}
	if len(legacyStore) != 0 {
		t.Fatalf("expected legacy store empty after deletes, remaining: %#v", legacyStore)
	}

	// Cold vault with legacyReader attached must see both deleted (no resurrection!).
	restarted := NewColabOneVault(spy)
	restarted.legacyReader = v.legacyReader
	if restarted.GetMap("colab") != nil || restarted.GetString("cloud_project", "none") != "none" {
		t.Fatal("deleted legacy credentials resurrected after restart")
	}
}

func TestLegacyLargeSecretMigrationUsesChunking(t *testing.T) {
	bigVal := map[string]any{"payload": strings.Repeat("大", 1200)} // > 3600 UTF-8 bytes
	bigJSON, err := json.Marshal(bigVal)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	legacyBlob := encodeUTF16LE(string(bigJSON))

	spy := newSpyBackend()
	v := NewColabOneVault(spy)
	v.legacyReader = func(service, key string) ([]byte, bool, error) {
		return colabOneResolveLegacyWinCredential(service, key, func(target string) ([]byte, string, bool, error) {
			if target == "big_legacy@ColabOne" {
				return legacyBlob, "big_legacy", true, nil
			}
			return nil, "", false, nil
		})
	}

	got := v.GetMap("big_legacy")
	if got == nil || got["payload"] != bigVal["payload"] {
		t.Fatalf("large legacy migration mismatch")
	}
	if spy.maxStored > mygo.MaxSecretSize {
		t.Fatalf("large legacy migration exceeded MaxSecretSize: %d", spy.maxStored)
	}
	if cold := NewColabOneVault(spy).GetMap("big_legacy"); cold == nil || cold["payload"] != bigVal["payload"] {
		t.Fatalf("cold read of migrated chunked legacy secret failed")
	}
}

func TestColabOneMyGoSecretBackendServiceDefault(t *testing.T) {
	var zero ColabOneMyGoSecretBackend
	if got := zero.serviceName(); got != ColabOneVaultService {
		t.Fatalf("zero-value serviceName() = %q, want %q", got, ColabOneVaultService)
	}
	if got := NewColabOneMyGoSecretBackend("").serviceName(); got != ColabOneVaultService {
		t.Fatalf("empty serviceName() = %q, want %q", got, ColabOneVaultService)
	}
	if got := NewColabOneMyGoSecretBackend("CustomSvc").serviceName(); got != "CustomSvc" {
		t.Fatalf("custom serviceName() = %q, want CustomSvc", got)
	}
	def := NewDefaultColabOneVault()
	if def == nil || def.service != ColabOneVaultService || def.backend == nil {
		t.Fatalf("NewDefaultColabOneVault returned invalid vault: %+v", def)
	}
}

func TestVaultConcurrencyRace(t *testing.T) {
	v := NewMemoryVault()
	const workers = 16
	const iters = 60

	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func(id int) {
			defer wg.Done()
			key := fmt.Sprintf("key-%d", id%4)
			large := strings.Repeat(fmt.Sprintf("worker-%d-", id), 400) // > 3.5KB
			for i := 0; i < iters; i++ {
				switch i % 6 {
				case 0:
					if err := v.Set(key, map[string]any{"id": id, "data": large}); err != nil {
						t.Errorf("Set large failed: %v", err)
					}
				case 1:
					if err := v.Set(key, "small-value"); err != nil {
						t.Errorf("Set small failed: %v", err)
					}
				case 2:
					_, _, _ = v.GetRaw(key)
				case 3:
					_ = v.GetMap(key)
					_ = v.GetString(key, "default")
				case 4:
					_, _ = v.GetAny(key)
					_ = v.GetStringSlice(key)
				case 5:
					if err := v.Delete(key); err != nil {
						t.Errorf("Delete failed: %v", err)
					}
				}
			}
		}(w)
	}
	wg.Wait()
}

func TestLegacyWinCredentialPrefersBareServiceOverCompoundTargetAndUTF8Fallback(t *testing.T) {
	// Python keyring WinVaultKeyring writes the NEWEST credential to bare "ColabOne"
	// (with UserName == key) and moves the previous stale value to "key@ColabOne".
	store := map[string]struct {
		blob     []byte
		userName string
	}{
		"ColabOne": {
			blob:     encodeUTF16LE(`{"token":"fresh-account-b"}`),
			userName: "colab",
		},
		"colab@ColabOne": {
			blob:     encodeUTF16LE(`{"token":"stale-account-a"}`),
			userName: "colab",
		},
		"odd@ColabOne": {
			blob:     []byte(`{"k":"v"}`), // 9 bytes (odd length) -> UTF-8 fallback
			userName: "odd",
		},
	}
	readTarget := func(target string) ([]byte, string, bool, error) {
		item, ok := store[target]
		if !ok {
			return nil, "", false, nil
		}
		return item.blob, item.userName, true, nil
	}

	got, found, err := colabOneResolveLegacyWinCredential("ColabOne", "colab", readTarget)
	if err != nil || !found {
		t.Fatalf("resolve colab failed: found=%v err=%v", found, err)
	}
	if string(got) != `{"token":"fresh-account-b"}` {
		t.Fatalf("migrated stale compound credential instead of newest bare service: %q", string(got))
	}

	gotOdd, foundOdd, errOdd := colabOneResolveLegacyWinCredential("ColabOne", "odd", readTarget)
	if errOdd != nil || !foundOdd || string(gotOdd) != `{"k":"v"}` {
		t.Fatalf("odd-length UTF-8 legacy fallback failed: got=%q found=%v err=%v", string(gotOdd), foundOdd, errOdd)
	}

	// Even-length UTF-8 JSON (16 bytes) must decode cleanly as UTF-8 instead of UTF-16LE CJK mojibake.
	evenUTF8 := []byte(`{"token":"abcd"}`)
	if len(evenUTF8)%2 != 0 {
		t.Fatalf("expected even length test input, got %d", len(evenUTF8))
	}
	gotEven, errEven := decodeLegacyWinBlob(evenUTF8)
	if errEven != nil || string(gotEven) != string(evenUTF8) {
		t.Fatalf("even-length UTF-8 JSON corrupted: got=%q err=%v", string(gotEven), errEven)
	}

	// Lone UTF-16 surrogate must be rejected by decodeUTF16LE.
	loneSurrogate := []byte{0x00, 0xD8, 0x41, 0x00}
	if _, err := decodeUTF16LE(loneSurrogate); err == nil {
		t.Fatal("expected decodeUTF16LE to reject unpaired surrogate")
	}
}

func TestVaultSetRemovesSupersededLegacyWindowsEntries(t *testing.T) {
	type credEntry struct {
		blob     []byte
		userName string
	}
	legacyStore := map[string]credEntry{
		"colab@ColabOne": {
			blob:     encodeUTF16LE(`{"token":"old-account-a"}`),
			userName: "colab",
		},
		"ColabOne": {
			blob:     encodeUTF16LE(`{"token":"old-account-a-bare"}`),
			userName: "colab",
		},
		"hf_token@ColabOne": {
			blob:     encodeUTF16LE(`"hf_legacy_old"`),
			userName: "hf_token",
		},
		"other@ColabOne": {
			blob:     encodeUTF16LE(`"keep-me"`),
			userName: "other",
		},
	}

	readLegacyTarget := func(target string) ([]byte, string, bool, error) {
		e, ok := legacyStore[target]
		if !ok {
			return nil, "", false, nil
		}
		return e.blob, e.userName, true, nil
	}
	deleteLegacyTarget := func(target string) error {
		delete(legacyStore, target)
		return nil
	}

	var deleterCalls []string
	var deleterErr error

	spy := newSpyBackend()
	v := NewColabOneVault(spy)
	v.legacyReader = func(service, key string) ([]byte, bool, error) {
		return colabOneResolveLegacyWinCredential(service, key, readLegacyTarget)
	}
	v.legacyDeleter = func(service, key string) error {
		deleterCalls = append(deleterCalls, service+"/"+key)
		_ = colabOneRemoveLegacyWinCredential(service, key, readLegacyTarget, deleteLegacyTarget)
		return deleterErr
	}

	// Failed Set must NOT remove legacy entries.
	spy.failSet = func(key string, _ []byte) error {
		return errors.New("backend unavailable")
	}
	if err := v.Set("colab", map[string]any{"token": "new-account-b"}); err == nil {
		t.Fatal("expected Set failure when backend fails")
	}
	if len(deleterCalls) != 0 {
		t.Fatalf("legacyDeleter called on failed Set: %v", deleterCalls)
	}
	if _, ok := legacyStore["colab@ColabOne"]; !ok {
		t.Fatal("failed Set removed legacy colab@ColabOne entry")
	}
	if _, ok := legacyStore["ColabOne"]; !ok {
		t.Fatal("failed Set removed legacy bare ColabOne entry")
	}

	// Successful Set invokes legacyDeleter (best-effort, ignoring deleter error)
	// and removes superseded legacy entries only for the updated key.
	spy.failSet = nil
	deleterErr = errors.New("best-effort cleanup error")
	if err := v.Set("colab", map[string]any{"token": "new-account-b"}); err != nil {
		t.Fatalf("Set(colab) failed: %v", err)
	}
	if len(deleterCalls) != 1 || deleterCalls[0] != "ColabOne/colab" {
		t.Fatalf("legacyDeleter calls = %v, want [ColabOne/colab]", deleterCalls)
	}
	if _, ok := legacyStore["colab@ColabOne"]; ok {
		t.Fatal("superseded colab@ColabOne remained after Set(colab)")
	}
	if _, ok := legacyStore["ColabOne"]; ok {
		t.Fatal("superseded bare ColabOne (UserName=colab) remained after Set(colab)")
	}
	if _, ok := legacyStore["hf_token@ColabOne"]; !ok {
		t.Fatal("Set(colab) wrongly removed unrelated hf_token@ColabOne")
	}
	if _, ok := legacyStore["other@ColabOne"]; !ok {
		t.Fatal("Set(colab) wrongly removed unrelated other@ColabOne")
	}

	// Updating hf_token removes hf_token@ColabOne too.
	deleterErr = nil
	if err := v.Set("hf_token", "hf_new_token"); err != nil {
		t.Fatalf("Set(hf_token) failed: %v", err)
	}
	if _, ok := legacyStore["hf_token@ColabOne"]; ok {
		t.Fatal("superseded hf_token@ColabOne remained after Set(hf_token)")
	}
	if _, ok := legacyStore["other@ColabOne"]; !ok {
		t.Fatal("Set(hf_token) wrongly removed unrelated other@ColabOne")
	}

	// Even if primary backend entry is later absent, superseded legacy account A cannot resurrect.
	_ = spy.inner.Delete("colab")
	restarted := NewColabOneVault(spy)
	restarted.legacyReader = v.legacyReader
	if got := restarted.GetMap("colab"); got != nil {
		t.Fatalf("superseded legacy credential resurrected after Set: %#v", got)
	}
}

func TestVaultChunkedOverwriteMidWriteFailurePreservesPreviousSecret(t *testing.T) {
	spy := newSpyBackend()
	v := NewColabOneVault(spy)

	v1 := map[string]any{
		"version": "v1",
		"token":   strings.Repeat("alpha-旧-", 600), // > 5KB (3+ chunks)
	}
	v2 := map[string]any{
		"version": "v2",
		"token":   strings.Repeat("beta-新-", 700), // > 6KB (3+ chunks)
	}

	if err := v.Set("runtime_session", v1); err != nil {
		t.Fatalf("initial Set(v1) failed: %v", err)
	}
	v1Raw, err := json.Marshal(v1)
	if err != nil {
		t.Fatalf("marshal v1: %v", err)
	}
	v1Chunks := (len(v1Raw) + colabOneVaultChunkSize - 1) / colabOneVaultChunkSize
	if v1Chunks < 3 {
		t.Fatalf("expected at least 3 chunks for v1, got %d", v1Chunks)
	}

	// 1. Fail mid-write on chunk i=1 (i > 0) of generation 2 during warm overwrite.
	spy.mu.Lock()
	spy.failSet = func(key string, _ []byte) error {
		if key == colabOneChunkKey("runtime_session", 2, 1) {
			return errors.New("injected mid-write chunk failure")
		}
		return nil
	}
	spy.mu.Unlock()

	if err := v.Set("runtime_session", v2); err == nil {
		t.Fatal("expected Set(v2) to fail on chunk 1 of gen 2")
	}

	// Warm vault and cold vault must BOTH read v1 100% intact.
	if got := v.GetMap("runtime_session"); got == nil || got["version"] != "v1" || got["token"] != v1["token"] {
		t.Fatalf("warm vault corrupted after mid-write failure: %#v", got)
	}
	if got := NewColabOneVault(spy).GetMap("runtime_session"); got == nil || got["version"] != "v1" || got["token"] != v1["token"] {
		t.Fatalf("cold vault corrupted after mid-write failure: %#v", got)
	}
	// Orphan gen-2 chunk 0 must be cleaned up; only manifest + gen-1 chunks remain.
	if _, err := spy.inner.Get(colabOneChunkKey("runtime_session", 2, 0)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("orphan gen-2 chunk 0 remained after failure (err=%v)", err)
	}
	if gotKeys := len(spy.keys()); gotKeys != 1+v1Chunks {
		t.Fatalf("backend key count after failed overwrite = %d, want %d (keys=%v)", gotKeys, 1+v1Chunks, spy.keys())
	}

	// 2. Fail on manifest write after all gen-2 chunks were written:
	// all gen-2 chunks must be rolled back and v1 must stay 100% intact.
	spy.mu.Lock()
	spy.failSet = func(key string, _ []byte) error {
		if key == "runtime_session" {
			return errors.New("injected manifest write failure")
		}
		return nil
	}
	spy.mu.Unlock()

	if err := v.Set("runtime_session", v2); err == nil {
		t.Fatal("expected Set(v2) to fail on manifest write")
	}
	if got := NewColabOneVault(spy).GetMap("runtime_session"); got == nil || got["version"] != "v1" || got["token"] != v1["token"] {
		t.Fatalf("cold vault corrupted after manifest write failure: %#v", got)
	}
	if gotKeys := len(spy.keys()); gotKeys != 1+v1Chunks {
		t.Fatalf("backend key count after manifest failure = %d, want %d (keys=%v)", gotKeys, 1+v1Chunks, spy.keys())
	}

	// 3. Fail mid-write on chunk i=2 from a cold vault overwriting existing gen-1 secret.
	spy.mu.Lock()
	spy.failSet = func(key string, _ []byte) error {
		if key == colabOneChunkKey("runtime_session", 2, 2) {
			return errors.New("injected cold mid-write chunk failure")
		}
		return nil
	}
	spy.mu.Unlock()

	if err := NewColabOneVault(spy).Set("runtime_session", v2); err == nil {
		t.Fatal("expected cold Set(v2) to fail on chunk 2 of gen 2")
	}
	if got := NewColabOneVault(spy).GetMap("runtime_session"); got == nil || got["version"] != "v1" || got["token"] != v1["token"] {
		t.Fatalf("cold vault corrupted after cold-writer mid-write failure: %#v", got)
	}
	if gotKeys := len(spy.keys()); gotKeys != 1+v1Chunks {
		t.Fatalf("backend key count after cold-writer failure = %d, want %d (keys=%v)", gotKeys, 1+v1Chunks, spy.keys())
	}

	// 4. Clear fault injection: retry Set(v2) succeeds at gen 2 and cleans up ALL gen-1 chunks.
	spy.mu.Lock()
	spy.failSet = nil
	spy.mu.Unlock()

	if err := v.Set("runtime_session", v2); err != nil {
		t.Fatalf("retry Set(v2) failed: %v", err)
	}
	if got := NewColabOneVault(spy).GetMap("runtime_session"); got == nil || got["version"] != "v2" || got["token"] != v2["token"] {
		t.Fatalf("cold vault mismatch after successful retry Set(v2)")
	}
	for i := 0; i < v1Chunks; i++ {
		if _, err := spy.inner.Get(colabOneChunkKey("runtime_session", 1, i)); !errors.Is(err, ErrNotFound) {
			t.Fatalf("old gen-1 chunk %d leaked after gen-2 commit (err=%v)", i, err)
		}
	}
}

func TestVaultLegacyGen0ChunkedSecretBackwardCompatibility(t *testing.T) {
	seedLegacyGen0 := func(spy *spyBackend, name string, payload any) int {
		t.Helper()
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal legacy payload: %v", err)
		}
		chunks := (len(raw) + colabOneVaultChunkSize - 1) / colabOneVaultChunkSize
		if chunks < 2 {
			t.Fatalf("expected multi-chunk payload, got %d chunks", chunks)
		}
		for i := 0; i < chunks; i++ {
			start := i * colabOneVaultChunkSize
			end := start + colabOneVaultChunkSize
			if end > len(raw) {
				end = len(raw)
			}
			// Legacy gen==0 chunk key format: name#chunk:<i>
			if err := spy.inner.Set(fmt.Sprintf("%s#chunk:%d", name, i), raw[start:end]); err != nil {
				t.Fatalf("seed legacy chunk %d: %v", i, err)
			}
		}
		// Legacy manifest has NO "gen" field.
		if err := spy.inner.Set(name, []byte(fmt.Sprintf(`{"__colabone_chunks__":%d}`, chunks))); err != nil {
			t.Fatalf("seed legacy manifest: %v", err)
		}
		return chunks
	}

	// 1. Reading a legacy gen==0 chunked secret works seamlessly.
	spy := newSpyBackend()
	origPayload := map[string]any{
		"kind": "legacy-gen0",
		"data": strings.Repeat("旧数据-", 700), // > 6KB
	}
	origChunks := seedLegacyGen0(spy, "legacy_session", origPayload)

	v := NewColabOneVault(spy)
	got := v.GetMap("legacy_session")
	if got == nil || got["kind"] != "legacy-gen0" || got["data"] != origPayload["data"] {
		t.Fatalf("failed to read legacy gen==0 chunked secret: %#v", got)
	}

	// 2. Mid-write failure while upgrading legacy gen==0 -> gen==1 preserves legacy secret intact.
	newPayload := map[string]any{
		"kind": "upgraded-gen1",
		"data": strings.Repeat("新数据-", 700),
	}
	spy.mu.Lock()
	spy.failSet = func(key string, _ []byte) error {
		if key == colabOneChunkKey("legacy_session", 1, 1) {
			return errors.New("injected gen-1 chunk failure")
		}
		return nil
	}
	spy.mu.Unlock()

	if err := v.Set("legacy_session", newPayload); err == nil {
		t.Fatal("expected upgrade Set to fail on gen-1 chunk 1")
	}
	if cold := NewColabOneVault(spy).GetMap("legacy_session"); cold == nil || cold["kind"] != "legacy-gen0" || cold["data"] != origPayload["data"] {
		t.Fatalf("legacy gen==0 secret was torn on failed upgrade: %#v", cold)
	}
	if gotKeys := len(spy.keys()); gotKeys != 1+origChunks {
		t.Fatalf("expected only legacy keys remaining after failed upgrade, got %v", spy.keys())
	}

	// 3. Successful overwrite upgrades manifest to gen==1 and deletes ALL legacy #chunk:<i> keys.
	spy.mu.Lock()
	spy.failSet = nil
	spy.mu.Unlock()

	if err := v.Set("legacy_session", newPayload); err != nil {
		t.Fatalf("upgrade Set failed: %v", err)
	}
	manifestRaw, err := spy.inner.Get("legacy_session")
	if err != nil {
		t.Fatalf("get upgraded manifest: %v", err)
	}
	manifest, ok := parseChunkManifest(manifestRaw)
	if !ok || manifest.Gen != 1 {
		t.Fatalf("upgraded manifest = %q -> (%+v, %v), want gen=1", string(manifestRaw), manifest, ok)
	}
	for i := 0; i < origChunks; i++ {
		if _, err := spy.inner.Get(fmt.Sprintf("legacy_session#chunk:%d", i)); !errors.Is(err, ErrNotFound) {
			t.Fatalf("legacy gen==0 chunk %d was not deleted after gen-1 commit (err=%v)", i, err)
		}
	}
	if cold := NewColabOneVault(spy).GetMap("legacy_session"); cold == nil || cold["kind"] != "upgraded-gen1" || cold["data"] != newPayload["data"] {
		t.Fatalf("cold read after gen-1 upgrade mismatch")
	}

	// 4. Overwriting a legacy gen==0 chunked secret with a small unchunked secret deletes legacy chunks.
	spySmall := newSpyBackend()
	seedLegacyGen0(spySmall, "legacy_to_small", origPayload)
	if err := NewColabOneVault(spySmall).Set("legacy_to_small", "tiny"); err != nil {
		t.Fatalf("Set small over legacy gen==0 failed: %v", err)
	}
	if keys := spySmall.keys(); len(keys) != 1 || keys[0] != "legacy_to_small" {
		t.Fatalf("expected only main key after small overwrite of legacy gen==0, got %v", keys)
	}
	if got := NewColabOneVault(spySmall).GetString("legacy_to_small", ""); got != "tiny" {
		t.Fatalf("small value after legacy gen==0 overwrite = %q, want tiny", got)
	}

	// 5. Deleting a legacy gen==0 chunked secret from a cold vault removes manifest + all #chunk:<i> keys.
	spyDel := newSpyBackend()
	seedLegacyGen0(spyDel, "legacy_delete", origPayload)
	if err := NewColabOneVault(spyDel).Delete("legacy_delete"); err != nil {
		t.Fatalf("Delete legacy gen==0 failed: %v", err)
	}
	if keys := spyDel.keys(); len(keys) != 0 {
		t.Fatalf("orphan legacy gen==0 keys remained after Delete: %v", keys)
	}
}

func TestVaultEphemeralKeysEvictedOnDeleteWithoutResurrection(t *testing.T) {
	type credEntry struct {
		blob     []byte
		userName string
	}
	legacyStore := map[string]credEntry{
		"runtime_session:ep-0@ColabOne": {
			blob:     encodeUTF16LE(`{"endpoint":"ep-0","token":"legacy-session-tok"}`),
			userName: "runtime_session:ep-0",
		},
		"remote:ep-0@ColabOne": {
			blob:     encodeUTF16LE(`{"endpoint":"ep-0","url":"https://legacy.example"}`),
			userName: "remote:ep-0",
		},
	}

	readLegacyTarget := func(target string) ([]byte, string, bool, error) {
		e, ok := legacyStore[target]
		if !ok {
			return nil, "", false, nil
		}
		return e.blob, e.userName, true, nil
	}
	deleteLegacyTarget := func(target string) error {
		delete(legacyStore, target)
		return nil
	}

	spy := newSpyBackend()
	v := NewColabOneVault(spy)
	v.legacyReader = func(service, key string) ([]byte, bool, error) {
		return colabOneResolveLegacyWinCredential(service, key, readLegacyTarget)
	}
	v.legacyDeleter = func(service, key string) error {
		return colabOneRemoveLegacyWinCredential(service, key, readLegacyTarget, deleteLegacyTarget)
	}

	// Simulate long multi-session uptime with repeated Set/Delete of both
	// unchunked and chunked ephemeral per-endpoint keys.
	const sessions = 64
	largeToken := strings.Repeat("tok-chunk-", 320) // > 3KB (multi-chunk)
	for i := 0; i < sessions; i++ {
		ep := fmt.Sprintf("ep-%d", i)
		sessionKey := "runtime_session:" + ep
		remoteKey := "remote:" + ep

		token := "tok-" + ep
		if i%16 == 0 {
			token = largeToken + ep
		}
		if err := v.Set(sessionKey, map[string]any{"endpoint": ep, "token": token}); err != nil {
			t.Fatalf("Set(%s) failed: %v", sessionKey, err)
		}
		if err := v.Set(remoteKey, map[string]any{"endpoint": ep, "url": "https://" + ep + ".example"}); err != nil {
			t.Fatalf("Set(%s) failed: %v", remoteKey, err)
		}

		// Positive cache hit works while active.
		if got := v.GetMap(sessionKey); got == nil || got["token"] != token {
			t.Fatalf("GetMap(%s) mismatch while active", sessionKey)
		}
		if got := v.GetMap(remoteKey); got == nil || got["endpoint"] != ep {
			t.Fatalf("GetMap(%s) mismatch while active", remoteKey)
		}

		if err := v.Delete(sessionKey); err != nil {
			t.Fatalf("Delete(%s) failed: %v", sessionKey, err)
		}
		if err := v.Delete(remoteKey); err != nil {
			t.Fatalf("Delete(%s) failed: %v", remoteKey, err)
		}
	}

	// In-memory cache, chunk metadata, primary backend, and legacy store must all be empty (0 tombstones).
	v.mu.RLock()
	cacheLen := len(v.cache)
	chunkMetaLen := len(v.chunkMeta)
	v.mu.RUnlock()
	if cacheLen != 0 || chunkMetaLen != 0 {
		t.Fatalf("ephemeral tombstones leaked: len(cache)=%d len(chunkMeta)=%d, want 0/0", cacheLen, chunkMetaLen)
	}
	if keys := spy.keys(); len(keys) != 0 {
		t.Fatalf("backend keys leaked after ephemeral deletes: %v", keys)
	}
	if len(legacyStore) != 0 {
		t.Fatalf("legacy store entries leaked after ephemeral deletes: %#v", legacyStore)
	}

	// Subsequent reads on both warm and cold vaults must observe absent without resurrecting
	// anything AND without repopulating ephemeral negative tombstones.
	cold := NewColabOneVault(spy)
	cold.legacyReader = v.legacyReader
	for _, key := range []string{"runtime_session:ep-0", "remote:ep-0", "runtime_session:ep-63", "remote:ep-63"} {
		if raw, ok, err := v.GetRaw(key); err != nil || ok || raw != nil {
			t.Fatalf("warm GetRaw(%s) = (%v, %v, %v), want (nil, false, nil)", key, raw, ok, err)
		}
		if raw, ok, err := cold.GetRaw(key); err != nil || ok || raw != nil {
			t.Fatalf("cold GetRaw(%s) = (%v, %v, %v), want (nil, false, nil)", key, raw, ok, err)
		}
	}
	v.mu.RLock()
	cacheLenAfterGet := len(v.cache)
	chunkMetaLenAfterGet := len(v.chunkMeta)
	v.mu.RUnlock()
	if cacheLenAfterGet != 0 || chunkMetaLenAfterGet != 0 {
		t.Fatalf("GetRaw repopulated ephemeral tombstones: len(cache)=%d len(chunkMeta)=%d, want 0/0", cacheLenAfterGet, chunkMetaLenAfterGet)
	}

	// Persistent singleton keys ("runtime_session", "remote", "colab") must still retain negative tombstones.
	for _, persistentKey := range []string{"runtime_session", "remote", "colab"} {
		if err := v.Set(persistentKey, map[string]any{"k": "v"}); err != nil {
			t.Fatalf("Set(%s) failed: %v", persistentKey, err)
		}
		if err := v.Delete(persistentKey); err != nil {
			t.Fatalf("Delete(%s) failed: %v", persistentKey, err)
		}
		before := spy.getCount(persistentKey)
		if raw, ok, err := v.GetRaw(persistentKey); err != nil || ok || raw != nil {
			t.Fatalf("GetRaw(%s) after delete = (%v, %v, %v), want (nil, false, nil)", persistentKey, raw, ok, err)
		}
		if got := spy.getCount(persistentKey); got != before {
			t.Fatalf("persistent key %q did not use negative cache after delete (%d -> %d)", persistentKey, before, got)
		}
	}
}

func TestVaultChunkReadErrorDoesNotPoisonNegativeCache(t *testing.T) {
	spy := newSpyBackend()
	writer := NewColabOneVault(spy)
	largeVal := strings.Repeat("A", colabOneVaultChunkSize+1024)
	if err := writer.Set("chunked_flaky", largeVal); err != nil {
		t.Fatalf("Set large chunked value failed: %v", err)
	}

	cold := NewColabOneVault(spy)
	var failChunkOnce atomic.Bool
	failChunkOnce.Store(true)
	spy.mu.Lock()
	spy.failGet = func(key string) error {
		if strings.HasPrefix(key, "chunked_flaky"+colabOneChunkKeySep) && failChunkOnce.CompareAndSwap(true, false) {
			return errors.New("transient chunk read failure")
		}
		return nil
	}
	spy.mu.Unlock()

	if _, ok, err := cold.GetRaw("chunked_flaky"); err == nil || ok {
		t.Fatalf("expected transient chunk read error without negative hit, got ok=%v err=%v", ok, err)
	}

	// Next read must succeed and NOT return a poisoned negative cache entry.
	var got string
	if ok, err := cold.GetJSON("chunked_flaky", &got); err != nil || !ok || got != largeVal {
		t.Fatalf("GetJSON after transient chunk error = (ok=%v, err=%v, len=%d), want (true, nil, %d)", ok, err, len(got), len(largeVal))
	}
}
