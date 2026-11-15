import { describe, it, expect } from "vitest";
import { canLoad, initialSnapshot, type ModelSpec } from "./types";
const model: ModelSpec = {
  id: "test",
  name: "test",
  description: "",
  downloaded: true,
  loaded: false,
  size: 1,
  min_vram_mib: 1,
  files: [],
};
describe("remote-only operation gates", () => {
  it("never loads without an active remote device", () =>
    expect(canLoad(model, initialSnapshot())).toBe(false));
  it("requires files on this runtime", () => {
    const state = initialSnapshot();
    state.runtime.state = "ready";
    expect(canLoad({ ...model, downloaded: false }, state)).toBe(false);
  });
  it("blocks a model switch during generation", () => {
    const state = initialSnapshot();
    state.runtime.state = "ready";
    state.runtime.tasks = [
      {
        id: "task",
        kind: "chat",
        model_id: "other",
        state: "running",
        progress: 0,
        message: "",
        error: "",
      },
    ];
    expect(canLoad(model, state)).toBe(false);
  });
  it("permits a downloaded model when the remote device is idle", () => {
    const state = initialSnapshot();
    state.runtime.state = "ready";
    expect(canLoad(model, state)).toBe(true);
  });
});
