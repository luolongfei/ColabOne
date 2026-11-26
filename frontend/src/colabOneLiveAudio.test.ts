import { afterEach, describe, expect, it, vi } from "vitest";
import {
  COLABONE_LIVE_MIC_FRAME_SAMPLES,
  COLABONE_LIVE_PLAYER_SUSPENDED_CAP,
  ColabOneLiveAudioPlayer,
  ColabOneLiveMicCapture,
  ColabOneStreamingResampler,
  colabOneComputeAudioRms,
  colabOneFloat32ToInt16Pcm,
  colabOneInt16PcmToFloat32,
  colabOneResampleLinear,
} from "./colabOneLiveAudio";

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

function createFakeAudioContext(
  initialTime = 1.0,
  initialState: AudioContextState = "running",
) {
  let currentTime = initialTime;
  let state: AudioContextState = initialState;
  const createdGains: Array<{
    gain: {
      value: number;
      cancelScheduledValues: ReturnType<typeof vi.fn>;
      setValueAtTime: ReturnType<typeof vi.fn>;
      linearRampToValueAtTime: ReturnType<typeof vi.fn>;
    };
    connect: ReturnType<typeof vi.fn>;
    disconnect: ReturnType<typeof vi.fn>;
  }> = [];
  const createdSources: Array<{
    buffer: AudioBuffer | null;
    onended: (() => void) | null;
    connect: ReturnType<typeof vi.fn>;
    disconnect: ReturnType<typeof vi.fn>;
    start: ReturnType<typeof vi.fn>;
    stop: ReturnType<typeof vi.fn>;
  }> = [];

  const ctx = {
    get currentTime() {
      return currentTime;
    },
    setCurrentTime(t: number) {
      currentTime = t;
    },
    get state() {
      return state;
    },
    setState(s: AudioContextState) {
      state = s;
    },
    destination: {} as AudioDestinationNode,
    resume: vi.fn(async () => {
      state = "running";
    }),
    close: vi.fn(async () => {
      state = "closed";
    }),
    createGain: vi.fn(() => {
      const g = {
        gain: {
          value: 1,
          cancelScheduledValues: vi.fn(),
          setValueAtTime: vi.fn((val: number) => {
            g.gain.value = val;
          }),
          linearRampToValueAtTime: vi.fn(),
        },
        connect: vi.fn(),
        disconnect: vi.fn(),
      };
      createdGains.push(g);
      return g as unknown as GainNode;
    }),
    createBuffer: vi.fn((_ch: number, length: number, sampleRate: number) => {
      const data = new Float32Array(length);
      return {
        length,
        sampleRate,
        duration: length / sampleRate,
        getChannelData: () => data,
      } as unknown as AudioBuffer;
    }),
    createBufferSource: vi.fn(() => {
      const src = {
        buffer: null as AudioBuffer | null,
        onended: null as (() => void) | null,
        connect: vi.fn(),
        disconnect: vi.fn(),
        start: vi.fn(),
        stop: vi.fn(),
      };
      createdSources.push(src);
      return src as unknown as AudioBufferSourceNode;
    }),
  };
  return {
    ctx: ctx as unknown as AudioContext & {
      setCurrentTime(t: number): void;
      setState(s: AudioContextState): void;
    },
    createdGains,
    createdSources,
  };
}

describe("colabOneLiveAudio pure PCM & DSP helpers", () => {
  it("converts Float32 to Int16 PCM with boundary clamping and NaN safety", () => {
    const input = new Float32Array([-2.5, -1, -0.5, 0, 0.5, 1, 1.8, Number.NaN]);
    const pcm = colabOneFloat32ToInt16Pcm(input);
    expect(Array.from(pcm)).toEqual([
      -32768,
      -32768,
      -16384,
      0,
      16384,
      32767,
      32767,
      0,
    ]);
  });

  it("round-trips Int16 PCM to Float32 within 1 LSB tolerance", () => {
    const pcm = new Int16Array([-32768, -16384, -1, 0, 1, 16384, 32767]);
    const floats = colabOneInt16PcmToFloat32(pcm);
    expect(floats[0]).toBe(-1);
    expect(floats[3]).toBe(0);
    expect(floats[6]).toBe(1);

    const back = colabOneFloat32ToInt16Pcm(floats);
    expect(Array.from(back)).toEqual(Array.from(pcm));
  });

  it("resamples 48 kHz to 16 kHz cleanly to 320-sample 20 ms frames", () => {
    const input48k = new Float32Array(960);
    for (let i = 0; i < input48k.length; i += 1) {
      input48k[i] = Math.sin((2 * Math.PI * 440 * i) / 48000);
    }
    const output16k = colabOneResampleLinear(input48k, 48000, 16000);
    expect(output16k.length).toBe(320);
    // Verify sine wave shape at sampled indices matches closely.
    expect(Math.abs(output16k[0] - input48k[0])).toBeLessThan(1e-5);
    expect(Math.abs(output16k[10] - input48k[30])).toBeLessThan(1e-5);
  });

  it("handles same-rate copy, empty arrays, and invalid sample rates", () => {
    expect(colabOneResampleLinear(new Float32Array(0), 48000, 16000).length).toBe(0);
    const orig = new Float32Array([0.1, -0.2, 0.3]);
    const copy = colabOneResampleLinear(orig, 16000, 16000);
    expect(copy).not.toBe(orig);
    expect(Array.from(copy)).toEqual(Array.from(orig));
    expect(() => colabOneResampleLinear(orig, 0, 16000)).toThrow(RangeError);
    expect(() => colabOneResampleLinear(orig, 16000, -1)).toThrow(RangeError);
  });

  it("computes normalized RMS amplitude accurately", () => {
    expect(colabOneComputeAudioRms(new Float32Array(0))).toBe(0);
    expect(colabOneComputeAudioRms(new Float32Array([0, 0, 0]))).toBe(0);
    expect(colabOneComputeAudioRms(new Float32Array([0.5, -0.5, 0.5, -0.5]))).toBeCloseTo(0.5, 6);
    expect(colabOneComputeAudioRms(new Float32Array([2, -2]))).toBe(1);
  });
});

describe("ColabOneStreamingResampler phase continuity across 128-sample quanta", () => {
  for (const fromRate of [48000, 44100]) {
    it(`maintains exact sample count and zero phase-jump discontinuities for ${fromRate} Hz to 16 kHz`, () => {
      const toRate = 16000;
      const numChunks = 30;
      const chunkSize = 128;
      const totalInput = numChunks * chunkSize;
      const freq = 440;

      const fullInput = new Float32Array(totalInput);
      for (let i = 0; i < totalInput; i += 1) {
        fullInput[i] = Math.sin((2 * Math.PI * freq * i) / fromRate);
      }

      const streamedResampler = new ColabOneStreamingResampler(fromRate, toRate);
      const chunks: Float32Array[] = [];
      for (let c = 0; c < numChunks; c += 1) {
        const quantum = fullInput.subarray(c * chunkSize, (c + 1) * chunkSize);
        chunks.push(streamedResampler.process(quantum));
      }

      const totalOut = chunks.reduce((acc, arr) => acc + arr.length, 0);
      const expectedOut = (totalInput * toRate) / fromRate;
      expect(Math.abs(totalOut - expectedOut)).toBeLessThanOrEqual(1);

      const streamed = new Float32Array(totalOut);
      let offset = 0;
      for (const part of chunks) {
        streamed.set(part, offset);
        offset += part.length;
      }

      // Must be bit-equivalent to processing the entire stream in one continuous buffer.
      const monolithic = new ColabOneStreamingResampler(fromRate, toRate).process(
        fullInput,
      );
      expect(streamed.length).toBe(monolithic.length);
      let maxDiff = 0;
      for (let i = 0; i < streamed.length; i += 1) {
        const d = Math.abs(streamed[i] - monolithic[i]);
        if (d > maxDiff) maxDiff = d;
      }
      expect(maxDiff).toBeLessThan(1e-5);

      // Verify smooth second-order derivative across all samples (no boundary clicks/jumps).
      const maxExpectedStep = (2 * Math.PI * freq) / toRate;
      for (let i = 2; i < streamed.length; i += 1) {
        const step = Math.abs(streamed[i] - streamed[i - 1]);
        expect(step).toBeLessThanOrEqual(maxExpectedStep * 1.15);
      }
    });
  }
});

describe("ColabOneLiveMicCapture gating, framing & abort-generation safety", () => {
  it("emits exact 320-sample (640-byte) 16 kHz frames in VAD mode and gates in PTT mode", () => {
    const frames: Int16Array[] = [];
    const levels: number[] = [];
    const mic = new ColabOneLiveMicCapture({
      mode: "vad",
      onPcmFrame: (pcm) => frames.push(pcm),
      onLevel: (rms) => levels.push(rms),
    });

    // Feed 960 samples at 48 kHz = 320 samples at 16 kHz (one 20ms frame).
    const chunk48k = new Float32Array(960).fill(0.25);
    mic.ingestFloatSamples(chunk48k, 48000);
    expect(frames.length).toBe(1);
    expect(frames[0].length).toBe(COLABONE_LIVE_MIC_FRAME_SAMPLES);
    expect(frames[0].byteLength).toBe(640);
    expect(levels[0]).toBeCloseTo(0.25, 3);

    // Switch to PTT mode without pressing: level still updates, uplink suppressed.
    mic.setMode("ptt");
    mic.ingestFloatSamples(chunk48k, 48000);
    expect(frames.length).toBe(1);
    expect(levels.length).toBe(2);

    // Hold PTT: uplink resumes immediately.
    mic.setPttActive(true);
    mic.ingestFloatSamples(chunk48k, 48000);
    expect(frames.length).toBe(2);
    expect(frames[1].byteLength).toBe(640);

    // Release PTT: uplink stops again.
    mic.setPttActive(false);
    mic.ingestFloatSamples(chunk48k, 48000);
    expect(frames.length).toBe(2);
  });

  it("aborts cleanly when stop() is called while start() is awaiting getUserMedia or worklet", async () => {
    const trackStop = vi.fn();
    let resolveGum!: (stream: MediaStream) => void;
    const gumPromise = new Promise<MediaStream>((resolve) => {
      resolveGum = resolve;
    });
    const createScriptProcessor = vi.fn();
    const closeSpy = vi.fn(async () => {});

    vi.stubGlobal("navigator", {
      mediaDevices: {
        getUserMedia: vi.fn(() => gumPromise),
      },
    });
    vi.stubGlobal(
      "AudioContext",
      class {
        state = "running";
        sampleRate = 16000;
        destination = {};
        createMediaStreamSource = vi.fn(() => ({ connect: vi.fn(), disconnect: vi.fn() }));
        createGain = vi.fn(() => ({ gain: { value: 0 }, connect: vi.fn(), disconnect: vi.fn() }));
        createScriptProcessor = createScriptProcessor;
        close = closeSpy;
      },
    );

    const onError = vi.fn();
    const mic = new ColabOneLiveMicCapture({
      onPcmFrame: vi.fn(),
      onError,
    });

    const startPromise = mic.start();
    // Stop immediately before getUserMedia resolves.
    mic.stop();
    resolveGum({
      getTracks: () => [{ stop: trackStop }],
    } as unknown as MediaStream);
    await startPromise;

    expect(mic.isRunning()).toBe(false);
    expect(trackStop).toHaveBeenCalledTimes(1);
    expect(createScriptProcessor).not.toHaveBeenCalled();
    expect(onError).not.toHaveBeenCalled();
  });
});

describe("ColabOneLiveAudioPlayer scheduling, suspended cap & barge-in fade-out", () => {
  it("schedules 24 kHz PCM chunks seamlessly, drops stale epochs, and fades out on interrupt", () => {
    vi.useFakeTimers();
    const { ctx, createdGains, createdSources } = createFakeAudioContext(2.0);
    const states: boolean[] = [];
    const player = new ColabOneLiveAudioPlayer({
      sampleRate: 24000,
      fadeOutMs: 30,
      initialEpoch: 1,
      audioContextFactory: () => ctx,
      onPlaybackStateChange: (p) => states.push(p),
    });

    expect(player.getAudioState()).toBe("suspended");

    const pcm = new Int16Array(2400).fill(8000); // 100 ms @ 24 kHz
    // Stale epoch 0 must be dropped.
    expect(player.enqueue(0, 0, pcm)).toBe(false);
    expect(createdSources.length).toBe(0);

    // Epoch 1 enqueues two back-to-back 100 ms chunks anchored at now + 25ms cushion.
    expect(player.enqueue(1, 0, pcm)).toBe(true);
    expect(player.getAudioState()).toBe("running");
    expect(player.enqueue(1, 100, pcm)).toBe(true);
    expect(createdSources.length).toBe(2);
    expect(createdSources[0].start).toHaveBeenCalledWith(2.025);
    expect(createdSources[1].start).toHaveBeenCalledWith(2.125);
    expect(states).toEqual([true]);

    // Seamless continuation even when nextPlayTime is only 1 ms ahead of now.
    ctx.setCurrentTime(2.224);
    expect(player.enqueue(1, 200, pcm)).toBe(true);
    expect(createdSources[2].start).toHaveBeenCalledWith(expect.closeTo(2.225, 6));

    // Advance time into first chunk (later unstarted sources must cancel immediately) and barge-in to epoch 2.
    ctx.setCurrentTime(2.05);
    // Simulate WebKit throwing on stop() for unstarted source; disconnect() must STILL execute!
    createdSources[1].stop.mockImplementationOnce(() => {
      throw new Error("InvalidStateError");
    });
    player.interrupt(2);

    expect(player.getCurrentEpoch()).toBe(2);
    expect(createdSources[1].disconnect).toHaveBeenCalled();
    // Epoch 1 gain node (index 1, after master gain at index 0) must ramp to 0 in 30 ms.
    const epoch1Gain = createdGains[1];
    expect(epoch1Gain.gain.linearRampToValueAtTime).toHaveBeenCalledWith(
      0,
      expect.closeTo(2.08, 6),
    );
    // Active first source stops after fade-out; unstarted third source stops immediately.
    expect(createdSources[0].stop).toHaveBeenCalledWith(expect.closeTo(2.085, 6));
    expect(createdSources[2].stop).toHaveBeenCalledWith(2.05);
    // Fresh gain node created for epoch 2.
    expect(createdGains.length).toBe(3);
    expect(states).toEqual([true, false]);

    // Epoch 1 trailing chunk arriving late must be dropped.
    expect(player.enqueue(1, 200, pcm)).toBe(false);
    // Epoch 2 chunk plays cleanly on the new gain node with fresh 25ms cushion.
    expect(player.enqueue(2, 0, pcm)).toBe(true);
    expect(createdSources.length).toBe(4);
    expect(createdSources[3].start).toHaveBeenCalledWith(expect.closeTo(2.075, 6));

    // Volume and mute controls adjust master gain.
    player.setVolume(0.4);
    expect(createdGains[0].gain.setValueAtTime).toHaveBeenCalledWith(0.4, 2.05);
    player.setMuted(true);
    expect(createdGains[0].gain.setValueAtTime).toHaveBeenCalledWith(0, 2.05);

    vi.advanceTimersByTime(100);
    expect(epoch1Gain.disconnect).toHaveBeenCalled();

    player.destroy();
    expect(player.getAudioState()).toBe("closed");
    expect(ctx.close).toHaveBeenCalled();
  });

  it("caps suspended buffering and enforces 2.5s max queued playout horizon", async () => {
    const { ctx, createdSources } = createFakeAudioContext(1.0, "suspended");
    // Keep ctx suspended until we explicitly call resume().
    ctx.resume = vi.fn(async () => {});

    const player = new ColabOneLiveAudioPlayer({
      sampleRate: 24000,
      initialEpoch: 1,
      audioContextFactory: () => ctx,
    });

    const pcm = new Int16Array(2400).fill(4000); // 100 ms
    for (let i = 0; i < 12; i += 1) {
      expect(player.enqueue(1, i * 100, pcm)).toBe(false);
    }
    expect(createdSources.length).toBe(0);
    expect(player.getAudioState()).toBe("suspended");

    // Once context transitions to running and resume() runs, only capped chunks flush.
    ctx.setState("running");
    await player.resume();
    expect(createdSources.length).toBe(COLABONE_LIVE_PLAYER_SUSPENDED_CAP);

    // Enqueue 30 more 100ms chunks (3.0s total): must cap queued horizon at <= 2.5s ahead of now.
    let accepted = 0;
    for (let i = 0; i < 30; i += 1) {
      if (player.enqueue(1, 1000 + i * 100, pcm)) {
        accepted += 1;
      }
    }
    // 5 flushed (0.5s) + 19 more (1.9s) + 0.025s cushion = 2.425s <= 2.5s (total 24 sources).
    expect(createdSources.length).toBeLessThanOrEqual(25);
    expect(accepted).toBeLessThan(30);

    player.destroy();
  });

  it("guards automatic ctx.resume() while suspended to prevent redundant calls per chunk", async () => {
    const { ctx } = createFakeAudioContext(1.0, "suspended");
    // Simulate browser autoplay policy keeping context suspended on automatic resume.
    ctx.resume = vi.fn(async () => {});

    const player = new ColabOneLiveAudioPlayer({
      sampleRate: 24000,
      initialEpoch: 1,
      audioContextFactory: () => ctx,
    });

    const pcm = new Int16Array(480).fill(2000); // 20 ms
    for (let i = 0; i < 50; i += 1) {
      expect(player.enqueue(1, i * 20, pcm)).toBe(false);
    }
    // Must attempt automatic resume only once while staying suspended.
    expect(ctx.resume).toHaveBeenCalledTimes(1);

    // Explicit user gesture resume() resets the guard and retries ctx.resume().
    await player.resume();
    expect(ctx.resume).toHaveBeenCalledTimes(2);

    // Once context runs and later suspends again, a single fresh auto-resume is allowed.
    ctx.setState("running");
    expect(player.enqueue(1, 1200, pcm)).toBe(true);
    ctx.setState("suspended");
    for (let i = 0; i < 10; i += 1) {
      expect(player.enqueue(1, 1300 + i * 20, pcm)).toBe(false);
    }
    expect(ctx.resume).toHaveBeenCalledTimes(3);

    player.destroy();
  });

  it("resets epoch downward on server restart via resetEpoch() and plays new epoch chunks", () => {
    vi.useFakeTimers();
    const { ctx, createdSources } = createFakeAudioContext(3.0);
    const player = new ColabOneLiveAudioPlayer({
      sampleRate: 24000,
      initialEpoch: 5,
      audioContextFactory: () => ctx,
    });

    const pcm = new Int16Array(2400).fill(6000);
    expect(player.enqueue(5, 0, pcm)).toBe(true);
    expect(player.getCurrentEpoch()).toBe(5);
    expect(createdSources.length).toBe(1);

    // Epoch 0 is rejected prior to resetEpoch.
    expect(player.enqueue(0, 0, pcm)).toBe(false);

    // After server restart resets epoch to 0, resetEpoch(0) accepts epoch 0 audio cleanly.
    player.resetEpoch(0);
    expect(player.getCurrentEpoch()).toBe(0);
    expect(player.enqueue(0, 0, pcm)).toBe(true);
    expect(createdSources.length).toBe(2);

    vi.advanceTimersByTime(100);
    player.destroy();
  });
});
