import { afterEach, describe, expect, it, vi } from "vitest";
import {
  COLABONE_LIVE_FRAME_AUDIO_PCM,
  COLABONE_LIVE_FRAME_FMP4,
  COLABONE_LIVE_FRAME_MIC_PCM,
  COLABONE_LIVE_FRAME_VIDEO,
  COLABONE_LIVE_HEADER_SIZE,
  ColabOneLiveSessionClient,
  ColabOneLiveVideoRenderer,
  colabOneBuildLiveWsUrl,
  colabOneIsFmp4InitSegment,
  colabOneIsH264Keyframe,
  colabOneIsJpegPayload,
  colabOnePackLiveFrame,
  colabOneUnpackLiveFrame,
  colabOneValidateLiveWsUrl,
} from "./colabOneLivePlayer";
import type { ColabOneLiveTelemetry } from "./types";

class FakeWebSocket {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSING = 2;
  static readonly CLOSED = 3;

  readonly url: string;
  binaryType: BinaryType = "blob";
  readyState = FakeWebSocket.CONNECTING;
  sent: Array<string | ArrayBuffer> = [];

  onopen: ((ev: Event) => void) | null = null;
  onmessage: ((ev: MessageEvent) => void) | null = null;
  onerror: ((ev: Event) => void) | null = null;
  onclose: ((ev: CloseEvent) => void) | null = null;

  constructor(url: string) {
    this.url = url;
  }

  send(data: string | ArrayBuffer): void {
    this.sent.push(data);
  }

  close(code = 1000, reason = ""): void {
    this.readyState = FakeWebSocket.CLOSED;
    this.onclose?.({ code, reason } as CloseEvent);
  }

  emitOpen(): void {
    this.readyState = FakeWebSocket.OPEN;
    this.onopen?.({} as Event);
  }

  emitText(obj: Record<string, unknown>): void {
    this.onmessage?.({ data: JSON.stringify(obj) } as MessageEvent);
  }

  emitBinary(buf: ArrayBuffer): void {
    this.onmessage?.({ data: buf } as MessageEvent);
  }

  emitDrop(code = 1006, reason = "network_drop"): void {
    this.readyState = FakeWebSocket.CLOSED;
    this.onclose?.({ code, reason } as CloseEvent);
  }
}

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe("colabOneLivePlayer binary frame codec & URL security", () => {
  it("packs and unpacks exact 13-byte Big-Endian headers with uint64 ptsMs", () => {
    const payload = new Uint8Array([0xde, 0xad, 0xbe, 0xef, 0x42]);
    const ptsMs = 0x1_0000_1234; // > 32-bit to verify 64-bit BE encoding
    const epoch = 0x01020304;
    const packed = colabOnePackLiveFrame(
      COLABONE_LIVE_FRAME_VIDEO,
      epoch,
      ptsMs,
      payload,
    );

    expect(packed.byteLength).toBe(COLABONE_LIVE_HEADER_SIZE + payload.length);
    const raw = new Uint8Array(packed);
    // Exact Big-Endian byte check: type=0x02, epoch=01 02 03 04, pts=00 00 00 01 00 00 12 34
    expect(Array.from(raw.slice(0, 13))).toEqual([
      0x02,
      0x01,
      0x02,
      0x03,
      0x04,
      0x00,
      0x00,
      0x00,
      0x01,
      0x00,
      0x00,
      0x12,
      0x34,
    ]);

    const unpacked = colabOneUnpackLiveFrame(packed);
    expect(unpacked).not.toBeNull();
    expect(unpacked!.frameType).toBe(COLABONE_LIVE_FRAME_VIDEO);
    expect(unpacked!.epoch).toBe(epoch);
    expect(unpacked!.ptsMs).toBe(ptsMs);
    expect(Array.from(unpacked!.payload)).toEqual(Array.from(payload));
  });

  it("returns null for truncated binary buffers", () => {
    expect(colabOneUnpackLiveFrame(new ArrayBuffer(0))).toBeNull();
    expect(colabOneUnpackLiveFrame(new ArrayBuffer(12))).toBeNull();
  });

  it("enforces wss:// or loopback ws:// and forbids tokens in query parameters", () => {
    expect(colabOneValidateLiveWsUrl("wss://live.example.com/_colabone_live/ws")).toBe(
      "wss://live.example.com/_colabone_live/ws",
    );
    expect(colabOneValidateLiveWsUrl("ws://127.0.0.1:8189/_colabone_live/ws")).toBe(
      "ws://127.0.0.1:8189/_colabone_live/ws",
    );
    expect(colabOneValidateLiveWsUrl("ws://localhost:8189/_colabone_live/ws")).toBe(
      "ws://localhost:8189/_colabone_live/ws",
    );
    expect(() =>
      colabOneValidateLiveWsUrl("ws://remote.example.com/_colabone_live/ws"),
    ).toThrow();
    expect(() =>
      colabOneValidateLiveWsUrl("wss://live.example.com/_colabone_live/ws?token=secret"),
    ).toThrow();
    expect(() => colabOneValidateLiveWsUrl("https://live.example.com/ws")).toThrow();

    expect(colabOneBuildLiveWsUrl("https://comfy.example.com")).toBe(
      "wss://comfy.example.com/_colabone_live/ws",
    );
    expect(colabOneBuildLiveWsUrl("http://127.0.0.1:8189")).toBe(
      "ws://127.0.0.1:8189/_colabone_live/ws",
    );
  });

  it("detects Annex-B H.264 IDR keyframes, fMP4 init segments, and JPEG magic headers", () => {
    // 4-byte start code + IDR NAL (type 5 is 0x65)
    expect(colabOneIsH264Keyframe(new Uint8Array([0, 0, 0, 1, 0x65, 0x88]))).toBe(true);
    // 3-byte start code + SPS-only NAL (type 7 is 0x67) is NOT an IDR keyframe
    expect(colabOneIsH264Keyframe(new Uint8Array([0, 0, 1, 0x67, 0x42]))).toBe(false);
    // SPS + PPS + IDR combined IS a keyframe
    expect(
      colabOneIsH264Keyframe(
        new Uint8Array([0, 0, 0, 1, 0x67, 0x42, 0, 0, 1, 0x68, 0xce, 0, 0, 1, 0x65, 0x88]),
      ),
    ).toBe(true);
    // Non-IDR P-frame (type 1 is 0x41)
    expect(colabOneIsH264Keyframe(new Uint8Array([0, 0, 0, 1, 0x41, 0x9a]))).toBe(false);
    // fMP4 init segment (ftyp / moov) vs media segment (moof)
    expect(
      colabOneIsFmp4InitSegment(
        new Uint8Array([0, 0, 0, 12, 0x66, 0x74, 0x79, 0x70, 0x69, 0x73, 0x6f, 0x6d]),
      ),
    ).toBe(true);
    expect(
      colabOneIsFmp4InitSegment(
        new Uint8Array([0, 0, 0, 12, 0x6d, 0x6f, 0x6f, 0x66, 0x00, 0x00, 0x00, 0x00]),
      ),
    ).toBe(false);
    // JPEG SOI
    expect(colabOneIsJpegPayload(new Uint8Array([0xff, 0xd8, 0xff, 0xe0]))).toBe(true);
    expect(colabOneIsJpegPayload(new Uint8Array([0x00, 0x00, 0x00, 0x01]))).toBe(false);
  });
});

describe("ColabOneLiveSessionClient auth, epoch gating, controls & reconnect", () => {
  it("authenticates on first frame, drops stale epoch frames, and reconnects with fresh tokens", async () => {
    vi.useFakeTimers();
    vi.stubGlobal("WebSocket", FakeWebSocket);

    const sockets: FakeWebSocket[] = [];
    let tokenSeq = 0;
    const videoEpochs: number[] = [];
    const audioEpochs: number[] = [];
    const fmp4Epochs: number[] = [];
    const states: string[] = [];
    const transcripts: string[] = [];
    const cards: Array<string | null> = [];
    const bargeIns: number[] = [];

    const client = new ColabOneLiveSessionClient({
      fetchToken: async () => {
        tokenSeq += 1;
        return {
          token: `tok-session-${tokenSeq}`,
          ws_url: "wss://comfy.example.com/_colabone_live/ws",
          codec: "h264",
        };
      },
      webSocketFactory: (url) => {
        const ws = new FakeWebSocket(url);
        sockets.push(ws);
        return ws as unknown as WebSocket;
      },
      initialReconnectDelayMs: 500,
      heartbeatIntervalMs: 0,
      onState: (st) => states.push(st),
      onTranscript: (tr) => transcripts.push(`${tr.role}:${tr.text}`),
      onCard: (card) => cards.push(card ? card.id : null),
      onBargeIn: (ep) => bargeIns.push(ep),
      onVideoFrame: (f) => videoEpochs.push(f.epoch),
      onAudioFrame: (f) => audioEpochs.push(f.epoch),
      onFmp4Chunk: (f) => fmp4Epochs.push(f.epoch),
    });

    await client.connect();
    expect(sockets.length).toBe(1);
    expect(sockets[0].url).toBe("wss://comfy.example.com/_colabone_live/ws");
    expect(sockets[0].url).not.toContain("tok-session");

    sockets[0].emitOpen();
    expect(JSON.parse(sockets[0].sent[0] as string)).toEqual({
      type: "auth",
      token: "tok-session-1",
    });

    sockets[0].emitText({ type: "auth_ok", epoch: 2, state: "IDLE" });
    expect(client.isConnected()).toBe(true);
    expect(client.getCurrentEpoch()).toBe(2);
    expect(states).toEqual(["IDLE"]);

    // Incoming stale epoch 1 frames must be dropped silently!
    sockets[0].emitBinary(
      colabOnePackLiveFrame(COLABONE_LIVE_FRAME_VIDEO, 1, 100, new Uint8Array([1])),
    );
    sockets[0].emitBinary(
      colabOnePackLiveFrame(COLABONE_LIVE_FRAME_AUDIO_PCM, 1, 100, new Uint8Array([2])),
    );
    expect(videoEpochs).toEqual([]);
    expect(audioEpochs).toEqual([]);
    expect(client.getDroppedStaleFrames()).toBe(2);

    // Current epoch 2 frames pass cleanly.
    sockets[0].emitBinary(
      colabOnePackLiveFrame(COLABONE_LIVE_FRAME_VIDEO, 2, 120, new Uint8Array([3])),
    );
    sockets[0].emitBinary(
      colabOnePackLiveFrame(COLABONE_LIVE_FRAME_AUDIO_PCM, 2, 120, new Uint8Array([4])),
    );
    sockets[0].emitBinary(
      colabOnePackLiveFrame(COLABONE_LIVE_FRAME_FMP4, 2, 140, new Uint8Array([5])),
    );
    expect(videoEpochs).toEqual([2]);
    expect(audioEpochs).toEqual([2]);
    expect(fmp4Epochs).toEqual([2]);

    // Manual barge-in bumps epoch immediately to 3 and sends control JSON.
    const nextEp = client.sendBargeIn("user_click");
    expect(nextEp).toBe(3);
    expect(bargeIns).toEqual([3]);
    expect(JSON.parse(sockets[0].sent[1] as string)).toEqual({
      type: "barge_in",
      epoch: 3,
      reason: "user_click",
    });

    // Late epoch 2 video frame after barge-in is dropped.
    sockets[0].emitBinary(
      colabOnePackLiveFrame(COLABONE_LIVE_FRAME_VIDEO, 2, 200, new Uint8Array([6])),
    );
    expect(videoEpochs).toEqual([2]);
    expect(client.getDroppedStaleFrames()).toBe(3);

    // Outbound mic PCM packs 0x01 with epoch 3.
    const mic = new Int16Array([100, -100, 200, -200]);
    expect(client.sendMicPcm(mic, 500)).toBe(true);
    const micPacket = colabOneUnpackLiveFrame(sockets[0].sent[2] as ArrayBuffer);
    expect(micPacket?.frameType).toBe(COLABONE_LIVE_FRAME_MIC_PCM);
    expect(micPacket?.epoch).toBe(3);
    expect(micPacket?.ptsMs).toBe(500);
    expect(micPacket?.payload.byteLength).toBe(8);

    // JSON events (transcript, transcript_delta, asr_final, action, turn_end, product card)
    sockets[0].emitText({ type: "transcript", role: "assistant", text: "欢迎来到直播间" });
    sockets[0].emitText({ type: "transcript_delta", role: "assistant", text: "今天推荐好物" });
    sockets[0].emitText({ type: "asr_final", text: "介绍一下耳机", epoch: 3 });
    sockets[0].emitText({
      type: "card",
      card: { id: "sku-1", name: "星耀耳机", price: "299", highlights: ["降噪"] },
    });
    sockets[0].emitText({ type: "card", product_id: "sku-2", epoch: 3 });
    sockets[0].emitText({ type: "card", action: "clear" });
    sockets[0].emitText({ type: "turn_end", epoch: 3, state: "IDLE" });
    expect(transcripts).toEqual([
      "assistant:欢迎来到直播间",
      "assistant:今天推荐好物",
      "user:介绍一下耳机",
    ]);
    expect(cards).toEqual(["sku-1", "sku-2", null]);

    // Outbound text & action commands matching server/live_server.py
    expect(client.sendTextInput("介绍一下续航", "director")).toBe(true);
    expect(client.sendTriggerAction("action_wave")).toBe(true);
    expect(client.sendSetMode("director", "ptt")).toBe(true);
    expect(client.sendPttState(true)).toBe(true);
    expect(client.sendPttState(false)).toBe(true);
    expect(client.sendSetCard("sku-1")).toBe(true);
    expect(client.sendSwitchEngine({ visual_engine: "musetalk_bank" })).toBe(true);
    expect(JSON.parse(sockets[0].sent[5] as string)).toMatchObject({
      type: "set_mode",
      director_mode: "director",
      mic_mode: "ptt",
    });
    expect(JSON.parse(sockets[0].sent[6] as string)).toMatchObject({
      type: "ptt_start",
      active: true,
    });
    expect(JSON.parse(sockets[0].sent[7] as string)).toMatchObject({
      type: "ptt_end",
      active: false,
    });
    expect(JSON.parse(sockets[0].sent[8] as string)).toMatchObject({
      type: "show_card",
      product_id: "sku-1",
    });

    // Unexpected drop triggers exponential backoff reconnect with fresh token.
    sockets[0].emitDrop();
    expect(client.getConnectionState()).toBe("reconnecting");
    await vi.advanceTimersByTimeAsync(500);
    expect(sockets.length).toBe(2);
    sockets[1].emitOpen();
    expect(JSON.parse(sockets[1].sent[0] as string)).toEqual({
      type: "auth",
      token: "tok-session-2",
    });

    client.disconnect();
    expect(client.getConnectionState()).toBe("disconnected");
  });

  it("aligns barge-in epoch with server echo without double-increment or duplicate callback", async () => {
    vi.stubGlobal("WebSocket", FakeWebSocket);
    const sockets: FakeWebSocket[] = [];
    const bargeIns: number[] = [];
    const states: string[] = [];
    const videoEpochs: number[] = [];

    const client = new ColabOneLiveSessionClient({
      fetchToken: async () => ({
        token: "tok-epoch",
        ws_url: "wss://comfy.example.com/_colabone_live/ws",
      }),
      webSocketFactory: (url) => {
        const ws = new FakeWebSocket(url);
        sockets.push(ws);
        return ws as unknown as WebSocket;
      },
      heartbeatIntervalMs: 0,
      onBargeIn: (ep) => bargeIns.push(ep),
      onState: (st) => states.push(st),
      onVideoFrame: (f) => videoEpochs.push(f.epoch),
    });

    await client.connect();
    sockets[0].emitOpen();
    sockets[0].emitText({ type: "auth_ok", epoch: 1, state: "SPEAKING" });
    expect(client.getCurrentEpoch()).toBe(1);

    // Client sends manual barge-in: local epoch becomes 2 and fires onBargeIn once.
    const nextEp = client.sendBargeIn("user_button");
    expect(nextEp).toBe(2);
    expect(client.getCurrentEpoch()).toBe(2);
    expect(bargeIns).toEqual([2]);

    // Server echoes barge_in at authoritative epoch 2 with state IDLE.
    sockets[0].emitText({ type: "barge_in", epoch: 2, state: "IDLE", reason: "user_button" });
    expect(client.getCurrentEpoch()).toBe(2);
    expect(bargeIns).toEqual([2]);
    expect(states).toEqual(["SPEAKING", "IDLE"]);

    // Subsequent epoch-2 media frames must be accepted cleanly!
    sockets[0].emitBinary(
      colabOnePackLiveFrame(COLABONE_LIVE_FRAME_VIDEO, 2, 300, new Uint8Array([9])),
    );
    expect(videoEpochs).toEqual([2]);
    client.disconnect();
  });

  it("stops reconnecting on terminal 4401 close code and handles pong RTT + missed-pong watchdog", async () => {
    vi.useFakeTimers();
    vi.stubGlobal("WebSocket", FakeWebSocket);

    const sockets: FakeWebSocket[] = [];
    const pongs: Array<{ ts?: number; rttMs?: number }> = [];
    const errors: Array<{ msg: string; code?: string }> = [];

    const client = new ColabOneLiveSessionClient({
      fetchToken: async () => ({
        token: "tok-hb",
        ws_url: "wss://comfy.example.com/_colabone_live/ws",
      }),
      webSocketFactory: (url) => {
        const ws = new FakeWebSocket(url);
        sockets.push(ws);
        return ws as unknown as WebSocket;
      },
      initialReconnectDelayMs: 500,
      heartbeatIntervalMs: 1000,
      onPong: (ts, rttMs) => pongs.push({ ts, rttMs }),
      onError: (msg, code) => errors.push({ msg, code }),
    });

    await client.connect();
    sockets[0].emitOpen();
    sockets[0].emitText({ type: "auth_ok", epoch: 1, state: "IDLE" });

    // Tick 1: sends ping, server replies with pong 42ms later.
    await vi.advanceTimersByTimeAsync(1000);
    const pingMsg = JSON.parse(sockets[0].sent[1] as string);
    expect(pingMsg.type).toBe("ping");
    await vi.advanceTimersByTimeAsync(42);
    sockets[0].emitText({ type: "pong", ts: pingMsg.ts });
    expect(pongs.length).toBe(1);
    expect(pongs[0].rttMs).toBe(42);

    // Silence server: 2 consecutive missed pongs force close & reconnect on the 3rd interval tick.
    await vi.advanceTimersByTimeAsync(1000); // ping 1 (missedPongs = 1)
    await vi.advanceTimersByTimeAsync(1000); // ping 2 (missedPongs = 2)
    await vi.advanceTimersByTimeAsync(1000); // watchdog triggers reconnect
    expect(client.getConnectionState()).toBe("reconnecting");

    await vi.advanceTimersByTimeAsync(500);
    expect(sockets.length).toBe(2);
    sockets[1].emitOpen();
    // Terminal 4401 unauthorized close must NOT reconnect.
    sockets[1].emitDrop(4401, "unauthorized");
    expect(client.getConnectionState()).toBe("error");
    expect(errors.some((e) => e.code === "ws_close_4401")).toBe(true);

    await vi.advanceTimersByTimeAsync(5000);
    expect(sockets.length).toBe(2);
    client.disconnect();
  });

  it("omits fps from telemetry callback when server telemetry does not include numeric fps", async () => {
    vi.stubGlobal("WebSocket", FakeWebSocket);
    const sockets: FakeWebSocket[] = [];
    const received: Array<Partial<ColabOneLiveTelemetry>> = [];

    const client = new ColabOneLiveSessionClient({
      fetchToken: async () => ({
        token: "tok-tel",
        ws_url: "wss://comfy.example.com/_colabone_live/ws",
      }),
      webSocketFactory: (url) => {
        const ws = new FakeWebSocket(url);
        sockets.push(ws);
        return ws as unknown as WebSocket;
      },
      heartbeatIntervalMs: 0,
      onTelemetry: (tel) => received.push(tel),
    });

    await client.connect();
    sockets[0].emitOpen();
    sockets[0].emitText({ type: "auth_ok", epoch: 1, state: "IDLE" });

    // Telemetry without fps must NOT carry fps or missing vram_total_mib keys.
    sockets[0].emitText({
      type: "telemetry",
      ttft_ms: 180,
      ttfa_ms: 340,
      e2e_ms: 490,
      vram_used_mib: 12000,
    });
    expect(received.length).toBe(1);
    expect("fps" in received[0]).toBe(false);
    expect("vram_total_mib" in received[0]).toBe(false);
    expect(received[0].ttft_ms).toBe(180);

    // Partial telemetry WITH numeric fps omits absent latency and VRAM keys instead of nulling them.
    sockets[0].emitText({
      type: "telemetry",
      fps: 24.5,
      ttft_ms: 170,
    });
    expect(received[1].fps).toBe(24.5);
    expect(received[1].ttft_ms).toBe(170);
    expect("ttfa_ms" in received[1]).toBe(false);
    expect("e2e_ms" in received[1]).toBe(false);
    expect("vram_used_mib" in received[1]).toBe(false);
    expect("vram_total_mib" in received[1]).toBe(false);

    client.disconnect();
  });

  it("preserves product_id for local lookup when server sends card: null and clears only on clear action", async () => {
    vi.stubGlobal("WebSocket", FakeWebSocket);
    const sockets: FakeWebSocket[] = [];
    const cards: Array<{ id: string | null; raw?: Record<string, unknown> }> = [];

    const client = new ColabOneLiveSessionClient({
      fetchToken: async () => ({
        token: "tok-card",
        ws_url: "wss://comfy.example.com/_colabone_live/ws",
      }),
      webSocketFactory: (url) => {
        const ws = new FakeWebSocket(url);
        sockets.push(ws);
        return ws as unknown as WebSocket;
      },
      heartbeatIntervalMs: 0,
      onCard: (card, raw) => cards.push({ id: card ? card.id : null, raw }),
    });

    await client.connect();
    sockets[0].emitOpen();
    sockets[0].emitText({ type: "auth_ok", epoch: 1, state: "IDLE" });

    // Server sends product_id with card: null when product metadata must be resolved locally.
    sockets[0].emitText({
      type: "card",
      product_id: "sku-2",
      card: null,
      epoch: 3,
    });
    // Explicit clear action clears the card cleanly.
    sockets[0].emitText({
      type: "card",
      action: "clear",
      product_id: "",
      card: null,
      epoch: 4,
    });

    expect(cards.map((c) => c.id)).toEqual(["sku-2", null]);
    client.disconnect();
  });

  it("adopts lower server epoch on auth_ok after remote server restart and accepts new frames", async () => {
    vi.useFakeTimers();
    vi.stubGlobal("WebSocket", FakeWebSocket);
    const sockets: FakeWebSocket[] = [];
    const videoEpochs: number[] = [];
    const audioEpochs: number[] = [];

    const client = new ColabOneLiveSessionClient({
      fetchToken: async () => ({
        token: "tok-restart",
        ws_url: "wss://comfy.example.com/_colabone_live/ws",
      }),
      webSocketFactory: (url) => {
        const ws = new FakeWebSocket(url);
        sockets.push(ws);
        return ws as unknown as WebSocket;
      },
      initialReconnectDelayMs: 200,
      heartbeatIntervalMs: 0,
      onVideoFrame: (f) => videoEpochs.push(f.epoch),
      onAudioFrame: (f) => audioEpochs.push(f.epoch),
    });

    await client.connect();
    sockets[0].emitOpen();
    sockets[0].emitText({ type: "auth_ok", epoch: 5, state: "IDLE" });
    expect(client.getCurrentEpoch()).toBe(5);

    // Server drops and restarts from epoch 0.
    sockets[0].emitDrop(1006, "server_restart");
    await vi.advanceTimersByTimeAsync(250);
    expect(sockets.length).toBe(2);
    sockets[1].emitOpen();
    sockets[1].emitText({ type: "auth_ok", epoch: 0, state: "IDLE" });
    expect(client.getCurrentEpoch()).toBe(0);

    // Epoch 0 media frames must NOT be dropped as stale.
    sockets[1].emitBinary(
      colabOnePackLiveFrame(COLABONE_LIVE_FRAME_VIDEO, 0, 100, new Uint8Array([7])),
    );
    sockets[1].emitBinary(
      colabOnePackLiveFrame(COLABONE_LIVE_FRAME_AUDIO_PCM, 0, 100, new Uint8Array([8, 9])),
    );
    expect(videoEpochs).toEqual([0]);
    expect(audioEpochs).toEqual([0]);
    client.disconnect();
  });

  it("polls gracefully when canvas_ready is false and emits canvas_not_ready only when retries stop", async () => {
    vi.useFakeTimers();
    vi.stubGlobal("WebSocket", FakeWebSocket);
    const sockets: FakeWebSocket[] = [];
    const errors: Array<{ msg: string; code?: string }> = [];
    const connStates: Array<{ state: string; detail?: string }> = [];
    let attempt = 0;

    const client = new ColabOneLiveSessionClient({
      fetchToken: async () => {
        attempt += 1;
        if (attempt < 3) {
          return {
            token: `tok-${attempt}`,
            ws_url: "",
            canvas_ready: false,
          };
        }
        return {
          token: `tok-${attempt}`,
          ws_url: "wss://comfy.example.com/_colabone_live/ws",
          canvas_ready: true,
        };
      },
      webSocketFactory: (url) => {
        const ws = new FakeWebSocket(url);
        sockets.push(ws);
        return ws as unknown as WebSocket;
      },
      initialReconnectDelayMs: 200,
      maxReconnectAttempts: 4,
      heartbeatIntervalMs: 0,
      onConnectionState: (state, detail) => connStates.push({ state, detail }),
      onError: (msg, code) => errors.push({ msg, code }),
    });

    await client.connect();
    expect(sockets.length).toBe(0);
    expect(errors).toEqual([]);
    expect(client.getConnectionState()).toBe("reconnecting");
    expect(connStates[connStates.length - 1]?.detail).toBe("canvas_starting");

    await vi.advanceTimersByTimeAsync(250);
    expect(sockets.length).toBe(0);
    expect(errors).toEqual([]);

    await vi.advanceTimersByTimeAsync(500);
    expect(sockets.length).toBe(1);
    sockets[0].emitOpen();
    sockets[0].emitText({ type: "auth_ok", epoch: 0, state: "IDLE" });
    expect(client.isConnected()).toBe(true);
    expect(errors).toEqual([]);
    client.disconnect();

    // When shouldRetryCanvasNotReady returns false, emit canvas_not_ready immediately.
    const noRetryErrors: Array<{ msg: string; code?: string }> = [];
    const noRetryClient = new ColabOneLiveSessionClient({
      fetchToken: async () => ({
        token: "tok-no-retry",
        ws_url: "",
        canvas_ready: false,
      }),
      shouldRetryCanvasNotReady: () => false,
      heartbeatIntervalMs: 0,
      onError: (msg, code) => noRetryErrors.push({ msg, code }),
    });
    await noRetryClient.connect();
    expect(noRetryClient.getConnectionState()).toBe("error");
    expect(noRetryErrors).toEqual([
      { msg: "Tunnel not ready", code: "canvas_not_ready" },
    ]);
  });

  it("resets reconnectAttempt on public connect() and guards sendBargeIn() when disconnected", async () => {
    vi.useFakeTimers();
    vi.stubGlobal("WebSocket", FakeWebSocket);

    let fetchCalls = 0;
    const states: string[] = [];
    const errors: Array<{ msg: string; code?: string }> = [];
    const bargeIns: number[] = [];
    const sockets: FakeWebSocket[] = [];

    const client = new ColabOneLiveSessionClient({
      fetchToken: async () => {
        fetchCalls += 1;
        throw new Error(`token_err_${fetchCalls}`);
      },
      webSocketFactory: (url) => {
        const ws = new FakeWebSocket(url);
        sockets.push(ws);
        return ws as unknown as WebSocket;
      },
      maxReconnectAttempts: 2,
      initialReconnectDelayMs: 100,
      maxReconnectDelayMs: 200,
      heartbeatIntervalMs: 0,
      onConnectionState: (state) => states.push(state),
      onError: (msg, code) => errors.push({ msg, code }),
      onBargeIn: (epoch) => bargeIns.push(epoch),
    });

    // Disconnected sendBargeIn must NOT mutate epoch or trigger callback.
    expect(client.getCurrentEpoch()).toBe(0);
    expect(client.sendBargeIn("offline_click")).toBe(0);
    expect(client.getCurrentEpoch()).toBe(0);
    expect(bargeIns).toEqual([]);

    // First manual connect() fails and exhausts 2 reconnect attempts (3 total fetches).
    await client.connect();
    await vi.advanceTimersByTimeAsync(150);
    await vi.advanceTimersByTimeAsync(250);
    expect(fetchCalls).toBe(3);
    expect(client.getConnectionState()).toBe("error");
    expect(errors.some((e) => e.code === "reconnect_exhausted")).toBe(true);

    // Calling public connect() again must reset reconnectAttempt to 0 and grant a full budget.
    states.length = 0;
    errors.length = 0;
    await client.connect();
    expect(states[0]).toBe("connecting");
    await vi.advanceTimersByTimeAsync(150);
    await vi.advanceTimersByTimeAsync(250);
    expect(fetchCalls).toBe(6);
    expect(errors.some((e) => e.code === "reconnect_exhausted")).toBe(true);
    expect(sockets.length).toBe(0);
    client.disconnect();
  });
});

describe("ColabOneLiveVideoRenderer WebCodecs & JPEG fallback", () => {
  it("decodes H.264 via WebCodecs with avc1.42E028 default, only from keyframe, and closes VideoFrame", () => {
    const drawImage = vi.fn();
    const canvas = {
      width: 512,
      height: 512,
      getContext: () => ({ drawImage }),
    } as unknown as HTMLCanvasElement;

    const frameClose = vi.fn();
    let capturedOutput: ((frame: VideoFrame) => void) | null = null;
    const decodeSpy = vi.fn();
    const configureSpy = vi.fn();
    const closeSpy = vi.fn();

    class MockEncodedVideoChunk {
      readonly type: string;
      readonly timestamp: number;
      readonly data: Uint8Array;
      constructor(init: { type: string; timestamp: number; data: Uint8Array }) {
        this.type = init.type;
        this.timestamp = init.timestamp;
        this.data = init.data;
      }
    }

    class MockVideoDecoder {
      state: CodecState = "unconfigured";
      constructor(init: { output: (f: VideoFrame) => void }) {
        capturedOutput = init.output;
      }
      configure(cfg: VideoDecoderConfig): void {
        configureSpy(cfg);
        this.state = "configured";
      }
      decode(chunk: EncodedVideoChunk): void {
        decodeSpy(chunk);
        capturedOutput?.({
          displayWidth: 720,
          displayHeight: 1280,
          codedWidth: 720,
          codedHeight: 1280,
          close: frameClose,
        } as unknown as VideoFrame);
      }
      reset(): void {
        this.state = "unconfigured";
      }
      close(): void {
        this.state = "closed";
        closeSpy();
      }
    }

    vi.stubGlobal("EncodedVideoChunk", MockEncodedVideoChunk);
    vi.stubGlobal("VideoDecoder", MockVideoDecoder);

    const rendered: Array<{ width: number; height: number; fps: number; mode: string }> = [];
    const renderer = new ColabOneLiveVideoRenderer({
      canvas,
      codec: "h264",
      onFrameRendered: (info) => rendered.push(info),
    });

    expect(renderer.getH264CodecString()).toBe("avc1.42E028");

    // Delta frame before first keyframe must be skipped.
    renderer.pushVideoFrame(new Uint8Array([0, 0, 0, 1, 0x41, 0x10]), 10);
    expect(decodeSpy).not.toHaveBeenCalled();

    // Keyframe decodes, resizes canvas, draws, and closes VideoFrame.
    renderer.pushVideoFrame(new Uint8Array([0, 0, 0, 1, 0x65, 0x20]), 40);
    expect(configureSpy).toHaveBeenCalledWith(
      expect.objectContaining({ codec: "avc1.42E028" }),
    );
    expect(decodeSpy).toHaveBeenCalledTimes(1);
    expect(canvas.width).toBe(720);
    expect(canvas.height).toBe(1280);
    expect(drawImage).toHaveBeenCalledTimes(1);
    expect(frameClose).toHaveBeenCalledTimes(1);
    expect(rendered[0]).toMatchObject({ width: 720, height: 1280, mode: "webcodecs" });
    // Burst frames should report sliding 1s window count (never 1000 FPS spikes).
    expect(rendered[0].fps).toBe(1);

    renderer.destroy();
    expect(closeSpy).toHaveBeenCalledTimes(1);
  });

  it("renders JPEG frames via createImageBitmap, guards post-destroy errors, and decays FPS to 0", async () => {
    vi.useFakeTimers();
    const drawImage = vi.fn();
    const canvas = {
      width: 320,
      height: 320,
      getContext: () => ({ drawImage }),
    } as unknown as HTMLCanvasElement;

    const bitmapClose = vi.fn();
    let rejectLate!: (err: Error) => void;
    let callCount = 0;
    vi.stubGlobal(
      "createImageBitmap",
      vi.fn(() => {
        callCount += 1;
        if (callCount === 1) {
          return Promise.resolve({
            width: 540,
            height: 960,
            close: bitmapClose,
          });
        }
        return new Promise((_, reject) => {
          rejectLate = reject;
        });
      }),
    );

    const fpsUpdates: number[] = [];
    const onError = vi.fn();
    const renderer = new ColabOneLiveVideoRenderer({
      canvas,
      codec: "jpeg",
      onFrameRendered: (info) => fpsUpdates.push(info.fps),
      onError,
    });

    renderer.pushVideoFrame(new Uint8Array([0xff, 0xd8, 0xff, 0xd9]), 100);
    await Promise.resolve();
    await Promise.resolve();

    expect(canvas.width).toBe(540);
    expect(canvas.height).toBe(960);
    expect(drawImage).toHaveBeenCalledTimes(1);
    expect(bitmapClose).toHaveBeenCalledTimes(1);
    expect(renderer.getRenderedFrameCount()).toBe(1);
    expect(fpsUpdates).toEqual([1]);

    // After 2 seconds of no frames, FPS decays cleanly to 0.
    await vi.advanceTimersByTimeAsync(2000);
    expect(fpsUpdates[fpsUpdates.length - 1]).toBe(0);

    // Late rejection after destroy must NOT invoke onError.
    renderer.pushVideoFrame(new Uint8Array([0xff, 0xd8, 0xff, 0xd9]), 200);
    renderer.destroy();
    rejectLate(new Error("late decode fail"));
    await Promise.resolve();
    await Promise.resolve();
    expect(onError).not.toHaveBeenCalled();
  });

  it("caches SPS/PPS for next IDR, applies decodeQueueSize backpressure, and closes decoder on sync decode error", () => {
    const drawImage = vi.fn();
    const canvas = {
      width: 512,
      height: 512,
      getContext: () => ({ drawImage }),
    } as unknown as HTMLCanvasElement;

    const decodedChunks: Array<{ type: string; data: Uint8Array }> = [];
    const closeSpy = vi.fn();
    let mockQueueSize = 0;
    let shouldThrowOnDecode = false;
    let decoderCount = 0;

    class MockEncodedVideoChunk {
      readonly type: string;
      readonly timestamp: number;
      readonly data: Uint8Array;
      constructor(init: { type: string; timestamp: number; data: Uint8Array }) {
        this.type = init.type;
        this.timestamp = init.timestamp;
        this.data = init.data;
      }
    }

    class MockVideoDecoder {
      state: CodecState = "unconfigured";
      get decodeQueueSize(): number {
        return mockQueueSize;
      }
      constructor(_init: { output: (f: VideoFrame) => void }) {
        decoderCount += 1;
      }
      configure(_cfg: VideoDecoderConfig): void {
        this.state = "configured";
      }
      decode(chunk: EncodedVideoChunk): void {
        if (shouldThrowOnDecode) {
          throw new Error("sync decode failure");
        }
        const c = chunk as unknown as MockEncodedVideoChunk;
        decodedChunks.push({ type: c.type, data: new Uint8Array(c.data) });
      }
      reset(): void {
        this.state = "unconfigured";
      }
      close(): void {
        this.state = "closed";
        closeSpy();
      }
    }

    vi.stubGlobal("EncodedVideoChunk", MockEncodedVideoChunk);
    vi.stubGlobal("VideoDecoder", MockVideoDecoder);

    const onError = vi.fn();
    const renderer = new ColabOneLiveVideoRenderer({
      canvas,
      codec: "h264",
      onError,
    });

    const sps = new Uint8Array([0, 0, 0, 1, 0x67, 0x42, 0xe0, 0x28]);
    const pps = new Uint8Array([0, 0, 0, 1, 0x68, 0xce, 0x38, 0x80]);
    const idr = new Uint8Array([0, 0, 0, 1, 0x65, 0x88, 0x01]);
    const delta = new Uint8Array([0, 0, 0, 1, 0x41, 0x9a, 0x02]);

    // Separate SPS and PPS are cached without feeding VideoDecoder directly.
    renderer.pushVideoFrame(sps, 0);
    renderer.pushVideoFrame(pps, 0);
    expect(decodedChunks.length).toBe(0);

    // Next IDR receives prepended SPS + PPS + IDR bytes.
    renderer.pushVideoFrame(idr, 40);
    expect(decodedChunks.length).toBe(1);
    expect(decodedChunks[0].type).toBe("key");
    expect(Array.from(decodedChunks[0].data)).toEqual([
      ...sps,
      ...pps,
      ...idr,
    ]);

    // Backpressure: decodeQueueSize > 3 drops delta and re-arms waitingForKeyframe.
    mockQueueSize = 4;
    renderer.pushVideoFrame(delta, 80);
    expect(decodedChunks.length).toBe(1);
    mockQueueSize = 0;
    renderer.pushVideoFrame(delta, 120);
    expect(decodedChunks.length).toBe(1);

    // Subsequent IDR resumes decoding cleanly.
    renderer.pushVideoFrame(idr, 160);
    expect(decodedChunks.length).toBe(2);

    // Synchronous decode error must close the decoder and allow clean recreation on next IDR.
    shouldThrowOnDecode = true;
    renderer.pushVideoFrame(delta, 200);
    expect(closeSpy).toHaveBeenCalledTimes(1);
    expect(onError).toHaveBeenCalledTimes(1);

    shouldThrowOnDecode = false;
    renderer.pushVideoFrame(delta, 240);
    expect(decodedChunks.length).toBe(2); // Still waiting for keyframe
    renderer.pushVideoFrame(idr, 280);
    expect(decoderCount).toBe(2);
    expect(decodedChunks.length).toBe(3);

    renderer.destroy();
  });

  it("recovers from MSE QuotaExceededError, pins init segment across overflow, resets epoch, and reinits on codec change", () => {
    const drawImage = vi.fn();
    const canvas = {
      width: 512,
      height: 512,
      getContext: () => ({ drawImage }),
    } as unknown as HTMLCanvasElement;

    const makeBox = (type: string, tag: number) =>
      new Uint8Array([
        0,
        0,
        0,
        12,
        type.charCodeAt(0),
        type.charCodeAt(1),
        type.charCodeAt(2),
        type.charCodeAt(3),
        tag,
        0,
        0,
        0,
      ]);

    const initSeg = makeBox("ftyp", 0xaa);

    let throwQuotaOnce = false;
    let supportedMime = `video/mp4; codecs="avc1.42E028"`;

    class MockSourceBuffer {
      mode = "segments";
      updating = false;
      appended: Uint8Array[] = [];
      removed: Array<[number, number]> = [];
      abortCalls = 0;
      private listeners = new Map<string, Array<() => void>>();
      bufferedRanges: Array<[number, number]> = [];

      get buffered(): TimeRanges {
        const ranges = this.bufferedRanges;
        return {
          length: ranges.length,
          start: (i: number) => ranges[i][0],
          end: (i: number) => ranges[i][1],
        };
      }

      addEventListener(ev: string, cb: () => void): void {
        const list = this.listeners.get(ev) ?? [];
        list.push(cb);
        this.listeners.set(ev, list);
      }

      emitUpdateEnd(): void {
        this.updating = false;
        for (const cb of this.listeners.get("updateend") ?? []) {
          cb();
        }
      }

      abort(): void {
        this.abortCalls += 1;
        this.updating = false;
      }

      appendBuffer(data: BufferSource): void {
        if (throwQuotaOnce) {
          throwQuotaOnce = false;
          const err = new Error("SourceBuffer full");
          err.name = "QuotaExceededError";
          throw err;
        }
        const bytes =
          data instanceof Uint8Array
            ? new Uint8Array(data)
            : new Uint8Array(data as ArrayBuffer);
        this.appended.push(bytes);
        this.updating = true;
      }

      remove(start: number, end: number): void {
        this.removed.push([start, end]);
        this.bufferedRanges = [];
        this.updating = true;
      }
    }

    const createdMediaSources: MockMediaSource[] = [];
    class MockMediaSource {
      static isTypeSupported(mime: string): boolean {
        return mime === supportedMime;
      }
      readyState: ReadyState = "closed";
      addedMimes: string[] = [];
      sourceBuffers: MockSourceBuffer[] = [];
      private listeners = new Map<string, Array<() => void>>();

      constructor() {
        createdMediaSources.push(this);
      }

      addEventListener(ev: string, cb: () => void): void {
        const list = this.listeners.get(ev) ?? [];
        list.push(cb);
        this.listeners.set(ev, list);
      }

      addSourceBuffer(mime: string): SourceBuffer {
        this.addedMimes.push(mime);
        const sb = new MockSourceBuffer();
        this.sourceBuffers.push(sb);
        return sb as unknown as SourceBuffer;
      }

      emitSourceOpen(): void {
        this.readyState = "open";
        for (const cb of this.listeners.get("sourceopen") ?? []) {
          cb();
        }
      }
    }

    const createObjectURL = vi.fn(() => `blob:mse-${createdMediaSources.length}`);
    const revokeObjectURL = vi.fn();
    vi.stubGlobal("MediaSource", MockMediaSource);
    vi.stubGlobal("URL", { createObjectURL, revokeObjectURL });
    vi.stubGlobal("requestAnimationFrame", undefined);

    const videoEl = {
      src: "",
      muted: false,
      playsInline: false,
      currentTime: 1,
      readyState: 0,
      videoWidth: 0,
      videoHeight: 0,
      play: vi.fn(() => Promise.resolve()),
      pause: vi.fn(),
      removeAttribute: vi.fn(),
      load: vi.fn(),
    } as unknown as HTMLVideoElement;

    const onError = vi.fn();
    const renderer = new ColabOneLiveVideoRenderer({
      canvas,
      videoElement: videoEl,
      codec: "fmp4",
      onError,
    });

    // 1. Init segment pinned even when 66 (> MAX_MSE_QUEUE_CHUNKS=60) media chunks overflow queue before sourceopen.
    renderer.pushFmp4Chunk(initSeg);
    for (let i = 1; i <= 66; i += 1) {
      renderer.pushFmp4Chunk(makeBox("moof", i));
    }
    expect(createdMediaSources.length).toBe(1);
    createdMediaSources[0].emitSourceOpen();
    const sb = createdMediaSources[0].sourceBuffers[0];
    expect(sb.appended.length).toBe(1);
    expect(Array.from(sb.appended[0])).toEqual(Array.from(initSeg));

    // 2. QuotaExceededError on media append re-unshifts chunk, forces eviction below 10s threshold, and resumes on updateend.
    sb.bufferedRanges = [[0, 4]];
    throwQuotaOnce = true;
    sb.emitUpdateEnd();
    expect(onError).not.toHaveBeenCalled();
    expect(sb.removed.length).toBe(1);
    expect(sb.removed[0][0]).toBe(0);
    // Next updateend after forced removal retries the exact front media chunk (tag 7, since 1..6 were shifted out).
    sb.emitUpdateEnd();
    expect(sb.appended.length).toBe(2);
    expect(sb.appended[1][8]).toBe(7);

    // 3. resetEpoch() aborts, removes full range, re-prepends cached init segment, and seeks to live edge.
    sb.bufferedRanges = [[0, 6]];
    videoEl.currentTime = 1;
    renderer.resetEpoch();
    expect(sb.abortCalls).toBe(1);
    expect(sb.removed[sb.removed.length - 1]).toEqual([0, Infinity]);
    // Removal finishes -> cached init segment is re-appended automatically.
    sb.emitUpdateEnd();
    expect(Array.from(sb.appended[sb.appended.length - 1])).toEqual(
      Array.from(initSeg),
    );
    // New media data arrives and finishes appending -> seeks videoElement to live edge.
    renderer.pushFmp4Chunk(makeBox("moof", 99));
    sb.bufferedRanges = [[6, 9]];
    sb.emitUpdateEnd();
    expect(videoEl.currentTime).toBeCloseTo(8.95, 2);

    // 4. setCodec() with new h264CodecString tears down MSE, resets mseFailed, and reinitializes.
    supportedMime = `video/mp4; codecs="avc1.640028"`;
    renderer.setCodec("fmp4", "avc1.640028");
    expect(createdMediaSources.length).toBe(2);
    createdMediaSources[1].emitSourceOpen();
    expect(createdMediaSources[1].addedMimes).toEqual([
      `video/mp4; codecs="avc1.640028"`,
    ]);
    expect(onError).not.toHaveBeenCalled();

    // 5. Encoder-name telemetry ("h264_nvenc" / "libx264") must NOT tear down active fmp4 MSE,
    // and resetEpoch({ preserveMseStream: true }) preserves MSE without calling remove(0, Infinity).
    const sb2 = createdMediaSources[1].sourceBuffers[0];
    sb2.bufferedRanges = [[0, 4]];
    const removesBefore = sb2.removed.length;
    renderer.setCodec("h264_nvenc");
    renderer.setCodec("libx264");
    expect(createdMediaSources.length).toBe(2);
    renderer.resetEpoch({ preserveMseStream: true });
    expect(sb2.removed.length).toBe(removesBefore);

    // 6. Split ftyp + moov init chunks are merged before appending to MSE.
    const splitFtyp = makeBox("ftyp", 0x11);
    const splitMoov = makeBox("moov", 0x22);
    const renderer2 = new ColabOneLiveVideoRenderer({
      canvas,
      videoElement: videoEl,
      codec: "fmp4",
      h264CodecString: "avc1.640028",
      onError,
    });
    renderer2.pushFmp4Chunk(splitFtyp);
    renderer2.pushFmp4Chunk(splitMoov);
    createdMediaSources[2].emitSourceOpen();
    const sb3 = createdMediaSources[2].sourceBuffers[0];
    expect(sb3.appended.length).toBe(1);
    expect(Array.from(sb3.appended[0])).toEqual([
      ...Array.from(splitFtyp),
      ...Array.from(splitMoov),
    ]);

    renderer2.destroy();
    renderer.destroy();
  });
});
