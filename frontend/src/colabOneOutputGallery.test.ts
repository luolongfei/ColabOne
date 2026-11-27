import { describe, expect, it } from "vitest";
import {
  buildColabOneOutputShareGroupInputs,
  computeColabOneGroupCU,
  groupColabOneOutputs,
  isColabOneInspectOutput,
  resolveColabOneCoverItems,
  sanitizeColabOneShareGroupId,
  sortColabOneGalleryItems,
} from "./colabOneOutputGallery";
import type { WorkflowOutput } from "./types";

function makeOutput(overrides: Partial<WorkflowOutput> & { id: string }): WorkflowOutput {
  return {
    workflow_id: "fashion-tryon-h3",
    workflow_name: "Fashion Try-On Studio",
    kind: "image",
    filename: `${overrides.id}.png`,
    extension: "png",
    mime: "image/png",
    size: 1024,
    created_at: 1700000000,
    elapsed: 10,
    ...overrides,
  };
}

describe("isColabOneInspectOutput", () => {
  it("detects explicit is_inspect flag and inspect filename patterns case-insensitively", () => {
    expect(isColabOneInspectOutput({ filename: "rmbg_inspect_01.png" })).toBe(true);
    expect(isColabOneInspectOutput({ filename: "sub/rmbg_inspect_garment.png" })).toBe(true);
    expect(isColabOneInspectOutput({ filename: "C:\\tmp\\RMBG_INSPECT_2.png" })).toBe(true);
    expect(isColabOneInspectOutput({ filename: "shot_01_inspect_cutout.webp" })).toBe(true);
    expect(isColabOneInspectOutput({ filename: "LOOK_INSPECT_MASK.JPG" })).toBe(true);
    expect(isColabOneInspectOutput({ filename: "00_抠衣质检_top.png" })).toBe(true);
    expect(isColabOneInspectOutput({ filename: "sub/00_抠衣质检_coat_01.png" })).toBe(true);
    expect(isColabOneInspectOutput({ filename: "00_inspect_garment_02.png" })).toBe(true);
    expect(isColabOneInspectOutput({ filename: "look_01.png", is_inspect: true })).toBe(true);
    // Explicit server-side is_inspect: false overrides filename heuristic.
    expect(isColabOneInspectOutput({ filename: "00_抠衣质检_override.png", is_inspect: false })).toBe(false);
    expect(isColabOneInspectOutput({ filename: "rmbg_inspect_override.png", is_inspect: false })).toBe(false);

    expect(isColabOneInspectOutput({ filename: "look_01.png" })).toBe(false);
    expect(isColabOneInspectOutput({ filename: "inspector_preview.png" })).toBe(false);
    expect(isColabOneInspectOutput({ filename: "my_rmbg_output_01.png" })).toBe(false);
    expect(isColabOneInspectOutput({ filename: "inspect_01.png" })).toBe(false);
    expect(isColabOneInspectOutput(null)).toBe(false);
    expect(isColabOneInspectOutput(undefined)).toBe(false);
  });
});

describe("groupColabOneOutputs", () => {
  it("groups same-task multi-item outputs into albums with natural ordering and inspect items last", () => {
    const outputs: WorkflowOutput[] = [
      makeOutput({
        id: "o10",
        task_id: "task-fashion-1",
        filename: "look_10.png",
        size: 5000,
        created_at: 1700000100,
        elapsed: 12,
        prompt_id: "shot-10",
      }),
      makeOutput({
        id: "o_insp2",
        task_id: "task-fashion-1",
        filename: "rmbg_inspect_10.png",
        size: 800,
        created_at: 1700000090,
        elapsed: 2,
        prompt_id: "rmbg-2",
      }),
      makeOutput({
        id: "o2",
        task_id: "task-fashion-1",
        filename: "look_2.png",
        size: 4000,
        created_at: 1700000080,
        elapsed: 11,
        prompt_id: "shot-2",
      }),
      makeOutput({
        id: "o_insp1",
        task_id: "task-fashion-1",
        filename: "rmbg_inspect_2.png",
        size: 700,
        created_at: 1700000070,
        elapsed: 1.5,
        prompt_id: "rmbg-1",
      }),
      makeOutput({
        id: "o1",
        task_id: "task-fashion-1",
        filename: "look_1.png",
        size: 3000,
        created_at: 1700000060,
        elapsed: 9.5,
        prompt_id: "shot-1",
      }),
    ];

    const groups = groupColabOneOutputs(outputs);
    expect(groups).toHaveLength(1);

    const album = groups[0];
    expect(album.id).toBe("album:task-fashion-1");
    expect(album.taskId).toBe("task-fashion-1");
    expect(album.isAlbum).toBe(true);
    expect(album.title).toBe("Fashion Try-On Studio");
    expect(album.workflowId).toBe("fashion-tryon-h3");
    expect(album.workflowName).toBe("Fashion Try-On Studio");
    expect(album.items.map((item) => item.filename)).toEqual([
      "look_1.png",
      "look_2.png",
      "look_10.png",
      "rmbg_inspect_2.png",
      "rmbg_inspect_10.png",
    ]);
    expect(album.primaryCount).toBe(3);
    expect(album.inspectCount).toBe(2);
    // Cover prioritizes primary items only when primary items exist.
    expect(album.coverItems.map((item) => item.filename)).toEqual([
      "look_1.png",
      "look_2.png",
      "look_10.png",
    ]);
    expect(album.extraCount).toBe(2);
    expect(album.totalSize).toBe(13500);
    expect(album.totalElapsed).toBeCloseTo(36, 5);
    expect(album.createdAt).toBe(1700000100);
    expect(album.hasImage).toBe(true);
    expect(album.hasVideo).toBe(false);
    expect(album.hasAudio).toBe(false);
  });

  it("caps coverItems at 4, falls back to inspect items when primaryCount is 0, and computes extraCount accurately", () => {
    const sixPrimary = Array.from({ length: 6 }, (_, idx) =>
      makeOutput({
        id: `p${idx + 1}`,
        task_id: "task-six",
        filename: `shot_${idx + 1}.png`,
        created_at: 1700000500 + idx,
      }),
    );
    const onlyInspect = [
      makeOutput({
        id: "i1",
        task_id: "task-inspect-only",
        filename: "rmbg_inspect_1.png",
        created_at: 1700000200,
      }),
      makeOutput({
        id: "i2",
        task_id: "task-inspect-only",
        filename: "rmbg_inspect_2.png",
        created_at: 1700000201,
      }),
    ];

    const groups = groupColabOneOutputs([...onlyInspect, ...sixPrimary]);
    expect(groups).toHaveLength(2);
    // Sorted descending by createdAt
    expect(groups[0].id).toBe("album:task-six");
    expect(groups[0].coverItems).toHaveLength(4);
    expect(groups[0].extraCount).toBe(2);

    expect(groups[1].id).toBe("album:task-inspect-only");
    expect(groups[1].primaryCount).toBe(0);
    expect(groups[1].inspectCount).toBe(2);
    expect(groups[1].coverItems.map((i) => i.id)).toEqual(["i1", "i2"]);
    expect(groups[1].extraCount).toBe(0);
  });

  it("deduplicates elapsed time for shared prompt_id batch runs while summing distinct prompt_ids", () => {
    const batchOutputs = [
      makeOutput({
        id: "b1",
        task_id: "task-batch",
        prompt_id: "prompt-same",
        elapsed: 18.5,
        filename: "batch_1.png",
        created_at: 1700001000,
      }),
      makeOutput({
        id: "b2",
        task_id: "task-batch",
        prompt_id: "prompt-same",
        elapsed: 18.5,
        filename: "batch_2.png",
        created_at: 1700001001,
      }),
      makeOutput({
        id: "b3",
        task_id: "task-batch",
        prompt_id: "",
        elapsed: 3.5,
        filename: "batch_3.png",
        created_at: 1700001002,
      }),
    ];

    const groups = groupColabOneOutputs(batchOutputs);
    expect(groups).toHaveLength(1);
    expect(groups[0].totalElapsed).toBeCloseTo(22.0, 5);
  });

  it("keeps single-item tasks and empty-task outputs as non-album groups and tracks drive/media flags", () => {
    const outputs: WorkflowOutput[] = [
      makeOutput({
        id: "single-with-task",
        task_id: "task-solo",
        kind: "video",
        extension: "mp4",
        mime: "video/mp4",
        filename: "avatar.mp4",
        workflow_id: "dh-1",
        workflow_name: "Digital Human",
        created_at: 1700000300,
        drive_synced: true,
        drive_exported: true,
      }),
      makeOutput({
        id: "no-task-a",
        task_id: "",
        kind: "audio",
        extension: "wav",
        mime: "audio/wav",
        filename: "voice.wav",
        workflow_id: "tts-1",
        workflow_name: "Voice",
        created_at: 1700000400,
        storage: "runtime",
        drive_synced: false,
      }),
      makeOutput({
        id: "mixed-1",
        task_id: "task-mixed",
        kind: "image",
        filename: "frame_1.png",
        created_at: 1700000100,
        storage: "drive",
        drive_exported: true,
      }),
      makeOutput({
        id: "mixed-2",
        task_id: "task-mixed",
        kind: "video",
        extension: "mp4",
        mime: "video/mp4",
        filename: "clip_1.mp4",
        created_at: 1700000200,
        storage: "runtime",
        drive_synced: false,
        drive_exported: false,
      }),
    ];

    const groups = groupColabOneOutputs(outputs);
    expect(groups.map((g) => g.id)).toEqual([
      "item:no-task-a",
      "item:single-with-task",
      "album:task-mixed",
    ]);

    expect(groups[0].isAlbum).toBe(false);
    expect(groups[0].hasAudio).toBe(true);
    expect(groups[0].allDriveSynced).toBe(false);
    expect(groups[0].anyDriveSynced).toBe(false);

    expect(groups[1].isAlbum).toBe(false);
    expect(groups[1].taskId).toBe("task-solo");
    expect(groups[1].hasVideo).toBe(true);
    expect(groups[1].allDriveSynced).toBe(true);
    expect(groups[1].allExportedToDrive).toBe(true);

    expect(groups[2].isAlbum).toBe(true);
    expect(groups[2].hasImage).toBe(true);
    expect(groups[2].hasVideo).toBe(true);
    expect(groups[2].allDriveSynced).toBe(false);
    expect(groups[2].anyDriveSynced).toBe(true);
    expect(groups[2].allExportedToDrive).toBe(false);
  });
});

describe("buildColabOneOutputShareGroupInputs", () => {
  it("preserves group and natural item ordering while dropping empty or unknown selections", () => {
    const groups = groupColabOneOutputs([
      makeOutput({
        id: "a2",
        task_id: "t1",
        filename: "look_2.png",
        created_at: 200,
      }),
      makeOutput({
        id: "a1",
        task_id: "t1",
        filename: "look_1.png",
        created_at: 190,
      }),
      makeOutput({
        id: "a_insp",
        task_id: "t1",
        filename: "rmbg_inspect_1.png",
        created_at: 180,
      }),
      makeOutput({
        id: "solo",
        task_id: "",
        workflow_id: "wf-video",
        workflow_name: "Avatar Video",
        kind: "video",
        extension: "mp4",
        mime: "video/mp4",
        filename: "demo.mp4",
        created_at: 300,
      }),
    ]);

    const inputs = buildColabOneOutputShareGroupInputs(groups, {
      "item:solo": ["solo", "unknown-id"],
      "album:t1": ["a_insp", "a2", "a1", "a2"],
      "album:nonexistent": ["x1"],
    });

    expect(inputs).toEqual([
      {
        id: "item:solo",
        title: "Avatar Video",
        workflow_id: "wf-video",
        workflow_name: "Avatar Video",
        task_id: "",
        output_ids: ["solo"],
      },
      {
        id: "album:t1",
        title: "Fashion Try-On Studio",
        workflow_id: "fashion-tryon-h3",
        workflow_name: "Fashion Try-On Studio",
        task_id: "t1",
        // Natural item order preserved: look_1, look_2, then inspect
        output_ids: ["a1", "a2", "a_insp"],
      },
    ]);

    // Empty selections are omitted
    expect(
      buildColabOneOutputShareGroupInputs(groups, {
        "item:solo": [],
        "album:t1": ["does-not-exist"],
      }),
    ).toEqual([]);
  });
});

describe("sortColabOneGalleryItems & resolveColabOneCoverItems", () => {
  it("handles generic gallery items cleanly including 00_抠衣质检_ and 00_inspect_ prefixes", () => {
    const sorted = sortColabOneGalleryItems([
      { id: "4", filename: "00_抠衣质检_garment.png" },
      { id: "3", filename: "rmbg_inspect_1.png" },
      { id: "2", filename: "shot_12.png" },
      { id: "1", filename: "shot_2.png" },
    ]);
    expect(sorted.map((item) => item.id)).toEqual(["1", "2", "4", "3"]);
    expect(resolveColabOneCoverItems(sorted, 4).map((i) => i.id)).toEqual([
      "1",
      "2",
    ]);
  });

  it("counts each multi-output album as 1 group and deduplicates CU by prompt_id", () => {
    const outputs: WorkflowOutput[] = [
      makeOutput({
        id: "a1",
        task_id: "task-album",
        prompt_id: "p1",
        filename: "look_1.png",
        cu_consumed: 0.42,
      }),
      makeOutput({
        id: "a2",
        task_id: "task-album",
        prompt_id: "p1",
        filename: "look_2.png",
        cu_consumed: 0.42,
      }),
      makeOutput({
        id: "a3",
        task_id: "task-album",
        prompt_id: "p2",
        filename: "00_抠衣质检_coat.png",
        cu_consumed: 0.08,
      }),
      makeOutput({
        id: "a4",
        task_id: "task-album",
        prompt_id: "",
        filename: "look_3.png",
        cu_consumed: 0.1,
      }),
      makeOutput({
        id: "solo1",
        task_id: "",
        filename: "solo.png",
        cu_consumed: 0.25,
      }),
    ];

    const groups = groupColabOneOutputs(outputs);
    // 4-item album + 1 standalone output = 2 output groups total
    expect(groups).toHaveLength(2);
    const album = groups.find((g) => g.isAlbum)!;
    expect(album.items).toHaveLength(4);
    expect(album.inspectCount).toBe(1);
    expect(album.items[album.items.length - 1].filename).toBe(
      "00_抠衣质检_coat.png",
    );
    expect(album.coverItems.map((i) => i.id)).toEqual(["a1", "a2", "a4"]);

    // Shared prompt_id "p1" counted once (0.42), plus "p2" (0.08) + unprompted (0.10) = 0.60
    const cu = computeColabOneGroupCU(album.items);
    expect(cu.total).toBeCloseTo(0.6, 5);
    expect(cu.hasAny).toBe(true);

    // Non-finite / zero values produce hasAny=false
    expect(
      computeColabOneGroupCU([
        makeOutput({ id: "z1", cu_consumed: 0 }),
        makeOutput({ id: "z2", cu_consumed: Number.NaN }),
      ]),
    ).toEqual({ total: 0, hasAny: false, anyEstimated: false, sample: null });

    // Prefers a cu_consumed > 0 sample carrying gpu_name / cu_rate metadata
    const richSampleCU = computeColabOneGroupCU([
      makeOutput({ id: "m1", prompt_id: "p-meta", cu_consumed: 0.3 }),
      makeOutput({
        id: "m2",
        prompt_id: "p-meta",
        cu_consumed: 0.3,
        cu_rate: 1.96,
      }),
      makeOutput({
        id: "m3",
        prompt_id: "p-meta",
        cu_consumed: 0.3,
        gpu_name: "L4",
        cu_rate: 1.96,
      }),
    ]);
    expect(richSampleCU.sample?.id).toBe("m3");
    expect(richSampleCU.sample?.gpu_name).toBe("L4");
    expect(richSampleCU.sample?.cu_rate).toBe(1.96);
  });

  it("sanitizes share group IDs consistently", () => {
    expect(sanitizeColabOneShareGroupId("album:task-1_ok", 0)).toBe(
      "album:task-1_ok",
    );
    expect(
      sanitizeColabOneShareGroupId(" album:bad id/with*chars ", 1),
    ).toBe("album:bad_id_with_chars");
    expect(sanitizeColabOneShareGroupId("   ", 2)).toBe("group-3");
  });
});
