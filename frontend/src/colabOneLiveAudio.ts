import type { ColabOneLiveMicMode } from "./types";

export const COLABONE_LIVE_MIC_SAMPLE_RATE = 16000;
export const COLABONE_LIVE_MIC_FRAME_MS = 20;
export const COLABONE_LIVE_MIC_FRAME_SAMPLES =
  (COLABONE_LIVE_MIC_SAMPLE_RATE * COLABONE_LIVE_MIC_FRAME_MS) / 1000; // 320 samples (640 bytes)
export const COLABONE_LIVE_AUDIO_OUT_SAMPLE_RATE = 24000;
export const COLABONE_LIVE_BARGE_IN_FADE_MS = 30;
export const COLABONE_LIVE_PLAYER_JITTER_CUSHION_SEC = 0.025;
export const COLABONE_LIVE_PLAYER_MAX_PLAYOUT_SEC = 2.5;
export const COLABONE_LIVE_PLAYER_SUSPENDED_CAP = 5;

/**
 * Returns true when a getUserMedia error indicates the host machine has no
 * physical or virtual microphone device attached (common on Windows servers).
 */
export function colabOneIsMissingAudioDeviceError(err: unknown): boolean {
  if (!err) return false;
  const name =
    typeof err === "object" && err !== null && "name" in err
      ? String((err as { name?: unknown }).name || "")
      : "";
  const msg =
    typeof err === "object" && err !== null && "message" in err
      ? String((err as { message?: unknown }).message || "")
      : String(err);
  const combined = `${name} ${msg}`.toLowerCase();
  return (
    combined.includes("notfounderror") ||
    combined.includes("devicesnotfounderror") ||
    combined.includes("requested device not found") ||
    combined.includes("no audio input") ||
    combined.includes("microphone capture is unavailable")
  );
}

/**
 * Converts normalized Float32 PCM samples in [-1, 1] into signed 16-bit PCM.
 */
export function colabOneFloat32ToInt16Pcm(input: Float32Array): Int16Array {
  const out = new Int16Array(input.length);
  for (let i = 0; i < input.length; i += 1) {
    const raw = input[i];
    if (!Number.isFinite(raw)) {
      out[i] = 0;
      continue;
    }
    const clamped = raw < -1 ? -1 : raw > 1 ? 1 : raw;
    out[i] =
      clamped < 0
        ? Math.max(-32768, Math.round(clamped * 32768))
        : Math.min(32767, Math.round(clamped * 32767));
  }
  return out;
}

/**
 * Converts signed 16-bit PCM samples into normalized Float32 PCM in [-1, 1].
 */
export function colabOneInt16PcmToFloat32(input: Int16Array): Float32Array {
  const out = new Float32Array(input.length);
  for (let i = 0; i < input.length; i += 1) {
    const sample = input[i];
    out[i] = sample < 0 ? sample / 32768 : sample / 32767;
  }
  return out;
}

/**
 * Linearly resamples mono Float32 PCM from `fromRate` Hz to `toRate` Hz.
 */
export function colabOneResampleLinear(
  input: Float32Array,
  fromRate: number,
  toRate: number,
): Float32Array {
  if (input.length === 0) {
    return new Float32Array(0);
  }
  if (!Number.isFinite(fromRate) || fromRate <= 0) {
    throw new RangeError(`Invalid source sample rate: ${fromRate}`);
  }
  if (!Number.isFinite(toRate) || toRate <= 0) {
    throw new RangeError(`Invalid target sample rate: ${toRate}`);
  }
  if (fromRate === toRate) {
    return new Float32Array(input);
  }
  const ratio = fromRate / toRate;
  const outLen = Math.max(1, Math.round(input.length / ratio));
  const out = new Float32Array(outLen);
  const lastIdx = input.length - 1;
  for (let i = 0; i < outLen; i += 1) {
    const pos = i * ratio;
    const idx0 = Math.floor(pos);
    if (idx0 >= lastIdx) {
      out[i] = input[lastIdx];
      continue;
    }
    const frac = pos - idx0;
    const s0 = input[idx0];
    const s1 = input[idx0 + 1];
    out[i] = s0 + (s1 - s0) * frac;
  }
  return out;
}

/**
 * Phase-continuous stateful streaming resampler for arbitrary audio quanta
 * (e.g. 128-sample AudioWorklet or 1024-sample ScriptProcessor blocks).
 * Preserves fractional phase cursor, previous boundary sample, and a causal
 * 3-tap low-pass anti-aliasing filter [0.25, 0.5, 0.25] when downsampling.
 */
export class ColabOneStreamingResampler {
  readonly fromRate: number;
  readonly toRate: number;
  private readonly ratio: number;
  private readonly isDownsampling: boolean;

  private phase = 0;
  private prevSample = 0;
  private lpPrev1 = 0;
  private lpPrev2 = 0;
  private lpInitialized = false;

  constructor(fromRate: number, toRate: number) {
    if (!Number.isFinite(fromRate) || fromRate <= 0) {
      throw new RangeError(`Invalid source sample rate: ${fromRate}`);
    }
    if (!Number.isFinite(toRate) || toRate <= 0) {
      throw new RangeError(`Invalid target sample rate: ${toRate}`);
    }
    this.fromRate = fromRate;
    this.toRate = toRate;
    this.ratio = fromRate / toRate;
    this.isDownsampling = fromRate > toRate;
  }

  reset(): void {
    this.phase = 0;
    this.prevSample = 0;
    this.lpPrev1 = 0;
    this.lpPrev2 = 0;
    this.lpInitialized = false;
  }

  process(input: Float32Array): Float32Array {
    const len = input.length;
    if (len === 0) {
      return new Float32Array(0);
    }
    if (this.fromRate === this.toRate) {
      return new Float32Array(input);
    }

    let src: Float32Array;
    if (this.isDownsampling) {
      src = new Float32Array(len);
      if (!this.lpInitialized) {
        const first = Number.isFinite(input[0]) ? input[0] : 0;
        this.lpPrev2 = first;
        this.lpPrev1 = first;
        this.lpInitialized = true;
      }
      let p2 = this.lpPrev2;
      let p1 = this.lpPrev1;
      for (let i = 0; i < len; i += 1) {
        const raw = input[i];
        const x = Number.isFinite(raw) ? raw : 0;
        src[i] = 0.25 * p2 + 0.5 * p1 + 0.25 * x;
        p2 = p1;
        p1 = x;
      }
      this.lpPrev2 = p2;
      this.lpPrev1 = p1;
    } else {
      src = input;
    }

    const lastIdx = len - 1;
    if (this.phase > lastIdx) {
      this.prevSample = src[lastIdx];
      this.phase -= len;
      return new Float32Array(0);
    }

    const estimatedCap = Math.max(
      1,
      Math.floor((lastIdx - this.phase) / this.ratio) + 2,
    );
    const out = new Float32Array(estimatedCap);
    let written = 0;
    let cursor = this.phase;

    while (cursor <= lastIdx) {
      const idx0 = Math.floor(cursor);
      const frac = cursor - idx0;
      let sample: number;
      if (idx0 < 0) {
        const s0 = this.prevSample;
        const s1 = src[0];
        sample = s0 + (s1 - s0) * frac;
      } else if (idx0 >= lastIdx) {
        sample = src[lastIdx];
      } else {
        const s0 = src[idx0];
        const s1 = src[idx0 + 1];
        sample = s0 + (s1 - s0) * frac;
      }
      out[written] = sample;
      written += 1;
      cursor += this.ratio;
    }

    this.prevSample = src[lastIdx];
    this.phase = cursor - len;
    return written === out.length ? out : out.subarray(0, written).slice();
  }
}

/**
 * Computes normalized root-mean-square amplitude in [0, 1].
 */
export function colabOneComputeAudioRms(samples: Float32Array): number {
  if (samples.length === 0) return 0;
  let sumSq = 0;
  let count = 0;
  for (let i = 0; i < samples.length; i += 1) {
    const v = samples[i];
    if (!Number.isFinite(v)) continue;
    sumSq += v * v;
    count += 1;
  }
  if (count === 0) return 0;
  const rms = Math.sqrt(sumSq / count);
  return rms > 1 ? 1 : rms < 0 ? 0 : rms;
}

const COLABONE_MIC_WORKLET_NAME = "colabone-live-mic-processor";
const COLABONE_MIC_WORKLET_SOURCE = `
class ColabOneLiveMicProcessor extends AudioWorkletProcessor {
  process(inputs) {
    const input = inputs && inputs[0];
    if (input && input[0] && input[0].length > 0) {
      const copy = new Float32Array(input[0].length);
      copy.set(input[0]);
      this.port.postMessage(copy.buffer, [copy.buffer]);
    }
    return true;
  }
}
registerProcessor("${COLABONE_MIC_WORKLET_NAME}", ColabOneLiveMicProcessor);
`;

export interface ColabOneLiveMicCaptureOptions {
  mode?: ColabOneLiveMicMode;
  targetSampleRate?: number;
  frameSamples?: number;
  onPcmFrame: (pcm: Int16Array, rms: number) => void;
  onLevel?: (rms: number) => void;
  onError?: (err: Error) => void;
}

function getAudioContextConstructor(): typeof AudioContext | null {
  const g = globalThis as typeof globalThis & {
    webkitAudioContext?: typeof AudioContext;
  };
  return g.AudioContext ?? g.webkitAudioContext ?? null;
}

function isAudioContextClosed(ctx: AudioContext): boolean {
  return ctx.state === "closed";
}

/**
 * Captures mono 16 kHz Int16 PCM microphone frames (20 ms / 640 bytes each)
 * via AudioWorkletNode with automatic ScriptProcessorNode fallback.
 */
export class ColabOneLiveMicCapture {
  private readonly targetSampleRate: number;
  private readonly frameSamples: number;
  private readonly onPcmFrame: (pcm: Int16Array, rms: number) => void;
  private readonly onLevel?: (rms: number) => void;
  private readonly onError?: (err: Error) => void;

  private mode: ColabOneLiveMicMode;
  private pttActive = false;
  private running = false;
  private startGen = 0;
  private mediaStream: MediaStream | null = null;
  private audioCtx: AudioContext | null = null;
  private sourceNode: MediaStreamAudioSourceNode | null = null;
  private workletNode: AudioWorkletNode | null = null;
  private scriptNode: ScriptProcessorNode | null = null;
  private silentGain: GainNode | null = null;
  private residual: Float32Array = new Float32Array(0);
  private resampler: ColabOneStreamingResampler | null = null;

  constructor(options: ColabOneLiveMicCaptureOptions) {
    this.mode = options.mode ?? "vad";
    this.targetSampleRate =
      options.targetSampleRate ?? COLABONE_LIVE_MIC_SAMPLE_RATE;
    this.frameSamples = options.frameSamples ?? COLABONE_LIVE_MIC_FRAME_SAMPLES;
    this.onPcmFrame = options.onPcmFrame;
    this.onLevel = options.onLevel;
    this.onError = options.onError;
  }

  getMode(): ColabOneLiveMicMode {
    return this.mode;
  }

  setMode(mode: ColabOneLiveMicMode): void {
    this.mode = mode;
    if (mode === "vad") {
      this.pttActive = false;
    }
  }

  isPttActive(): boolean {
    return this.pttActive;
  }

  setPttActive(active: boolean): void {
    this.pttActive = Boolean(active);
    if (!this.pttActive && this.mode === "ptt") {
      // Discard buffered pre-release audio so the next press starts cleanly.
      this.residual = new Float32Array(0);
      this.resampler?.reset();
    }
  }

  isRunning(): boolean {
    return this.running;
  }

  async start(): Promise<void> {
    if (this.running) return;
    if (
      typeof navigator === "undefined" ||
      !navigator.mediaDevices ||
      typeof navigator.mediaDevices.getUserMedia !== "function"
    ) {
      throw new Error("Microphone capture is unavailable in this environment");
    }
    const AudioCtxCtor = getAudioContextConstructor();
    if (!AudioCtxCtor) {
      throw new Error("Web Audio API is unavailable in this environment");
    }

    const gen = ++this.startGen;
    this.running = true;
    this.residual = new Float32Array(0);
    this.resampler = null;

    let stream: MediaStream | null = null;
    let ctx: AudioContext | null = null;
    const isAborted = () => gen !== this.startGen || !this.running;
    const cleanupLocal = () => {
      if (stream) {
        for (const track of stream.getTracks()) {
          try {
            track.stop();
          } catch {
            // Ignore teardown errors.
          }
        }
        stream = null;
      }
      if (ctx) {
        const closing = ctx;
        ctx = null;
        void closing.close().catch(() => {});
      }
    };

    try {
      stream = await navigator.mediaDevices.getUserMedia({
        audio: {
          echoCancellation: true,
          noiseSuppression: true,
          autoGainControl: true,
          channelCount: 1,
          sampleRate: this.targetSampleRate,
        },
      });
      if (isAborted()) {
        cleanupLocal();
        return;
      }
      try {
        ctx = new AudioCtxCtor({ sampleRate: this.targetSampleRate });
      } catch {
        ctx = new AudioCtxCtor();
      }
      if (ctx.state === "suspended" || (ctx.state as string) === "interrupted") {
        await ctx.resume();
      }
      if (isAborted()) {
        cleanupLocal();
        return;
      }
      if (isAudioContextClosed(ctx)) {
        throw new Error("AudioContext closed during microphone startup");
      }

      const source = ctx.createMediaStreamSource(stream);
      const silent = ctx.createGain();
      silent.gain.value = 0;

      this.mediaStream = stream;
      this.audioCtx = ctx;
      this.sourceNode = source;
      this.silentGain = silent;
      this.residual = new Float32Array(0);
      this.resampler = null;

      const attachedWorklet = await this.tryAttachWorklet(
        ctx,
        source,
        silent,
        gen,
      );
      if (isAborted() || this.audioCtx !== ctx) {
        if (this.mediaStream === stream || this.audioCtx === ctx) {
          this.stop();
        } else {
          cleanupLocal();
        }
        return;
      }
      if (isAudioContextClosed(ctx)) {
        throw new Error("AudioContext closed during microphone startup");
      }
      if (!attachedWorklet) {
        this.attachScriptProcessorFallback(ctx, source, silent);
      }
    } catch (err) {
      const stillCurrent = gen === this.startGen;
      if (stillCurrent) {
        this.running = false;
        this.mediaStream = null;
        this.audioCtx = null;
        this.sourceNode = null;
        this.silentGain = null;
        this.resampler = null;
      }
      cleanupLocal();
      if (!stillCurrent) {
        return;
      }
      const error = err instanceof Error ? err : new Error(String(err));
      this.onError?.(error);
      throw error;
    }
  }

  private async tryAttachWorklet(
    ctx: AudioContext,
    source: MediaStreamAudioSourceNode,
    silent: GainNode,
    gen: number,
  ): Promise<boolean> {
    if (
      !ctx.audioWorklet ||
      typeof ctx.audioWorklet.addModule !== "function" ||
      typeof AudioWorkletNode === "undefined" ||
      typeof Blob === "undefined" ||
      typeof URL === "undefined" ||
      typeof URL.createObjectURL !== "function"
    ) {
      return false;
    }
    const blob = new Blob([COLABONE_MIC_WORKLET_SOURCE], {
      type: "application/javascript",
    });
    const url = URL.createObjectURL(blob);
    try {
      await ctx.audioWorklet.addModule(url);
      if (gen !== this.startGen || !this.running || ctx.state === "closed") {
        return true;
      }
      const node = new AudioWorkletNode(ctx, COLABONE_MIC_WORKLET_NAME, {
        numberOfInputs: 1,
        numberOfOutputs: 1,
        channelCount: 1,
      });
      node.port.onmessage = (event: MessageEvent) => {
        if (!this.running || gen !== this.startGen) return;
        const data = event.data;
        if (data instanceof ArrayBuffer) {
          this.handleInputFloatChunk(new Float32Array(data), ctx.sampleRate);
        } else if (data instanceof Float32Array) {
          this.handleInputFloatChunk(data, ctx.sampleRate);
        }
      };
      source.connect(node);
      node.connect(silent);
      silent.connect(ctx.destination);
      this.workletNode = node;
      return true;
    } catch {
      return false;
    } finally {
      URL.revokeObjectURL(url);
    }
  }

  private attachScriptProcessorFallback(
    ctx: AudioContext,
    source: MediaStreamAudioSourceNode,
    silent: GainNode,
  ): void {
    const createSP = (
      ctx as AudioContext & {
        createScriptProcessor?: (
          bufferSize: number,
          inCh: number,
          outCh: number,
        ) => ScriptProcessorNode;
      }
    ).createScriptProcessor;
    if (typeof createSP !== "function") {
      throw new Error("Neither AudioWorklet nor ScriptProcessor is available");
    }
    const node = createSP.call(ctx, 1024, 1, 1);
    node.onaudioprocess = (event: AudioProcessingEvent) => {
      if (!this.running) return;
      const channel = event.inputBuffer.getChannelData(0);
      this.handleInputFloatChunk(new Float32Array(channel), ctx.sampleRate);
    };
    source.connect(node);
    node.connect(silent);
    silent.connect(ctx.destination);
    this.scriptNode = node;
  }

  /**
   * Exposed for unit testing and internal audio callback ingestion.
   */
  ingestFloatSamples(samples: Float32Array, inputSampleRate: number): void {
    this.handleInputFloatChunk(samples, inputSampleRate);
  }

  private handleInputFloatChunk(
    samples: Float32Array,
    inputSampleRate: number,
  ): void {
    if (samples.length === 0) return;
    let resampled: Float32Array;
    if (inputSampleRate === this.targetSampleRate) {
      this.resampler = null;
      resampled = samples;
    } else {
      if (
        !this.resampler ||
        this.resampler.fromRate !== inputSampleRate ||
        this.resampler.toRate !== this.targetSampleRate
      ) {
        this.resampler = new ColabOneStreamingResampler(
          inputSampleRate,
          this.targetSampleRate,
        );
      }
      resampled = this.resampler.process(samples);
    }

    if (resampled.length === 0) return;

    const combined = new Float32Array(this.residual.length + resampled.length);
    combined.set(this.residual, 0);
    combined.set(resampled, this.residual.length);

    let offset = 0;
    while (offset + this.frameSamples <= combined.length) {
      const frame = combined.subarray(offset, offset + this.frameSamples);
      offset += this.frameSamples;
      const rms = colabOneComputeAudioRms(frame);
      this.onLevel?.(rms);
      const shouldUplink = this.mode === "vad" || this.pttActive;
      if (shouldUplink) {
        const pcm = colabOneFloat32ToInt16Pcm(frame);
        this.onPcmFrame(pcm, rms);
      }
    }
    this.residual = combined.slice(offset);
  }

  stop(): void {
    this.startGen += 1;
    this.running = false;
    this.pttActive = false;
    this.residual = new Float32Array(0);
    this.resampler = null;
    if (this.workletNode) {
      try {
        this.workletNode.port.onmessage = null;
        this.workletNode.disconnect();
      } catch {
        // Ignore teardown errors.
      }
      this.workletNode = null;
    }
    if (this.scriptNode) {
      try {
        this.scriptNode.onaudioprocess = null;
        this.scriptNode.disconnect();
      } catch {
        // Ignore teardown errors.
      }
      this.scriptNode = null;
    }
    if (this.sourceNode) {
      try {
        this.sourceNode.disconnect();
      } catch {
        // Ignore teardown errors.
      }
      this.sourceNode = null;
    }
    if (this.silentGain) {
      try {
        this.silentGain.disconnect();
      } catch {
        // Ignore teardown errors.
      }
      this.silentGain = null;
    }
    if (this.mediaStream) {
      for (const track of this.mediaStream.getTracks()) {
        try {
          track.stop();
        } catch {
          // Ignore teardown errors.
        }
      }
      this.mediaStream = null;
    }
    if (this.audioCtx) {
      const ctx = this.audioCtx;
      this.audioCtx = null;
      try {
        ctx.onstatechange = null;
      } catch {
        // Ignore teardown errors.
      }
      void ctx.close().catch(() => {});
    }
    this.onLevel?.(0);
  }
}

export interface ColabOneLiveAudioPlayerOptions {
  sampleRate?: number;
  fadeOutMs?: number;
  initialEpoch?: number;
  audioContextFactory?: (sampleRate: number) => AudioContext;
  onPlaybackStateChange?: (playing: boolean) => void;
  onAudioStateChange?: (state: AudioContextState) => void;
}

interface ScheduledSourceEntry {
  source: AudioBufferSourceNode;
  startTime: number;
  endTime: number;
  epoch: number;
}

/**
 * Low-latency 24 kHz Mono Int16 PCM downlink player with epoch isolation,
 * bounded suspended buffering, max playout drift cap, and click-free fade-out
 * on barge-in interruption.
 */
export class ColabOneLiveAudioPlayer {
  private readonly sampleRate: number;
  private readonly fadeOutMs: number;
  private readonly audioContextFactory?: (sampleRate: number) => AudioContext;
  private readonly onPlaybackStateChange?: (playing: boolean) => void;
  private readonly onAudioStateChange?: (state: AudioContextState) => void;

  private audioCtx: AudioContext | null = null;
  private masterGain: GainNode | null = null;
  private epochGain: GainNode | null = null;
  private currentEpoch: number;
  private nextPlayTime = -1;
  private volume = 1;
  private muted = false;
  private destroyed = false;
  private resumeAttemptedWhileSuspended = false;
  private activeSources = new Set<ScheduledSourceEntry>();
  private suspendedQueue: Array<{ epoch: number; pcm: Int16Array }> = [];
  private fadingNodes: Array<{
    gain: GainNode;
    sources: ScheduledSourceEntry[];
    timer: ReturnType<typeof setTimeout> | null;
  }> = [];

  constructor(options: ColabOneLiveAudioPlayerOptions = {}) {
    this.sampleRate =
      options.sampleRate ?? COLABONE_LIVE_AUDIO_OUT_SAMPLE_RATE;
    this.fadeOutMs = options.fadeOutMs ?? COLABONE_LIVE_BARGE_IN_FADE_MS;
    this.currentEpoch = options.initialEpoch ?? 0;
    this.audioContextFactory = options.audioContextFactory;
    this.onPlaybackStateChange = options.onPlaybackStateChange;
    this.onAudioStateChange = options.onAudioStateChange;
  }

  getCurrentEpoch(): number {
    return this.currentEpoch;
  }

  getVolume(): number {
    return this.volume;
  }

  isMuted(): boolean {
    return this.muted;
  }

  isPlaying(): boolean {
    return this.activeSources.size > 0;
  }

  getAudioState(): AudioContextState | "closed" {
    if (this.destroyed) return "closed";
    if (!this.audioCtx) return "suspended";
    return this.audioCtx.state;
  }

  private ensureContext(): AudioContext | null {
    if (this.destroyed) return null;
    if (this.audioCtx) return this.audioCtx;
    let ctx: AudioContext | null = null;
    if (this.audioContextFactory) {
      ctx = this.audioContextFactory(this.sampleRate);
    } else {
      const Ctor = getAudioContextConstructor();
      if (!Ctor) return null;
      try {
        ctx = new Ctor({ sampleRate: this.sampleRate });
      } catch {
        ctx = new Ctor();
      }
    }
    const master = ctx.createGain();
    master.gain.value = this.muted ? 0 : this.volume;
    master.connect(ctx.destination);

    const epochNode = ctx.createGain();
    epochNode.gain.value = 1;
    epochNode.connect(master);

    this.audioCtx = ctx;
    this.masterGain = master;
    this.epochGain = epochNode;
    try {
      ctx.onstatechange = () => {
        if (this.destroyed || this.audioCtx !== ctx) return;
        this.onAudioStateChange?.(ctx.state);
        if (ctx.state === "running") {
          this.resumeAttemptedWhileSuspended = false;
          this.flushSuspendedQueue();
        }
      };
    } catch {
      // Ignore read-only onstatechange in minimal mock AudioContexts.
    }
    return ctx;
  }

  async resume(): Promise<void> {
    const ctx = this.ensureContext();
    if (!ctx || ctx.state === "closed") return;
    this.resumeAttemptedWhileSuspended = false;
    if (ctx.state !== "running") {
      await ctx.resume();
    }
    if (!this.destroyed && ctx.state === "running") {
      this.resumeAttemptedWhileSuspended = false;
      this.flushSuspendedQueue();
    }
  }

  setVolume(volume: number): void {
    const clamped = Number.isFinite(volume)
      ? Math.max(0, Math.min(1, volume))
      : 1;
    this.volume = clamped;
    this.applyMasterGain();
  }

  setMuted(muted: boolean): void {
    this.muted = Boolean(muted);
    this.applyMasterGain();
  }

  private applyMasterGain(): void {
    if (!this.masterGain || !this.audioCtx) return;
    const target = this.muted ? 0 : this.volume;
    const now = this.audioCtx.currentTime;
    try {
      this.masterGain.gain.cancelScheduledValues(now);
      this.masterGain.gain.setValueAtTime(target, now);
    } catch {
      this.masterGain.gain.value = target;
    }
  }

  private flushSuspendedQueue(): void {
    if (this.destroyed || this.suspendedQueue.length === 0) return;
    const pending = this.suspendedQueue;
    this.suspendedQueue = [];
    for (const item of pending) {
      if (item.epoch === this.currentEpoch) {
        this.schedulePcm(item.pcm);
      }
    }
  }

  private schedulePcm(pcmInt16: Int16Array): boolean {
    const ctx = this.audioCtx;
    if (
      this.destroyed ||
      !ctx ||
      !this.epochGain ||
      ctx.state !== "running" ||
      pcmInt16.length === 0
    ) {
      return false;
    }

    const now = ctx.currentTime;
    const chunkDuration = pcmInt16.length / this.sampleRate;
    const startTime =
      this.nextPlayTime >= now
        ? this.nextPlayTime
        : now + COLABONE_LIVE_PLAYER_JITTER_CUSHION_SEC;

    if (
      this.activeSources.size > 0 &&
      startTime + chunkDuration - now > COLABONE_LIVE_PLAYER_MAX_PLAYOUT_SEC
    ) {
      return false;
    }

    const floatSamples = colabOneInt16PcmToFloat32(pcmInt16);
    const buffer = ctx.createBuffer(1, floatSamples.length, this.sampleRate);
    const channelData = buffer.getChannelData(0);
    channelData.set(floatSamples);

    const source = ctx.createBufferSource();
    source.buffer = buffer;
    source.connect(this.epochGain);

    const endTime = startTime + buffer.duration;
    this.nextPlayTime = endTime;

    const wasEmpty = this.activeSources.size === 0;
    const entry: ScheduledSourceEntry = {
      source,
      startTime,
      endTime,
      epoch: this.currentEpoch,
    };
    this.activeSources.add(entry);
    source.onended = () => {
      this.activeSources.delete(entry);
      try {
        source.disconnect();
      } catch {
        // Ignore teardown errors.
      }
      if (this.activeSources.size === 0) {
        this.onPlaybackStateChange?.(false);
      }
    };
    source.start(startTime);
    if (wasEmpty) {
      this.onPlaybackStateChange?.(true);
    }
    return true;
  }

  /**
   * Enqueues a 24 kHz mono Int16 PCM chunk for scheduled playback.
   * Frames older than `currentEpoch` are dropped immediately.
   */
  enqueue(epoch: number, _ptsMs: number, pcmInt16: Int16Array): boolean {
    if (this.destroyed || pcmInt16.length === 0) return false;
    if (epoch < this.currentEpoch) {
      return false;
    }
    if (epoch > this.currentEpoch) {
      this.interrupt(epoch);
    }
    const ctx = this.ensureContext();
    if (!ctx || !this.epochGain || ctx.state === "closed") return false;

    if (ctx.state !== "running") {
      while (this.suspendedQueue.length >= COLABONE_LIVE_PLAYER_SUSPENDED_CAP) {
        this.suspendedQueue.shift();
      }
      this.suspendedQueue.push({
        epoch: this.currentEpoch,
        pcm: new Int16Array(pcmInt16),
      });
      if (!this.resumeAttemptedWhileSuspended) {
        this.resumeAttemptedWhileSuspended = true;
        void ctx
          .resume()
          .then(() => {
            if (!this.destroyed && ctx.state === "running") {
              this.resumeAttemptedWhileSuspended = false;
              this.flushSuspendedQueue();
            }
          })
          .catch(() => {});
      }
      return false;
    }

    this.resumeAttemptedWhileSuspended = false;
    if (this.suspendedQueue.length > 0) {
      this.flushSuspendedQueue();
    }
    return this.schedulePcm(pcmInt16);
  }

  /**
   * Unconditionally resets the player epoch (for example after a remote server
   * restart whose epoch counter starts over) and clears queued/active playback.
   */
  resetEpoch(epoch: number): void {
    const safeEpoch =
      Number.isFinite(epoch) && epoch >= 0 ? Math.floor(epoch) : 0;
    this.interrupt(safeEpoch);
    this.currentEpoch = safeEpoch;
  }

  /**
   * Immediately interrupts playback for barge-in: ramps active audio down over
   * `fadeOutMs` (default 30 ms), cancels unstarted queued sources right away,
   * and advances `currentEpoch` so stale frames are discarded.
   */
  interrupt(newEpoch: number): void {
    if (newEpoch > this.currentEpoch) {
      this.currentEpoch = newEpoch;
    }
    this.nextPlayTime = -1;
    this.suspendedQueue = [];

    const ctx = this.audioCtx;
    const oldGain = this.epochGain;
    const oldSources = Array.from(this.activeSources);
    this.activeSources.clear();

    if (!ctx || !oldGain || !this.masterGain || ctx.state === "closed") {
      if (oldSources.length > 0) {
        this.onPlaybackStateChange?.(false);
      }
      return;
    }

    if (oldSources.length === 0) {
      return;
    }

    const now = ctx.currentTime;
    const fadeSec = Math.max(0.005, this.fadeOutMs / 1000);
    const stopAt = now + fadeSec + 0.005;

    try {
      oldGain.gain.cancelScheduledValues(now);
      oldGain.gain.setValueAtTime(oldGain.gain.value, now);
      oldGain.gain.linearRampToValueAtTime(0, now + fadeSec);
    } catch {
      oldGain.gain.value = 0;
    }

    for (const entry of oldSources) {
      entry.source.onended = null;
      const unstarted = entry.startTime > now;
      try {
        if (unstarted) {
          // Not started yet: cancel immediately so it never renders.
          entry.source.stop(now);
        } else {
          entry.source.stop(stopAt);
        }
      } catch {
        // Source may already be stopped (e.g. WebKit InvalidStateError).
      }
      if (unstarted) {
        try {
          entry.source.disconnect();
        } catch {
          // Ignore teardown errors.
        }
      }
    }

    // Prepare a clean unity-gain bus for the next epoch immediately.
    const nextGain = ctx.createGain();
    nextGain.gain.value = 1;
    nextGain.connect(this.masterGain);
    this.epochGain = nextGain;

    const cleanupMs = Math.ceil((fadeSec + 0.02) * 1000);
    const fadingEntry = {
      gain: oldGain,
      sources: oldSources,
      timer: null as ReturnType<typeof setTimeout> | null,
    };
    fadingEntry.timer = setTimeout(() => {
      for (const s of fadingEntry.sources) {
        try {
          s.source.disconnect();
        } catch {
          // Ignore teardown errors.
        }
      }
      try {
        fadingEntry.gain.disconnect();
      } catch {
        // Ignore teardown errors.
      }
      const idx = this.fadingNodes.indexOf(fadingEntry);
      if (idx >= 0) this.fadingNodes.splice(idx, 1);
    }, cleanupMs);
    this.fadingNodes.push(fadingEntry);

    if (oldSources.length > 0) {
      this.onPlaybackStateChange?.(false);
    }
  }

  destroy(): void {
    this.destroyed = true;
    this.resumeAttemptedWhileSuspended = false;
    this.nextPlayTime = -1;
    this.suspendedQueue = [];
    for (const entry of this.activeSources) {
      entry.source.onended = null;
      try {
        entry.source.stop();
      } catch {
        // Ignore teardown errors.
      }
      try {
        entry.source.disconnect();
      } catch {
        // Ignore teardown errors.
      }
    }
    this.activeSources.clear();

    for (const fading of this.fadingNodes) {
      if (fading.timer) clearTimeout(fading.timer);
      for (const s of fading.sources) {
        try {
          s.source.stop();
        } catch {
          // Ignore teardown errors.
        }
        try {
          s.source.disconnect();
        } catch {
          // Ignore teardown errors.
        }
      }
      try {
        fading.gain.disconnect();
      } catch {
        // Ignore teardown errors.
      }
    }
    this.fadingNodes = [];

    if (this.epochGain) {
      try {
        this.epochGain.disconnect();
      } catch {
        // Ignore teardown errors.
      }
      this.epochGain = null;
    }
    if (this.masterGain) {
      try {
        this.masterGain.disconnect();
      } catch {
        // Ignore teardown errors.
      }
      this.masterGain = null;
    }
    if (this.audioCtx) {
      const ctx = this.audioCtx;
      this.audioCtx = null;
      try {
        ctx.onstatechange = null;
      } catch {
        // Ignore teardown errors.
      }
      void ctx.close().catch(() => {});
    }
    this.onPlaybackStateChange?.(false);
  }
}
