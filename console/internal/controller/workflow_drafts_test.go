package controller

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

func newDraftTestController(t *testing.T, cfgDir string, comfy ComfyValidator) *ColabOneController {
	t.Helper()
	if comfy == nil {
		comfy = realComfyTestAdapter{}
	}
	ctrl := NewColabOneController(ColabOneControllerConfig{
		Root:           cfgDir,
		ConfigDir:      cfgDir,
		Background:     false,
		Vault:          NewColabOneMemoryVault(),
		Auth:           newMockAuth(),
		Provider:       newMockProvider(NewColabOneMemoryVault()),
		Models:         &mockModels{},
		Hub:            &mockHub{},
		Comfy:          comfy,
		PublicAPI:      mockPublicAPI{},
		AssignProxy:    &mockAssignProxy{},
		Shortcuts:      &mockShortcuts{},
		ServiceBundler: defaultMockBundler(),
		Location:       mockLocation{},
	})
	t.Cleanup(func() { ctrl.Close() })
	return ctrl
}

func mustJSONRoundTripMap(t *testing.T, v any) map[string]any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	var out map[string]any
	if err := decodeJSONWithNumbers(b, &out); err != nil {
		t.Fatalf("decodeJSONWithNumbers failed: %v", err)
	}
	return out
}

func TestWorkflowDraftPersistenceAcrossRestart(t *testing.T) {
	ctx := context.Background()
	cfgDir := t.TempDir()

	ctrl1 := newDraftTestController(t, cfgDir, realComfyTestAdapter{})

	// Initially empty get returns nil draft and empty drafts map.
	emptyOne, err := ctrl1.Command(ctx, "comfyui.draft.get", map[string]any{"workflow_id": "wf-txt2img"})
	if err != nil {
		t.Fatalf("initial draft.get(wf-txt2img) failed: %v", err)
	}
	if emptyOne.(map[string]any)["draft"] != nil {
		t.Fatalf("expected untyped nil draft when absent, got %#v", emptyOne)
	}
	emptyAll, err := ctrl1.Command(ctx, "comfyui.draft.get", map[string]any{})
	if err != nil {
		t.Fatalf("initial draft.get(all) failed: %v", err)
	}
	emptyAllJSON := mustJSONRoundTripMap(t, emptyAll)
	if draftsMap := asMap(emptyAllJSON["drafts"]); draftsMap == nil || len(draftsMap) != 0 {
		t.Fatalf("expected empty non-nil drafts map, got %#v", emptyAllJSON)
	}

	// Save text/numeric/bool fields (including 64-bit seed via json.Number and nil ignored key).
	saveRes, err := ctrl1.Command(ctx, "comfyui.draft.save", map[string]any{
		"workflow_id": "wf-txt2img",
		"fields": map[string]any{
			"6.text":     "cinematic portrait of a fox",
			"3.seed":     json.Number("1085739284759283741"),
			"3.cfg":      7.5,
			"3.steps":    28,
			"9.upscale":  true,
			"ignored":    nil,
			"node:1.key": "colon-supported",
		},
	})
	if err != nil {
		t.Fatalf("comfyui.draft.save failed: %v", err)
	}
	saveMap := saveRes.(map[string]any)
	if !asBool(saveMap["ok"]) {
		t.Fatalf("expected ok=true, got %#v", saveMap)
	}
	savedDraft, ok := saveMap["draft"].(*ColabOneWorkflowDraft)
	if !ok || savedDraft == nil {
		t.Fatalf("expected *ColabOneWorkflowDraft, got %T", saveMap["draft"])
	}
	if _, hasIgnored := savedDraft.Fields["ignored"]; hasIgnored {
		t.Fatalf("expected nil field key to be omitted, got %#v", savedDraft.Fields)
	}
	if savedDraft.Fields["3.seed"] != int64(1085739284759283741) {
		t.Fatalf("seed precision lost: %#v (%T)", savedDraft.Fields["3.seed"], savedDraft.Fields["3.seed"])
	}

	// Save image media and audio media; verify text fields are preserved.
	imgBytes := []byte("\x89PNG\r\n\x1a\nfake-png-content-123456")
	imgB64 := base64.StdEncoding.EncodeToString(imgBytes)
	imgSHA := sha256.Sum256(imgBytes)
	imgSHAHex := hex.EncodeToString(imgSHA[:])

	imgRes, err := ctrl1.Command(ctx, "comfyui.draft.media.save", map[string]any{
		"workflow_id": "wf-txt2img",
		"field_key":   "10.image",
		"filename":    `C:\Users\alice\Pictures\hero` + "\x00\n" + `.PNG`,
		"kind":        "image",
		"data":        "data:image/png;base64," + imgB64,
	})
	if err != nil {
		t.Fatalf("comfyui.draft.media.save (image) failed: %v", err)
	}
	imgResMap := imgRes.(map[string]any)
	imgMeta, ok := imgResMap["media"].(ColabOneWorkflowDraftMedia)
	if !ok {
		t.Fatalf("expected ColabOneWorkflowDraftMedia, got %T", imgResMap["media"])
	}
	if imgMeta.Filename != "hero.PNG" || imgMeta.Kind != "image" || imgMeta.MIME != "image/png" ||
		imgMeta.Size != int64(len(imgBytes)) || imgMeta.SHA256 != imgSHAHex || !reHex32ID.MatchString(imgMeta.ID) {
		t.Fatalf("unexpected image media metadata: %+v", imgMeta)
	}

	// Verify file permissions 0o600 on Unix.
	mediaFile := filepath.Join(cfgDir, "comfyui-drafts", "media", imgMeta.ID+".png")
	info, err := os.Lstat(mediaFile)
	if err != nil {
		t.Fatalf("expected persisted media file %s: %v", mediaFile, err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("expected media file perm 0600, got %o", info.Mode().Perm())
	}

	// Save audio media for the same workflow.
	audioBytes := []byte("RIFF\x24\x00\x00\x00WAVEfmt-audio-bytes")
	audioB64 := base64.StdEncoding.EncodeToString(audioBytes)
	audioSHA := sha256.Sum256(audioBytes)
	audioSHAHex := hex.EncodeToString(audioSHA[:])

	audioRes, err := ctrl1.Command(ctx, "comfyui.draft.media.save", map[string]any{
		"workflow_id": "wf-txt2img",
		"field_key":   "12.audio",
		"filename":    "/var/data/voice_clip.wav",
		"kind":        "audio",
		"data":        audioB64,
	})
	if err != nil {
		t.Fatalf("comfyui.draft.media.save (audio) failed: %v", err)
	}
	audioMeta := audioRes.(map[string]any)["media"].(ColabOneWorkflowDraftMedia)
	if audioMeta.Filename != "voice_clip.wav" || audioMeta.Kind != "audio" || audioMeta.MIME != "audio/wav" ||
		audioMeta.Size != int64(len(audioBytes)) || audioMeta.SHA256 != audioSHAHex {
		t.Fatalf("unexpected audio media metadata: %+v", audioMeta)
	}

	// Update text fields again and verify existing media entries are preserved!
	if _, err := ctrl1.Command(ctx, "comfyui.draft.save", map[string]any{
		"workflow_id": "wf-txt2img",
		"fields": map[string]any{
			"6.text":   "updated fox prompt",
			"3.seed":   json.Number("1085739284759283741"),
			"10.image": "hero.PNG",
			"12.audio": "voice_clip.wav",
		},
	}); err != nil {
		t.Fatalf("second comfyui.draft.save failed: %v", err)
	}

	// Close controller and construct a brand-new controller pointing to the same cfgDir.
	ctrl1.Close()
	ctrl2 := newDraftTestController(t, cfgDir, realComfyTestAdapter{})

	// Verify single-workflow draft survived restart.
	gotOne, err := ctrl2.Command(ctx, "comfyui.draft.get", map[string]any{"workflow_id": "wf-txt2img"})
	if err != nil {
		t.Fatalf("reloaded draft.get failed: %v", err)
	}
	reloaded := gotOne.(map[string]any)["draft"].(*ColabOneWorkflowDraft)
	if reloaded == nil {
		t.Fatal("expected reloaded draft to be non-nil")
	}
	if reloaded.Fields["6.text"] != "updated fox prompt" {
		t.Fatalf("unexpected reloaded prompt: %#v", reloaded.Fields["6.text"])
	}
	if reloaded.Fields["3.seed"] != int64(1085739284759283741) {
		t.Fatalf("reloaded 64-bit seed mismatch: %#v (%T)", reloaded.Fields["3.seed"], reloaded.Fields["3.seed"])
	}
	if len(reloaded.Media) != 2 || reloaded.Media["10.image"].ID != imgMeta.ID || reloaded.Media["12.audio"].ID != audioMeta.ID {
		t.Fatalf("reloaded media mismatch: %+v", reloaded.Media)
	}

	// Verify get-all returns the workflow draft.
	gotAll, err := ctrl2.Command(ctx, "comfyui.draft.get", map[string]any{})
	if err != nil {
		t.Fatalf("reloaded draft.get(all) failed: %v", err)
	}
	allJSON := mustJSONRoundTripMap(t, gotAll)
	allDrafts := asMap(allJSON["drafts"])
	if len(allDrafts) != 1 || asMap(allDrafts["wf-txt2img"]) == nil {
		t.Fatalf("unexpected reloaded all drafts: %#v", allJSON)
	}

	// Verify image & audio media.read roundtrip after restart.
	readImg, err := ctrl2.Command(ctx, "comfyui.draft.media.read", map[string]any{
		"workflow_id": "wf-txt2img",
		"field_key":   "10.image",
	})
	if err != nil {
		t.Fatalf("media.read (image) failed: %v", err)
	}
	readImgMap := readImg.(map[string]any)
	if readImgMap["id"] != imgMeta.ID || readImgMap["filename"] != "hero.PNG" || readImgMap["kind"] != "image" ||
		readImgMap["mime"] != "image/png" || readImgMap["size"] != int64(len(imgBytes)) ||
		readImgMap["sha256"] != imgSHAHex || readImgMap["data"] != imgB64 {
		t.Fatalf("image media.read mismatch: %#v", readImgMap)
	}

	readAudio, err := ctrl2.Command(ctx, "comfyui.draft.media.read", map[string]any{
		"workflow_id": "wf-txt2img",
		"field_key":   "12.audio",
	})
	if err != nil {
		t.Fatalf("media.read (audio) failed: %v", err)
	}
	readAudioMap := readAudio.(map[string]any)
	if readAudioMap["id"] != audioMeta.ID || readAudioMap["filename"] != "voice_clip.wav" || readAudioMap["kind"] != "audio" ||
		readAudioMap["mime"] != "audio/wav" || readAudioMap["size"] != int64(len(audioBytes)) ||
		readAudioMap["sha256"] != audioSHAHex || readAudioMap["data"] != audioB64 {
		t.Fatalf("audio media.read mismatch: %#v", readAudioMap)
	}
}

func TestWorkflowDraftMediaReplaceDeleteAndClearIsolation(t *testing.T) {
	ctx := context.Background()
	cfgDir := t.TempDir()
	ctrl := newDraftTestController(t, cfgDir, realComfyTestAdapter{})
	mediaDir := filepath.Join(cfgDir, "comfyui-drafts", "media")

	// Save initial image on wf-a and wf-b.
	b1 := base64.StdEncoding.EncodeToString([]byte("image-one"))
	b2 := base64.StdEncoding.EncodeToString([]byte("image-two-replacement"))
	bAudio := base64.StdEncoding.EncodeToString([]byte("audio-keep"))
	bOther := base64.StdEncoding.EncodeToString([]byte("other-workflow-image"))

	if _, err := ctrl.Command(ctx, "comfyui.draft.save", map[string]any{
		"workflow_id": "wf-a",
		"fields":      map[string]any{"6.text": "keep my text"},
	}); err != nil {
		t.Fatalf("save wf-a text failed: %v", err)
	}

	res1, err := ctrl.Command(ctx, "comfyui.draft.media.save", map[string]any{
		"workflow_id": "wf-a",
		"field_key":   "10.image",
		"filename":    "first.jpg",
		"kind":        "image",
		"data":        b1,
	})
	if err != nil {
		t.Fatalf("save first image failed: %v", err)
	}
	firstMeta := res1.(map[string]any)["media"].(ColabOneWorkflowDraftMedia)
	firstPath := filepath.Join(mediaDir, firstMeta.ID+".jpg")
	if _, err := os.Stat(firstPath); err != nil {
		t.Fatalf("first image not written: %v", err)
	}

	// Replace 10.image with a webp image -> old .jpg file must be deleted!
	res2, err := ctrl.Command(ctx, "comfyui.draft.media.save", map[string]any{
		"workflow_id": "wf-a",
		"field_key":   "10.image",
		"filename":    "second.webp",
		"kind":        "image",
		"data":        b2,
	})
	if err != nil {
		t.Fatalf("replace image failed: %v", err)
	}
	secondMeta := res2.(map[string]any)["media"].(ColabOneWorkflowDraftMedia)
	if secondMeta.ID == firstMeta.ID {
		t.Fatalf("expected fresh ID on replacement, got same %s", secondMeta.ID)
	}
	if _, err := os.Stat(firstPath); !os.IsNotExist(err) {
		t.Fatalf("expected old replaced media file %s to be deleted, stat err=%v", firstPath, err)
	}
	secondPath := filepath.Join(mediaDir, secondMeta.ID+".webp")
	if _, err := os.Stat(secondPath); err != nil {
		t.Fatalf("replacement media file missing: %v", err)
	}
	// Text field saved earlier must still exist, and field_key updated to cleanFilename.
	draftAfterReplace := res2.(map[string]any)["draft"].(*ColabOneWorkflowDraft)
	if draftAfterReplace.Fields["6.text"] != "keep my text" || draftAfterReplace.Fields["10.image"] != "second.webp" {
		t.Fatalf("unexpected fields after media replace: %#v", draftAfterReplace.Fields)
	}

	// Add an audio file to wf-a and an image to wf-b.
	resAudio, err := ctrl.Command(ctx, "comfyui.draft.media.save", map[string]any{
		"workflow_id": "wf-a",
		"field_key":   "12.audio",
		"filename":    "clip.mp3",
		"kind":        "audio",
		"data":        bAudio,
	})
	if err != nil {
		t.Fatalf("save wf-a audio failed: %v", err)
	}
	audioMeta := resAudio.(map[string]any)["media"].(ColabOneWorkflowDraftMedia)
	audioPath := filepath.Join(mediaDir, audioMeta.ID+".mp3")

	resOther, err := ctrl.Command(ctx, "comfyui.draft.media.save", map[string]any{
		"workflow_id": "wf-b",
		"field_key":   "10.image",
		"filename":    "other.png",
		"kind":        "image",
		"data":        bOther,
	})
	if err != nil {
		t.Fatalf("save wf-b image failed: %v", err)
	}
	otherMeta := resOther.(map[string]any)["media"].(ColabOneWorkflowDraftMedia)
	otherPath := filepath.Join(mediaDir, otherMeta.ID+".png")

	// Delete only 10.image from wf-a -> removes 10.image file & field, leaves 12.audio, 6.text, and wf-b intact.
	if _, err := ctrl.Command(ctx, "comfyui.draft.media.delete", map[string]any{
		"workflow_id": "wf-a",
		"field_key":   "10.image",
	}); err != nil {
		t.Fatalf("media.delete failed: %v", err)
	}
	if _, err := os.Stat(secondPath); !os.IsNotExist(err) {
		t.Fatalf("expected deleted media file %s removed, err=%v", secondPath, err)
	}
	if _, err := os.Stat(audioPath); err != nil {
		t.Fatalf("expected unrelated audio file %s kept, err=%v", audioPath, err)
	}
	gotA, err := ctrl.Command(ctx, "comfyui.draft.get", map[string]any{"workflow_id": "wf-a"})
	if err != nil {
		t.Fatalf("draft.get(wf-a) failed: %v", err)
	}
	draftA := gotA.(map[string]any)["draft"].(*ColabOneWorkflowDraft)
	if _, exists := draftA.Media["10.image"]; exists {
		t.Fatalf("expected 10.image removed from Media: %+v", draftA.Media)
	}
	if _, exists := draftA.Fields["10.image"]; exists {
		t.Fatalf("expected 10.image removed from Fields: %+v", draftA.Fields)
	}
	if draftA.Fields["6.text"] != "keep my text" || draftA.Media["12.audio"].ID != audioMeta.ID {
		t.Fatalf("expected 6.text and 12.audio preserved: %+v", draftA)
	}
	if _, err := ctrl.Command(ctx, "comfyui.draft.media.read", map[string]any{
		"workflow_id": "wf-a",
		"field_key":   "10.image",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after media.delete, got %v", err)
	}

	// Clear wf-a -> removes wf-a draft and its audio file, while keeping wf-b intact!
	if _, err := ctrl.Command(ctx, "comfyui.draft.clear", map[string]any{"workflow_id": "wf-a"}); err != nil {
		t.Fatalf("draft.clear(wf-a) failed: %v", err)
	}
	if _, err := os.Stat(audioPath); !os.IsNotExist(err) {
		t.Fatalf("expected wf-a audio file deleted on clear, err=%v", err)
	}
	gotACleared, err := ctrl.Command(ctx, "comfyui.draft.get", map[string]any{"workflow_id": "wf-a"})
	if err != nil {
		t.Fatalf("draft.get(wf-a) after clear failed: %v", err)
	}
	if gotACleared.(map[string]any)["draft"] != nil {
		t.Fatalf("expected nil draft after clear, got %#v", gotACleared)
	}
	if _, err := os.Stat(otherPath); err != nil {
		t.Fatalf("expected wf-b media file preserved after clearing wf-a, err=%v", err)
	}
	gotB, err := ctrl.Command(ctx, "comfyui.draft.get", map[string]any{"workflow_id": "wf-b"})
	if err != nil {
		t.Fatalf("draft.get(wf-b) failed: %v", err)
	}
	if draftB := gotB.(map[string]any)["draft"].(*ColabOneWorkflowDraft); draftB == nil || draftB.Media["10.image"].ID != otherMeta.ID {
		t.Fatalf("expected wf-b draft intact, got %#v", gotB)
	}
}

func TestWorkflowDeleteCleansUpDraftAndMedia(t *testing.T) {
	ctx := context.Background()
	cfgDir := t.TempDir()
	ctrl := newDraftTestController(t, cfgDir, mockComfy{})

	// Import an offline custom workflow.
	imported, err := ctrl.Command(ctx, "comfyui.workflow.import", map[string]any{
		"id":   "wf-custom-cleanup",
		"name": "Custom Cleanup Workflow",
		"workflow": map[string]any{
			"1": map[string]any{"class_type": "CLIPTextEncode", "inputs": map[string]any{"text": "hi"}},
		},
	})
	if err != nil {
		t.Fatalf("workflow.import failed: %v", err)
	}
	wfID := asString(asMap(imported)["id"])

	// Save draft fields + media for that custom workflow and for another workflow.
	if _, err := ctrl.Command(ctx, "comfyui.draft.save", map[string]any{
		"workflow_id": wfID,
		"fields":      map[string]any{"1.text": "custom draft text"},
	}); err != nil {
		t.Fatalf("draft.save failed: %v", err)
	}
	mRes, err := ctrl.Command(ctx, "comfyui.draft.media.save", map[string]any{
		"workflow_id": wfID,
		"field_key":   "2.image",
		"filename":    "pic.png",
		"kind":        "image",
		"data":        base64.StdEncoding.EncodeToString([]byte("png-data")),
	})
	if err != nil {
		t.Fatalf("draft.media.save failed: %v", err)
	}
	meta := mRes.(map[string]any)["media"].(ColabOneWorkflowDraftMedia)
	mediaPath := filepath.Join(cfgDir, "comfyui-drafts", "media", meta.ID+".png")

	if _, err := ctrl.Command(ctx, "comfyui.draft.save", map[string]any{
		"workflow_id": "wf-other-stay",
		"fields":      map[string]any{"1.text": "must survive"},
	}); err != nil {
		t.Fatalf("draft.save other failed: %v", err)
	}

	// Delete the custom workflow -> its draft and media must be cleaned up!
	if _, err := ctrl.Command(ctx, "comfyui.workflow.delete", map[string]any{"id": wfID}); err != nil {
		t.Fatalf("comfyui.workflow.delete failed: %v", err)
	}
	if _, err := os.Stat(mediaPath); !os.IsNotExist(err) {
		t.Fatalf("expected media file %s removed after workflow.delete, err=%v", mediaPath, err)
	}
	gotDeleted, err := ctrl.Command(ctx, "comfyui.draft.get", map[string]any{"workflow_id": wfID})
	if err != nil {
		t.Fatalf("draft.get deleted wf failed: %v", err)
	}
	if gotDeleted.(map[string]any)["draft"] != nil {
		t.Fatalf("expected draft cleared after workflow.delete, got %#v", gotDeleted)
	}
	gotOther, err := ctrl.Command(ctx, "comfyui.draft.get", map[string]any{"workflow_id": "wf-other-stay"})
	if err != nil {
		t.Fatalf("draft.get other wf failed: %v", err)
	}
	if gotOther.(map[string]any)["draft"] == nil {
		t.Fatal("expected unrelated workflow draft to survive workflow.delete")
	}
}

func TestWorkflowDraftValidationRejections(t *testing.T) {
	ctx := context.Background()
	cfgDir := t.TempDir()
	ctrl := newDraftTestController(t, cfgDir, realComfyTestAdapter{})
	validB64 := base64.StdEncoding.EncodeToString([]byte("hello-media"))

	// Invalid workflow IDs across actions.
	for _, badID := range []any{"../etc", "bad id", "wf/1", strings.Repeat("a", 81), 123} {
		if _, err := ctrl.Command(ctx, "comfyui.draft.get", map[string]any{"workflow_id": badID}); err == nil {
			t.Errorf("draft.get accepted invalid workflow_id %#v", badID)
		}
		if _, err := ctrl.Command(ctx, "comfyui.draft.save", map[string]any{"workflow_id": badID, "fields": map[string]any{}}); err == nil {
			t.Errorf("draft.save accepted invalid workflow_id %#v", badID)
		}
		if _, err := ctrl.Command(ctx, "comfyui.draft.clear", map[string]any{"workflow_id": badID}); err == nil {
			t.Errorf("draft.clear accepted invalid workflow_id %#v", badID)
		}
	}
	if _, err := ctrl.Command(ctx, "comfyui.draft.save", map[string]any{"workflow_id": "", "fields": map[string]any{}}); err == nil {
		t.Error("draft.save accepted empty workflow_id")
	}
	if _, err := ctrl.Command(ctx, "comfyui.draft.clear", map[string]any{"workflow_id": ""}); err == nil {
		t.Error("draft.clear accepted empty workflow_id")
	}

	// Invalid fields payload types / limits / values.
	if _, err := ctrl.Command(ctx, "comfyui.draft.save", map[string]any{
		"workflow_id": "wf-1",
		"fields":      []any{"not-a-map"},
	}); err == nil {
		t.Error("draft.save accepted slice fields")
	}
	tooManyFields := make(map[string]any, 129)
	for i := 0; i < 129; i++ {
		tooManyFields[fmt.Sprintf("k_%d", i)] = i
	}
	if _, err := ctrl.Command(ctx, "comfyui.draft.save", map[string]any{
		"workflow_id": "wf-1",
		"fields":      tooManyFields,
	}); err == nil {
		t.Error("draft.save accepted 129 fields")
	}
	for _, badKey := range []string{"", "bad key", "bad/key", "bad$key", strings.Repeat("k", 161)} {
		if _, err := ctrl.Command(ctx, "comfyui.draft.save", map[string]any{
			"workflow_id": "wf-1",
			"fields":      map[string]any{badKey: "v"},
		}); err == nil {
			t.Errorf("draft.save accepted invalid field key %q", badKey)
		}
		if _, err := ctrl.Command(ctx, "comfyui.draft.media.save", map[string]any{
			"workflow_id": "wf-1",
			"field_key":   badKey,
			"filename":    "a.png",
			"kind":        "image",
			"data":        validB64,
		}); err == nil {
			t.Errorf("media.save accepted invalid field_key %q", badKey)
		}
	}
	for _, tc := range []struct {
		name string
		val  any
	}{
		{"tooLongStringASCII", strings.Repeat("a", 200001)},
		{"tooLongStringCJK", strings.Repeat("汉", 200001)},
		{"nan", math.NaN()},
		{"posInf", math.Inf(1)},
		{"negInf", math.Inf(-1)},
		{"nestedMap", map[string]any{"a": 1}},
		{"slice", []any{1, 2}},
		{"badJSONNumber", json.Number("not-a-number")},
	} {
		if _, err := ctrl.Command(ctx, "comfyui.draft.save", map[string]any{
			"workflow_id": "wf-1",
			"fields":      map[string]any{"1.val": tc.val},
		}); err == nil {
			t.Errorf("draft.save accepted invalid field value %s", tc.name)
		} else if strings.HasPrefix(tc.name, "tooLongString") && err.Error() != "草稿文本字段超过 200000 个字符上限" {
			t.Errorf("%s: got error %q, want %q", tc.name, err.Error(), "草稿文本字段超过 200000 个字符上限")
		}
	}

	// Rune-based string cap: >66,667 CJK characters (>200,000 UTF-8 bytes) up to 200,000 runes must be accepted
	// consistently across both comfyui.draft.save and migration draft validation.
	cjk70k := strings.Repeat("汉", 70000) // 210,000 UTF-8 bytes, 70,000 runes
	if _, err := ctrl.Command(ctx, "comfyui.draft.save", map[string]any{
		"workflow_id": "wf-cjk-runes",
		"fields":      map[string]any{"pipeline.script_text": cjk70k},
	}); err != nil {
		t.Fatalf("draft.save rejected 70,000-rune CJK string (210,000 bytes): %v", err)
	}
	cjk200k := strings.Repeat("字", 200000) // 600,000 UTF-8 bytes, exact 200,000-rune boundary
	if _, err := ctrl.Command(ctx, "comfyui.draft.save", map[string]any{
		"workflow_id": "wf-cjk-boundary",
		"fields":      map[string]any{"pipeline.script_text": cjk200k},
	}); err != nil {
		t.Fatalf("draft.save rejected 200,000-rune CJK boundary string: %v", err)
	}
	if validated, err := validateMigrationWorkflowDrafts(map[string]any{
		"version": 1,
		"drafts": map[string]any{
			"wf-cjk-runes": map[string]any{
				"fields": map[string]any{"pipeline.script_text": cjk70k},
			},
		},
	}); err != nil || validated["wf-cjk-runes"] == nil {
		t.Fatalf("validateMigrationWorkflowDrafts rejected 70,000-rune CJK string: err=%v validated=%#v", err, validated)
	}

	// Mismatched kind/extension & unsupported extensions & unsafe filenames.
	for _, tc := range []struct {
		kind     string
		filename string
	}{
		{"image", "clip.wav"},
		{"image", "clip.mp3"},
		{"audio", "photo.png"},
		{"audio", "photo.webp"},
		{"video", "movie.mp4"},
		{"image", "script.exe"},
		{"image", "noext"},
		{"image", ".png"},
		{"image", ".."},
		{"image", "dir/"},
		{"image", "dir/.."},
		{"image", "   "},
	} {
		if _, err := ctrl.Command(ctx, "comfyui.draft.media.save", map[string]any{
			"workflow_id": "wf-1",
			"field_key":   "10.media",
			"filename":    tc.filename,
			"kind":        tc.kind,
			"data":        validB64,
		}); err == nil {
			t.Errorf("media.save accepted mismatched/invalid kind=%q filename=%q", tc.kind, tc.filename)
		}
	}

	// Invalid / empty / oversized base64.
	for _, tc := range []struct {
		name     string
		kind     string
		filename string
		data     string
	}{
		{"empty", "image", "a.png", ""},
		{"whitespaceOnly", "image", "a.png", "   \n\t "},
		{"emptyDataURI", "image", "a.png", "data:image/png;base64,"},
		{"malformedDataURI", "image", "a.png", "data:image/png;base64"},
		{"badMod4", "image", "a.png", "abc"},
		{"illegalChars", "image", "a.png", "!!!!"},
		{"earlyPadding", "image", "a.png", strings.Repeat("AAAA", 1023) + "AA==" + "AAAA"},
		// 16 MiB + 1 byte image: 16 MiB = 16777216 = 3 * 5592405 + 1 -> +1 byte = 5592405*"AAAA" + "AAA="
		{"over16MiBImage", "image", "big.png", strings.Repeat("AAAA", colabOneMaxWorkflowDraftImageBytes/3) + "AAA="},
		// Fast encoded length cap for 64 MiB audio
		{"hugeAudioEncoded", "audio", "huge.wav", strings.Repeat("A", colabOneMaxWorkflowDraftAudioBytes+colabOneMaxWorkflowDraftAudioBytes/2+8192)},
	} {
		if _, err := ctrl.Command(ctx, "comfyui.draft.media.save", map[string]any{
			"workflow_id": "wf-1",
			"field_key":   "10.media",
			"filename":    tc.filename,
			"kind":        tc.kind,
			"data":        tc.data,
		}); err == nil {
			t.Errorf("media.save accepted invalid/oversized payload %s", tc.name)
		}
	}

	// Long >180-rune filename is cleanly truncated while preserving extension.
	longName := strings.Repeat("画", 200) + ".webp"
	longRes, err := ctrl.Command(ctx, "comfyui.draft.media.save", map[string]any{
		"workflow_id": "wf-1",
		"field_key":   "10.image",
		"filename":    longName,
		"kind":        "image",
		"data":        validB64,
	})
	if err != nil {
		t.Fatalf("expected long filename to be cleanly truncated, got err: %v", err)
	}
	longMeta := longRes.(map[string]any)["media"].(ColabOneWorkflowDraftMedia)
	if !strings.HasSuffix(longMeta.Filename, ".webp") || utf8.RuneCountInString(longMeta.Filename) > 180 {
		t.Fatalf("unexpected truncated filename: %q (runes=%d)", longMeta.Filename, utf8.RuneCountInString(longMeta.Filename))
	}
}

func TestWorkflowDraftCorruptionMissingFileAndSymlinkHardening(t *testing.T) {
	ctx := context.Background()
	cfgDir := t.TempDir()
	ctrl := newDraftTestController(t, cfgDir, realComfyTestAdapter{})
	mediaDir := filepath.Join(cfgDir, "comfyui-drafts", "media")

	// Save an image media file.
	origBytes := []byte("original-valid-image-bytes")
	saveRes, err := ctrl.Command(ctx, "comfyui.draft.media.save", map[string]any{
		"workflow_id": "wf-sec",
		"field_key":   "10.image",
		"filename":    "safe.png",
		"kind":        "image",
		"data":        base64.StdEncoding.EncodeToString(origBytes),
	})
	if err != nil {
		t.Fatalf("media.save failed: %v", err)
	}
	meta := saveRes.(map[string]any)["media"].(ColabOneWorkflowDraftMedia)
	mediaPath := filepath.Join(mediaDir, meta.ID+".png")

	// 1. Corrupted hash (same length, different byte) -> media.read must fail.
	tampered := append([]byte(nil), origBytes...)
	tampered[0] ^= 0xff
	if err := os.WriteFile(mediaPath, tampered, 0o600); err != nil {
		t.Fatalf("write tampered file failed: %v", err)
	}
	if _, err := ctrl.Command(ctx, "comfyui.draft.media.read", map[string]any{
		"workflow_id": "wf-sec",
		"field_key":   "10.image",
	}); err == nil || !strings.Contains(err.Error(), "哈希") {
		t.Fatalf("expected hash mismatch error, got %v", err)
	}

	// 2. Corrupted size -> media.read must fail, and draft.get reconciles truncated media + ghost field.
	if err := os.WriteFile(mediaPath, []byte("short"), 0o600); err != nil {
		t.Fatalf("write short file failed: %v", err)
	}
	if _, err := ctrl.Command(ctx, "comfyui.draft.media.read", map[string]any{
		"workflow_id": "wf-sec",
		"field_key":   "10.image",
	}); err == nil || !strings.Contains(err.Error(), "大小") {
		t.Fatalf("expected size mismatch error, got %v", err)
	}

	saveTruncRes, err := ctrl.Command(ctx, "comfyui.draft.media.save", map[string]any{
		"workflow_id": "wf-trunc",
		"field_key":   "10.image",
		"filename":    "truncated.png",
		"kind":        "image",
		"data":        base64.StdEncoding.EncodeToString(origBytes),
	})
	if err != nil {
		t.Fatalf("media.save wf-trunc failed: %v", err)
	}
	truncMeta := saveTruncRes.(map[string]any)["media"].(ColabOneWorkflowDraftMedia)
	if err := os.WriteFile(filepath.Join(mediaDir, truncMeta.ID+".png"), []byte("x"), 0o600); err != nil {
		t.Fatalf("truncate media file failed: %v", err)
	}
	gotTrunc, err := ctrl.Command(ctx, "comfyui.draft.get", map[string]any{"workflow_id": "wf-trunc"})
	if err != nil {
		t.Fatalf("draft.get on truncated media failed: %v", err)
	}
	truncDraft := gotTrunc.(map[string]any)["draft"].(*ColabOneWorkflowDraft)
	if len(truncDraft.Media) != 0 {
		t.Fatalf("expected truncated media pruned on draft.get, got %+v", truncDraft.Media)
	}
	if _, hasGhostField := truncDraft.Fields["10.image"]; hasGhostField {
		t.Fatalf("expected ghost field pruned for truncated media, got %+v", truncDraft.Fields)
	}

	// 3. Symlink file attack (Unix/when symlinks allowed) -> media.read rejects, draft.get omits cleanly!
	if runtime.GOOS != "windows" {
		secretFile := filepath.Join(t.TempDir(), "secret.txt")
		if err := os.WriteFile(secretFile, origBytes, 0o600); err != nil {
			t.Fatalf("write secret file failed: %v", err)
		}
		if err := os.Remove(mediaPath); err != nil {
			t.Fatalf("remove media file failed: %v", err)
		}
		if err := os.Symlink(secretFile, mediaPath); err != nil {
			t.Fatalf("symlink failed: %v", err)
		}
		if _, err := ctrl.Command(ctx, "comfyui.draft.media.read", map[string]any{
			"workflow_id": "wf-sec",
			"field_key":   "10.image",
		}); err == nil || !strings.Contains(err.Error(), "不安全") {
			t.Fatalf("expected symlink security error on media.read, got %v", err)
		}
		gotDraft, err := ctrl.Command(ctx, "comfyui.draft.get", map[string]any{"workflow_id": "wf-sec"})
		if err != nil {
			t.Fatalf("draft.get failed on symlink media: %v", err)
		}
		d := gotDraft.(map[string]any)["draft"].(*ColabOneWorkflowDraft)
		if len(d.Media) != 0 {
			t.Fatalf("expected symlinked media omitted from draft.get, got %+v", d.Media)
		}
		if _, hasGhostField := d.Fields["10.image"]; hasGhostField {
			t.Fatalf("expected ghost field filename pruned for symlinked media, got %+v", d.Fields)
		}
	}

	// 4. Missing file reconciliation in draft.get and across restart (no ghost filename left in Fields).
	saveRes2, err := ctrl.Command(ctx, "comfyui.draft.media.save", map[string]any{
		"workflow_id": "wf-missing",
		"field_key":   "10.image",
		"filename":    "vanish.png",
		"kind":        "image",
		"data":        base64.StdEncoding.EncodeToString(origBytes),
	})
	if err != nil {
		t.Fatalf("media.save wf-missing failed: %v", err)
	}
	meta2 := saveRes2.(map[string]any)["media"].(ColabOneWorkflowDraftMedia)
	if err := os.Remove(filepath.Join(mediaDir, meta2.ID+".png")); err != nil {
		t.Fatalf("remove vanish.png failed: %v", err)
	}
	if _, err := ctrl.Command(ctx, "comfyui.draft.media.read", map[string]any{
		"workflow_id": "wf-missing",
		"field_key":   "10.image",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for deleted backing file, got %v", err)
	}
	gotMissing, err := ctrl.Command(ctx, "comfyui.draft.get", map[string]any{"workflow_id": "wf-missing"})
	if err != nil {
		t.Fatalf("draft.get on missing backing file failed: %v", err)
	}
	missingDraft := gotMissing.(map[string]any)["draft"].(*ColabOneWorkflowDraft)
	if len(missingDraft.Media) != 0 {
		t.Fatalf("expected missing media omitted cleanly: %+v", gotMissing)
	}
	if _, hasGhostField := missingDraft.Fields["10.image"]; hasGhostField {
		t.Fatalf("expected ghost field filename removed on missing media reconciliation: %+v", missingDraft.Fields)
	}

	// Startup reconciliation also clears ghost field filenames when a media file disappeared before restart.
	saveRes3, err := ctrl.Command(ctx, "comfyui.draft.media.save", map[string]any{
		"workflow_id": "wf-missing-startup",
		"field_key":   "10.image",
		"filename":    "vanish-restart.png",
		"kind":        "image",
		"data":        base64.StdEncoding.EncodeToString(origBytes),
	})
	if err != nil {
		t.Fatalf("media.save wf-missing-startup failed: %v", err)
	}
	meta3 := saveRes3.(map[string]any)["media"].(ColabOneWorkflowDraftMedia)
	if err := os.Remove(filepath.Join(mediaDir, meta3.ID+".png")); err != nil {
		t.Fatalf("remove vanish-restart.png failed: %v", err)
	}
	ctrlRestarted := newDraftTestController(t, cfgDir, realComfyTestAdapter{})
	gotRestarted, err := ctrlRestarted.Command(ctx, "comfyui.draft.get", map[string]any{"workflow_id": "wf-missing-startup"})
	if err != nil {
		t.Fatalf("draft.get after restart failed: %v", err)
	}
	restartedDraft := gotRestarted.(map[string]any)["draft"].(*ColabOneWorkflowDraft)
	if len(restartedDraft.Media) != 0 {
		t.Fatalf("expected missing media pruned at startup, got %+v", restartedDraft.Media)
	}
	if _, hasGhostField := restartedDraft.Fields["10.image"]; hasGhostField {
		t.Fatalf("expected ghost field filename pruned at startup, got %+v", restartedDraft.Fields)
	}

	// 5. Symlink directory attacks (comfyui-drafts/media and comfyui-drafts itself).
	if runtime.GOOS != "windows" {
		symDirCfg := t.TempDir()
		outsideDir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(symDirCfg, "comfyui-drafts"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outsideDir, filepath.Join(symDirCfg, "comfyui-drafts", "media")); err != nil {
			t.Fatal(err)
		}
		symCtrl := newDraftTestController(t, symDirCfg, realComfyTestAdapter{})
		if _, err := symCtrl.Command(ctx, "comfyui.draft.media.save", map[string]any{
			"workflow_id": "wf-sym",
			"field_key":   "10.image",
			"filename":    "a.png",
			"kind":        "image",
			"data":        base64.StdEncoding.EncodeToString(origBytes),
		}); err == nil {
			t.Fatal("expected media.save to reject symlinked media directory")
		}

		symRootCfg := t.TempDir()
		if err := os.Symlink(outsideDir, filepath.Join(symRootCfg, "comfyui-drafts")); err != nil {
			t.Fatal(err)
		}
		symRootCtrl := newDraftTestController(t, symRootCfg, realComfyTestAdapter{})
		if _, err := symRootCtrl.Command(ctx, "comfyui.draft.save", map[string]any{
			"workflow_id": "wf-sym",
			"fields":      map[string]any{"1.text": "blocked"},
		}); err == nil {
			t.Fatal("expected draft.save to reject symlinked comfyui-drafts directory")
		}
		if _, err := symRootCtrl.Command(ctx, "comfyui.draft.media.save", map[string]any{
			"workflow_id": "wf-sym",
			"field_key":   "10.image",
			"filename":    "a.png",
			"kind":        "image",
			"data":        base64.StdEncoding.EncodeToString(origBytes),
		}); err == nil {
			t.Fatal("expected media.save to reject symlinked comfyui-drafts directory")
		}
	}
}

func TestWorkflowDraftStartupOrphanGCAndCorruptResilience(t *testing.T) {
	ctx := context.Background()

	// 1. Orphan media file GC on startup (while keeping referenced files & .save-* temp files).
	cfgDir := t.TempDir()
	ctrl1 := newDraftTestController(t, cfgDir, realComfyTestAdapter{})
	saveRes, err := ctrl1.Command(ctx, "comfyui.draft.media.save", map[string]any{
		"workflow_id": "wf-gc",
		"field_key":   "10.image",
		"filename":    "keep.png",
		"kind":        "image",
		"data":        base64.StdEncoding.EncodeToString([]byte("keep-bytes")),
	})
	if err != nil {
		t.Fatalf("media.save failed: %v", err)
	}
	keepMeta := saveRes.(map[string]any)["media"].(ColabOneWorkflowDraftMedia)
	ctrl1.Close()

	mediaDir := filepath.Join(cfgDir, "comfyui-drafts", "media")
	keepPath := filepath.Join(mediaDir, keepMeta.ID+".png")
	orphanPath := filepath.Join(mediaDir, "deadbeefdeadbeefdeadbeefdeadbeef.png")
	tempPath := filepath.Join(mediaDir, ".save-in-flight.tmp")
	if err := os.WriteFile(orphanPath, []byte("orphan"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tempPath, []byte("temp"), 0o600); err != nil {
		t.Fatal(err)
	}

	ctrl2 := newDraftTestController(t, cfgDir, realComfyTestAdapter{})
	if _, err := os.Stat(orphanPath); !os.IsNotExist(err) {
		t.Fatalf("expected orphan media file pruned on startup, stat err=%v", err)
	}
	if _, err := os.Stat(keepPath); err != nil {
		t.Fatalf("expected referenced media file kept on startup, stat err=%v", err)
	}
	if _, err := os.Stat(tempPath); err != nil {
		t.Fatalf("expected .save-* temp file skipped during startup GC, stat err=%v", err)
	}
	got, err := ctrl2.Command(ctx, "comfyui.draft.get", map[string]any{"workflow_id": "wf-gc"})
	if err != nil || got.(map[string]any)["draft"] == nil {
		t.Fatalf("expected wf-gc draft intact after startup GC, got=%#v err=%v", got, err)
	}

	// 2. Corrupt drafts.json does not crash startup and does not destroy existing media files.
	corruptCfg := t.TempDir()
	corruptMediaDir := filepath.Join(corruptCfg, "comfyui-drafts", "media")
	if err := os.MkdirAll(corruptMediaDir, 0o700); err != nil {
		t.Fatal(err)
	}
	corruptDraftsPath := filepath.Join(corruptCfg, "comfyui-drafts", "drafts.json")
	if err := os.WriteFile(corruptDraftsPath, []byte("{corrupt-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	preservedMedia := filepath.Join(corruptMediaDir, "0123456789abcdef0123456789abcdef.png")
	if err := os.WriteFile(preservedMedia, []byte("preserve-me"), 0o600); err != nil {
		t.Fatal(err)
	}

	corruptCtrl := newDraftTestController(t, corruptCfg, realComfyTestAdapter{})
	allRes, err := corruptCtrl.Command(ctx, "comfyui.draft.get", map[string]any{})
	if err != nil {
		t.Fatalf("draft.get after corrupt startup failed: %v", err)
	}
	if len(asMap(mustJSONRoundTripMap(t, allRes)["drafts"])) != 0 {
		t.Fatalf("expected empty drafts after corrupt drafts.json, got %#v", allRes)
	}
	corruptBackupPath := corruptDraftsPath + ".corrupt"
	if raw, err := os.ReadFile(corruptBackupPath); err != nil || string(raw) != "{corrupt-json" {
		t.Fatalf("expected corrupt drafts.json backed up to drafts.json.corrupt, raw=%q err=%v", string(raw), err)
	}
	// A subsequent save must succeed and must NOT destroy drafts.json.corrupt.
	if _, err := corruptCtrl.Command(ctx, "comfyui.draft.save", map[string]any{
		"workflow_id": "wf-after-corrupt",
		"fields":      map[string]any{"1.text": "fresh"},
	}); err != nil {
		t.Fatalf("draft.save after corrupt backup failed: %v", err)
	}
	if raw, err := os.ReadFile(corruptBackupPath); err != nil || string(raw) != "{corrupt-json" {
		t.Fatalf("drafts.json.corrupt was clobbered after subsequent save: raw=%q err=%v", string(raw), err)
	}
	if _, err := os.Stat(preservedMedia); err != nil {
		t.Fatalf("expected media file preserved when drafts.json is corrupt, err=%v", err)
	}

	// 3. Oversized (>32 MiB) drafts.json is skipped cleanly without reading into memory.
	bigCfg := t.TempDir()
	bigDir := filepath.Join(bigCfg, "comfyui-drafts")
	if err := os.MkdirAll(bigDir, 0o700); err != nil {
		t.Fatal(err)
	}
	bigPath := filepath.Join(bigDir, "drafts.json")
	bigFile, err := os.OpenFile(bigPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := bigFile.Truncate(colabOneMaxWorkflowDraftsFileBytes + 1); err != nil {
		_ = bigFile.Close()
		t.Fatal(err)
	}
	_ = bigFile.Close()
	bigCtrl := newDraftTestController(t, bigCfg, realComfyTestAdapter{})
	bigRes, err := bigCtrl.Command(ctx, "comfyui.draft.get", map[string]any{})
	if err != nil {
		t.Fatalf("draft.get after oversized drafts.json failed: %v", err)
	}
	if len(asMap(mustJSONRoundTripMap(t, bigRes)["drafts"])) != 0 {
		t.Fatalf("expected empty drafts after oversized drafts.json, got %#v", bigRes)
	}
}

func TestWorkflowDraftConcurrentAccessAndLockOrdering(t *testing.T) {
	ctx := context.Background()
	cfgDir := t.TempDir()
	ctrl := newDraftTestController(t, cfgDir, realComfyTestAdapter{})
	b64 := base64.StdEncoding.EncodeToString([]byte("concurrent-media-payload"))

	var wg sync.WaitGroup
	errCh := make(chan error, 32)
	for g := 0; g < 6; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			wfID := fmt.Sprintf("wf-conc-%d", id%3)
			for i := 0; i < 6; i++ {
				if _, err := ctrl.Command(ctx, "comfyui.draft.save", map[string]any{
					"workflow_id": wfID,
					"fields":      map[string]any{"6.text": fmt.Sprintf("prompt-%d-%d", id, i)},
				}); err != nil {
					errCh <- err
					return
				}
				if _, err := ctrl.Command(ctx, "comfyui.draft.media.save", map[string]any{
					"workflow_id": wfID,
					"field_key":   "10.image",
					"filename":    fmt.Sprintf("img_%d_%d.png", id, i),
					"kind":        "image",
					"data":        b64,
				}); err != nil {
					errCh <- err
					return
				}
				if _, err := ctrl.Command(ctx, "comfyui.draft.get", map[string]any{"workflow_id": wfID}); err != nil {
					errCh <- err
					return
				}
				if _, err := ctrl.Command(ctx, "comfyui.draft.media.read", map[string]any{
					"workflow_id": wfID,
					"field_key":   "10.image",
				}); err != nil {
					errCh <- err
					return
				}
			}
		}(g)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent draft operation failed: %v", err)
	}
}
