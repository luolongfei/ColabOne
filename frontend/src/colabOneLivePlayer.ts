import type {
  ColabOneLiveCompanionStatus,
  ColabOneLiveDirectorMode,
  ColabOneLiveDirectorState,
  ColabOneLiveMicMode,
  ColabOneLiveProductItem,
  ColabOneLiveSessionTokenResult,
  ColabOneLiveTelemetry,
  ColabOneLiveTtsEngine,
  ColabOneLiveVisualEngine,
} from "./types";

export const COLABONE_LIVE_FRAME_MIC_PCM = 0x01;
export const COLABONE_LIVE_FRAME_VIDEO = 0x02;
export const COLABONE_LIVE_FRAME_AUDIO_PCM = 0x03;
export const COLABONE_LIVE_FRAME_FMP4 = 0x04;
export const COLABONE_LIVE_FRAME_CAMERA_JPEG = 0x05;
export const COLABONE_LIVE_HEADER_SIZE = 13;

export interface ColabOneLiveBinaryFrame {
  frameType: number;
  epoch: number;
  ptsMs: number;
  payload: Uint8Array;
}

const UINT32_MOD = 0x100000000;
const TERMINAL_WS_CLOSE_CODES = new Set([1000, 1008, 4401, 4403]);
const DEFAULT_MAX_RECONNECT_ATTEMPTS = 6;
const DEFAULT_H264_CODEC_STRING = "avc1.42E028";
const MAX_MSE_QUEUE_CHUNKS = 60;
const MSE_EVICT_THRESHOLD_SEC = 10;
const FPS_WINDOW_MS = 1000;
const FPS_STALE_DECAY_MS = 1500;

/**
 * Packs a 13-byte Big-Endian binary frame matching `common/live_studio.py`:
 * [1B frameType][4B uint32 BE epoch][8B uint64 BE ptsMs][payload...]
 */
export function colabOnePackLiveFrame(
  frameType: number,
  epoch: number,
  ptsMs: number,
  payload: Uint8Array,
): ArrayBuffer {
  const safePts =
    Number.isFinite(ptsMs) && ptsMs > 0 ? Math.floor(ptsMs) : 0;
  const hi = Math.floor(safePts / UINT32_MOD) >>> 0;
  const lo = safePts >>> 0;
  const buf = new ArrayBuffer(COLABONE_LIVE_HEADER_SIZE + payload.byteLength);
  const view = new DataView(buf);
  view.setUint8(0, frameType & 0xff);
  view.setUint32(1, epoch >>> 0, false);
  view.setUint32(5, hi, false);
  view.setUint32(9, lo, false);
  if (payload.byteLength > 0) {
    new Uint8Array(buf, COLABONE_LIVE_HEADER_SIZE).set(payload);
  }
  return buf;
}

/**
 * Unpacks a 13-byte Big-Endian binary frame. Returns null if truncated.
 */
export function colabOneUnpackLiveFrame(
  buffer: ArrayBuffer,
): ColabOneLiveBinaryFrame | null {
  if (!buffer || buffer.byteLength < COLABONE_LIVE_HEADER_SIZE) {
    return null;
  }
  const view = new DataView(buffer);
  const frameType = view.getUint8(0);
  const epoch = view.getUint32(1, false);
  const hi = view.getUint32(5, false);
  const lo = view.getUint32(9, false);
  const ptsMs = hi * UINT32_MOD + lo;
  const payload = new Uint8Array(buffer, COLABONE_LIVE_HEADER_SIZE);
  return { frameType, epoch, ptsMs, payload };
}

function isLoopbackHost(hostname: string): boolean {
  const h = hostname.trim().toLowerCase().replace(/^\[|\]$/g, "");
  return h === "127.0.0.1" || h === "localhost" || h === "::1";
}

/**
 * Validates that a Live Studio WebSocket URL uses `wss:` (or loopback `ws:`)
 * and never embeds authentication tokens in the query string.
 */
export function colabOneValidateLiveWsUrl(rawUrl: string): string {
  const trimmed = String(rawUrl || "").trim();
  if (!trimmed) {
    throw new Error("Live Studio WebSocket URL is empty");
  }
  let parsed: URL;
  try {
    parsed = new URL(trimmed);
  } catch {
    throw new Error(`Invalid Live Studio WebSocket URL: ${trimmed}`);
  }
  if (parsed.protocol === "wss:") {
    // Allowed for all valid hosts.
  } else if (parsed.protocol === "ws:") {
    const isSameOriginProxy =
      typeof window !== "undefined" &&
      window.location?.protocol === "http:" &&
      Boolean(window.location?.host) &&
      parsed.host === window.location.host &&
      parsed.pathname === "/api/live/ws" &&
      !parsed.username &&
      !parsed.password &&
      !parsed.search &&
      !parsed.hash;
    if (!isLoopbackHost(parsed.hostname) && !isSameOriginProxy) {
      throw new Error(
        "Plain ws:// is only permitted for loopback hosts; use wss:// for remote tunnels",
      );
    }
  } else {
    throw new Error(`Unsupported WebSocket protocol: ${parsed.protocol}`);
  }
  for (const key of parsed.searchParams.keys()) {
    if (key.toLowerCase().includes("token") || key.toLowerCase() === "auth") {
      throw new Error(
        "Live Studio WebSocket URL must not carry authentication tokens in query parameters",
      );
    }
  }
  return parsed.toString();
}

/**
 * Builds `wss://<domain>/_colabone_live/ws` from a tunnel URL or bare hostname.
 */
export function colabOneBuildLiveWsUrl(
  canvasUrlOrDomain: string,
  wsPath = "/_colabone_live/ws",
): string {
  const raw = String(canvasUrlOrDomain || "").trim();
  if (!raw) return "";
  const normalizedPath = wsPath.startsWith("/") ? wsPath : `/${wsPath}`;
  if (raw.startsWith("ws://") || raw.startsWith("wss://")) {
    const parsed = new URL(raw);
    if (!parsed.pathname || parsed.pathname === "/") {
      parsed.pathname = normalizedPath;
    }
    parsed.search = "";
    return colabOneValidateLiveWsUrl(parsed.toString());
  }
  const withScheme =
    raw.startsWith("http://") || raw.startsWith("https://")
      ? raw
      : `https://${raw}`;
  const parsed = new URL(withScheme);
  const wsProto =
    parsed.protocol === "http:" && isLoopbackHost(parsed.hostname)
      ? "ws:"
      : "wss:";
  return colabOneValidateLiveWsUrl(
    `${wsProto}//${parsed.host}${normalizedPath}`,
  );
}

export type ColabOneLiveConnectionState =
  | "disconnected"
  | "connecting"
  | "authenticating"
  | "connected"
  | "reconnecting"
  | "error";

export interface ColabOneLiveTranscriptEvent {
  id: string;
  role: "user" | "assistant" | "director" | "system";
  text: string;
  isFinal: boolean;
  isDelta?: boolean;
  epoch: number;
  ts: number;
}

export interface ColabOneLiveSessionClientOptions {
  fetchToken: () => Promise<ColabOneLiveSessionTokenResult>;
  shouldRetryCanvasNotReady?: () => boolean;
  webSocketFactory?: (url: string) => WebSocket;
  reconnect?: boolean;
  maxReconnectAttempts?: number;
  initialReconnectDelayMs?: number;
  maxReconnectDelayMs?: number;
  heartbeatIntervalMs?: number;
  onConnectionState?: (
    state: ColabOneLiveConnectionState,
    detail?: string,
  ) => void;
  onAuthOk?: (payload: Record<string, unknown>) => void;
  onState?: (
    state: ColabOneLiveDirectorState,
    detail?: Record<string, unknown>,
  ) => void;
  onAction?: (actionId: string, detail?: Record<string, unknown>) => void;
  onBargeIn?: (epoch: number, reason?: string) => void;
  onTranscript?: (entry: ColabOneLiveTranscriptEvent) => void;
  onCard?: (
    card: ColabOneLiveProductItem | null,
    raw?: Record<string, unknown>,
  ) => void;
  onTelemetry?: (telemetry: Partial<ColabOneLiveTelemetry>) => void;
  onCompanionStatus?: (
    status: ColabOneLiveCompanionStatus,
    isInitial?: boolean,
  ) => void;
  onSystemNotice?: (text: string, code?: string) => void;
  onVideoFrame?: (frame: ColabOneLiveBinaryFrame) => void;
  onAudioFrame?: (frame: ColabOneLiveBinaryFrame) => void;
  onFmp4Chunk?: (frame: ColabOneLiveBinaryFrame) => void;
  onError?: (message: string, code?: string) => void;
  onPong?: (ts?: number, rttMs?: number) => void;
}

const VALID_DIRECTOR_STATES = new Set<ColabOneLiveDirectorState>([
  "IDLE",
  "LISTENING",
  "THINKING",
  "SPEAKING",
  "PERFORMING",
]);

function normalizeDirectorState(raw: unknown): ColabOneLiveDirectorState {
  const upper = String(raw || "IDLE").toUpperCase() as ColabOneLiveDirectorState;
  return VALID_DIRECTOR_STATES.has(upper) ? upper : "IDLE";
}

let colabOneLiveClientUidSeq = 0;

/**
 * Full-duplex WebSocket session client for ColabOne Live Studio.
 * Authenticates via first-frame JSON `{type:"auth", token}` (never URL query),
 * enforces monotonic epoch filtering on downlink media frames, monitors pong
 * heartbeats, and supports bounded exponential-backoff reconnection.
 */
export class ColabOneLiveSessionClient {
  private readonly clientUid = ++colabOneLiveClientUidSeq;
  private readonly options: ColabOneLiveSessionClientOptions;
  private readonly shouldReconnect: boolean;
  private readonly maxReconnectAttempts: number;
  private readonly initialReconnectDelayMs: number;
  private readonly maxReconnectDelayMs: number;
  private readonly heartbeatIntervalMs: number;

  private ws: WebSocket | null = null;
  private connectionState: ColabOneLiveConnectionState = "disconnected";
  private authenticated = false;
  private manualDisconnect = false;
  private reconnectAttempt = 0;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private heartbeatTimer: ReturnType<typeof setInterval> | null = null;
  private connectGen = 0;
  private missedPongs = 0;
  private lastPingSentAt: number | null = null;

  private currentEpoch = 0;
  private droppedStaleFrames = 0;
  private negotiatedCodec = "h264";
  private transcriptSeq = 0;

  constructor(options: ColabOneLiveSessionClientOptions) {
    this.options = options;
    this.shouldReconnect = options.reconnect !== false;
    this.maxReconnectAttempts =
      options.maxReconnectAttempts ?? DEFAULT_MAX_RECONNECT_ATTEMPTS;
    this.initialReconnectDelayMs = options.initialReconnectDelayMs ?? 1000;
    this.maxReconnectDelayMs = options.maxReconnectDelayMs ?? 15000;
    this.heartbeatIntervalMs = options.heartbeatIntervalMs ?? 15000;
  }

  getConnectionState(): ColabOneLiveConnectionState {
    return this.connectionState;
  }

  getCurrentEpoch(): number {
    return this.currentEpoch;
  }

  getDroppedStaleFrames(): number {
    return this.droppedStaleFrames;
  }

  getNegotiatedCodec(): string {
    return this.negotiatedCodec;
  }

  isConnected(): boolean {
    return (
      this.ws !== null &&
      this.ws.readyState === WebSocket.OPEN &&
      this.authenticated
    );
  }

  private setConnectionState(
    next: ColabOneLiveConnectionState,
    detail?: string,
  ): void {
    this.connectionState = next;
    this.options.onConnectionState?.(next, detail);
  }

  async connect(): Promise<void> {
    this.reconnectAttempt = 0;
    await this.connectInternal(false);
  }

  private async connectInternal(isReconnect: boolean): Promise<void> {
    this.manualDisconnect = false;
    this.clearReconnectTimer();
    const gen = ++this.connectGen;
    this.missedPongs = 0;
    this.lastPingSentAt = null;
    this.closeSocketOnly();
    this.setConnectionState(
      isReconnect || this.reconnectAttempt > 0 ? "reconnecting" : "connecting",
    );

    let tokenResult: ColabOneLiveSessionTokenResult;
    try {
      tokenResult = await this.options.fetchToken();
    } catch (err) {
      if (gen !== this.connectGen || this.manualDisconnect) return;
      const msg = err instanceof Error ? err.message : String(err);
      this.options.onError?.(msg, "token_fetch_failed");
      this.scheduleReconnect(msg);
      return;
    }

    if (gen !== this.connectGen || this.manualDisconnect) return;

    if (tokenResult?.canvas_ready === false) {
      const canRetry =
        typeof this.options.shouldRetryCanvasNotReady === "function"
          ? this.options.shouldRetryCanvasNotReady()
          : true;
      if (
        canRetry &&
        this.shouldReconnect &&
        this.reconnectAttempt < this.maxReconnectAttempts
      ) {
        this.scheduleReconnect("canvas_starting");
        return;
      }
      const msg = "Tunnel not ready";
      this.options.onError?.(msg, "canvas_not_ready");
      this.setConnectionState("error", msg);
      return;
    }

    const token = String(tokenResult?.token || "").trim();
    if (!token) {
      const msg = "Live Studio session token is empty";
      this.options.onError?.(msg, "empty_token");
      this.setConnectionState("error", msg);
      return;
    }

    const rawWsUrl = String(tokenResult?.ws_url || "").trim();
    if (!rawWsUrl) {
      const canRetry =
        typeof this.options.shouldRetryCanvasNotReady === "function"
          ? this.options.shouldRetryCanvasNotReady()
          : false;
      if (
        canRetry &&
        this.shouldReconnect &&
        this.reconnectAttempt < this.maxReconnectAttempts
      ) {
        this.scheduleReconnect("canvas_starting");
        return;
      }
      const msg = "Tunnel not ready";
      this.options.onError?.(msg, "canvas_not_ready");
      this.setConnectionState("error", msg);
      return;
    }

    let wsUrl: string;
    try {
      wsUrl = colabOneValidateLiveWsUrl(rawWsUrl);
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      this.options.onError?.(msg, "invalid_ws_url");
      this.setConnectionState("error", msg);
      return;
    }

    if (tokenResult.codec) {
      this.negotiatedCodec = tokenResult.codec;
    }

    let socket: WebSocket;
    try {
      const factory =
        this.options.webSocketFactory ?? ((url: string) => new WebSocket(url));
      socket = factory(wsUrl);
      socket.binaryType = "arraybuffer";
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      this.options.onError?.(msg, "ws_construct_failed");
      this.scheduleReconnect(msg);
      return;
    }

    this.ws = socket;
    this.authenticated = false;

    socket.onopen = () => {
      if (gen !== this.connectGen || this.ws !== socket) return;
      this.setConnectionState("authenticating");
      // Mandatory first-frame JSON auth: token is never placed in the URL.
      try {
        if (socket.readyState === WebSocket.OPEN) {
          socket.send(JSON.stringify({ type: "auth", token }));
        }
      } catch (err) {
        if (gen !== this.connectGen || this.ws !== socket) return;
        const msg = err instanceof Error ? err.message : String(err);
        this.options.onError?.(msg, "ws_send_failed");
        this.closeSocketOnly();
        this.authenticated = false;
        this.scheduleReconnect(msg);
      }
    };

    socket.onmessage = (event: MessageEvent) => {
      if (gen !== this.connectGen || this.ws !== socket) return;
      if (typeof event.data === "string") {
        this.handleTextMessage(event.data);
        return;
      }
      if (event.data instanceof ArrayBuffer) {
        this.handleBinaryMessage(event.data);
        return;
      }
      if (typeof Blob !== "undefined" && event.data instanceof Blob) {
        void event.data.arrayBuffer().then((buf) => {
          if (gen === this.connectGen && this.ws === socket) {
            this.handleBinaryMessage(buf);
          }
        });
      }
    };

    socket.onerror = () => {
      if (gen !== this.connectGen || this.ws !== socket) return;
      this.options.onError?.("WebSocket connection error", "ws_error");
    };

    socket.onclose = (event: CloseEvent) => {
      if (gen !== this.connectGen || this.ws !== socket) return;
      this.stopHeartbeat();
      this.ws = null;
      this.authenticated = false;
      if (this.manualDisconnect) {
        this.setConnectionState("disconnected");
        return;
      }
      const code = typeof event?.code === "number" ? event.code : 0;
      const reason =
        event?.reason || (code ? `code ${code}` : "closed");
      if (TERMINAL_WS_CLOSE_CODES.has(code)) {
        if (code === 1000) {
          this.setConnectionState("disconnected", reason);
        } else {
          this.options.onError?.(reason, `ws_close_${code}`);
          this.setConnectionState("error", reason);
        }
        return;
      }
      this.scheduleReconnect(reason);
    };
  }

  disconnect(): void {
    this.manualDisconnect = true;
    this.connectGen += 1;
    this.reconnectAttempt = 0;
    this.missedPongs = 0;
    this.lastPingSentAt = null;
    this.clearReconnectTimer();
    this.stopHeartbeat();
    this.closeSocketOnly();
    this.authenticated = false;
    this.setConnectionState("disconnected");
  }

  private closeSocketOnly(): void {
    if (!this.ws) return;
    const s = this.ws;
    this.ws = null;
    s.onopen = null;
    s.onmessage = null;
    s.onerror = null;
    s.onclose = null;
    try {
      if (
        s.readyState === WebSocket.CONNECTING ||
        s.readyState === WebSocket.OPEN
      ) {
        s.close(1000, "client_disconnect");
      }
    } catch {
      // Ignore close errors.
    }
  }

  private clearReconnectTimer(): void {
    if (this.reconnectTimer !== null) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
  }

  private startHeartbeat(): void {
    this.stopHeartbeat();
    this.missedPongs = 0;
    if (this.heartbeatIntervalMs <= 0) return;
    this.heartbeatTimer = setInterval(() => {
      if (!this.isConnected()) return;
      if (this.missedPongs >= 2) {
        this.stopHeartbeat();
        this.closeSocketOnly();
        this.authenticated = false;
        this.scheduleReconnect("pong_timeout");
        return;
      }
      this.missedPongs += 1;
      const now = Date.now();
      this.lastPingSentAt = now;
      this.sendJson({ type: "ping", ts: now });
    }, this.heartbeatIntervalMs);
  }

  private stopHeartbeat(): void {
    if (this.heartbeatTimer !== null) {
      clearInterval(this.heartbeatTimer);
      this.heartbeatTimer = null;
    }
    this.missedPongs = 0;
  }

  private scheduleReconnect(reason?: string): void {
    if (!this.shouldReconnect || this.manualDisconnect) {
      this.setConnectionState("disconnected", reason);
      return;
    }
    if (this.reconnectAttempt >= this.maxReconnectAttempts) {
      const detail = reason || "reconnect attempts exhausted";
      this.options.onError?.(detail, "reconnect_exhausted");
      this.setConnectionState("error", detail);
      return;
    }
    this.reconnectAttempt += 1;
    const expDelay =
      this.initialReconnectDelayMs * Math.pow(2, this.reconnectAttempt - 1);
    const baseDelay = Math.min(this.maxReconnectDelayMs, expDelay);
    // Jitter within [0.80, 1.00] of the exponential cap to avoid thundering herds.
    const delay = Math.max(
      50,
      Math.round(baseDelay * (0.8 + Math.random() * 0.2)),
    );
    this.setConnectionState("reconnecting", reason);
    this.clearReconnectTimer();
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null;
      if (!this.manualDisconnect) {
        void this.connectInternal(true);
      }
    }, delay);
  }

  private handleTextMessage(raw: string): void {
    let msg: Record<string, unknown>;
    try {
      const parsed = JSON.parse(raw);
      if (!parsed || typeof parsed !== "object") return;
      msg = parsed as Record<string, unknown>;
    } catch {
      return;
    }

    const type = String(msg.type || "");
    if (typeof msg.codec === "string" && msg.codec) {
      this.negotiatedCodec = msg.codec;
    }

    if (
      type === "auth_ok" ||
      type === "authenticated" ||
      type === "ready" ||
      type === "session_ready"
    ) {
      this.authenticated = true;
      this.reconnectAttempt = 0;
      this.missedPongs = 0;
      if (
        typeof msg.epoch === "number" &&
        Number.isFinite(msg.epoch) &&
        msg.epoch >= 0
      ) {
        this.currentEpoch = Math.floor(msg.epoch);
      }
      this.setConnectionState("connected");
      this.startHeartbeat();
      if (msg.companion && typeof msg.companion === "object") {
        this.options.onCompanionStatus?.(
          msg.companion as ColabOneLiveCompanionStatus,
          true,
        );
      }
      this.options.onAuthOk?.(msg);
      if (msg.state || msg.director_state) {
        this.options.onState?.(
          normalizeDirectorState(msg.director_state ?? msg.state),
          {
            ...(msg.state !== undefined ? { state: msg.state } : {}),
            ...(msg.director_state !== undefined
              ? { director_state: msg.director_state }
              : {}),
            ...(typeof msg.action_id === "string"
              ? { action_id: msg.action_id }
              : {}),
          },
        );
      }
      return;
    }

    // Some servers may immediately send state/telemetry after auth acceptance.
    if (!this.authenticated && type !== "error") {
      this.authenticated = true;
      this.reconnectAttempt = 0;
      this.missedPongs = 0;
      this.setConnectionState("connected");
      this.startHeartbeat();
    }

    switch (type) {
      case "companion_status": {
        this.options.onCompanionStatus?.(
          {
            connected: Boolean(msg.connected),
            device_label:
              typeof msg.device_label === "string" ? msg.device_label : "",
            mic_active: Boolean(msg.mic_active),
            camera_mode:
              typeof msg.camera_mode === "string" ? msg.camera_mode : "off",
            camera_fps:
              typeof msg.camera_fps === "number" ? msg.camera_fps : 0,
            motion_energy:
              typeof msg.motion_energy === "number" ? msg.motion_energy : 0,
            vision_frame_ready: Boolean(msg.vision_frame_ready),
            connected_at:
              typeof msg.connected_at === "number" ? msg.connected_at : null,
          },
          false,
        );
        break;
      }
      case "system_notice": {
        const text = String(msg.text || "").trim();
        if (text) {
          this.options.onSystemNotice?.(
            text,
            typeof msg.code === "string" ? msg.code : undefined,
          );
        }
        break;
      }
      case "barge_in": {
        const serverEpoch =
          typeof msg.epoch === "number" ? msg.epoch : this.currentEpoch + 1;
        const alreadyAtEpoch =
          typeof msg.epoch === "number" && msg.epoch <= this.currentEpoch;
        const nextEpoch = Math.max(this.currentEpoch, serverEpoch);
        this.currentEpoch = nextEpoch;
        if (!alreadyAtEpoch) {
          this.options.onBargeIn?.(
            nextEpoch,
            typeof msg.reason === "string" ? msg.reason : undefined,
          );
        }
        if (msg.state || msg.director_state) {
          this.options.onState?.(
            normalizeDirectorState(msg.director_state ?? msg.state),
            msg,
          );
        }
        break;
      }
      case "state":
      case "turn_end": {
        if (typeof msg.epoch === "number" && msg.epoch > this.currentEpoch) {
          this.currentEpoch = msg.epoch;
        }
        const nextState = normalizeDirectorState(
          msg.director_state ?? msg.state ?? "IDLE",
        );
        this.options.onState?.(nextState, msg);
        break;
      }
      case "action": {
        if (typeof msg.epoch === "number" && msg.epoch > this.currentEpoch) {
          this.currentEpoch = msg.epoch;
        }
        const actionId = String(msg.action_id || "").trim();
        if (actionId) {
          this.options.onAction?.(actionId, msg);
        }
        if (msg.state || msg.director_state) {
          this.options.onState?.(
            normalizeDirectorState(msg.director_state ?? msg.state),
            msg,
          );
        }
        break;
      }
      case "transcript":
      case "transcript_delta":
      case "asr_final": {
        const text = String(msg.text ?? msg.content ?? "").trim();
        if (!text) break;
        const defaultRole = type === "asr_final" ? "user" : "assistant";
        const roleRaw = String(msg.role || defaultRole).toLowerCase();
        const role: ColabOneLiveTranscriptEvent["role"] =
          roleRaw === "user" ||
          roleRaw === "director" ||
          roleRaw === "system"
            ? roleRaw
            : "assistant";
        const epoch =
          typeof msg.epoch === "number" ? msg.epoch : this.currentEpoch;
        const id =
          typeof msg.id === "string" && msg.id
            ? msg.id
            : `tr-${this.clientUid}-${++this.transcriptSeq}`;
        const isDelta = type === "transcript_delta";
        const isFinal = isDelta
          ? msg.is_final === true || msg.final === true
          : type === "asr_final" ||
            (msg.is_final !== false && msg.final !== false);
        this.options.onTranscript?.({
          id,
          role,
          text,
          isFinal,
          isDelta,
          epoch,
          ts: typeof msg.ts === "number" ? msg.ts : Date.now(),
        });
        break;
      }
      case "card": {
        const productId = String(msg.product_id ?? msg.card_id ?? "").trim();
        if (
          msg.action === "clear" ||
          (!msg.card && !msg.name && !productId)
        ) {
          this.options.onCard?.(null, msg);
        } else if (msg.card && typeof msg.card === "object") {
          this.options.onCard?.(msg.card as ColabOneLiveProductItem, msg);
        } else if (typeof msg.name === "string" || productId) {
          const highlights = Array.isArray(msg.highlights)
            ? msg.highlights.map((item) => String(item))
            : typeof msg.highlights === "string"
              ? msg.highlights
              : "";
          this.options.onCard?.(
            {
              id: String(msg.id || productId || "card"),
              name: String(msg.name || ""),
              price: String(msg.price || ""),
              highlights,
              script_hint:
                typeof msg.script_hint === "string"
                  ? msg.script_hint
                  : undefined,
              tag: typeof msg.tag === "string" ? msg.tag : undefined,
            },
            msg,
          );
        }
        break;
      }
      case "telemetry": {
        const src =
          msg.telemetry && typeof msg.telemetry === "object"
            ? (msg.telemetry as Record<string, unknown>)
            : msg;
        const tel: Partial<ColabOneLiveTelemetry> = {
          epoch:
            typeof src.epoch === "number" && Number.isFinite(src.epoch)
              ? src.epoch
              : this.currentEpoch,
        };
        if (typeof src.ttft_ms === "number" && Number.isFinite(src.ttft_ms)) {
          tel.ttft_ms = src.ttft_ms;
        }
        if (typeof src.ttfa_ms === "number" && Number.isFinite(src.ttfa_ms)) {
          tel.ttfa_ms = src.ttfa_ms;
        }
        if (typeof src.e2e_ms === "number" && Number.isFinite(src.e2e_ms)) {
          tel.e2e_ms = src.e2e_ms;
        }
        if (
          typeof src.vram_used_mib === "number" &&
          Number.isFinite(src.vram_used_mib)
        ) {
          tel.vram_used_mib = src.vram_used_mib;
        }
        if (
          typeof src.vram_total_mib === "number" &&
          Number.isFinite(src.vram_total_mib)
        ) {
          tel.vram_total_mib = src.vram_total_mib;
        }
        if (typeof src.fps === "number" && Number.isFinite(src.fps)) {
          tel.fps = src.fps;
        }
        if (typeof src.rtt_ms === "number" && Number.isFinite(src.rtt_ms)) {
          tel.rtt_ms = src.rtt_ms;
        }
        if (typeof src.video_codec === "string") {
          tel.video_codec = src.video_codec;
        }
        if (
          typeof src.audio_sample_rate === "number" &&
          Number.isFinite(src.audio_sample_rate)
        ) {
          tel.audio_sample_rate = src.audio_sample_rate;
        }
        if (
          typeof src.dropped_frames === "number" &&
          Number.isFinite(src.dropped_frames)
        ) {
          tel.dropped_frames = src.dropped_frames;
        }
        if (
          typeof src.uptime_seconds === "number" &&
          Number.isFinite(src.uptime_seconds)
        ) {
          tel.uptime_seconds = src.uptime_seconds;
        }
        if (
          typeof src.updated_at === "number" &&
          Number.isFinite(src.updated_at)
        ) {
          tel.updated_at = src.updated_at;
        }
        this.options.onTelemetry?.(tel);
        break;
      }
      case "error": {
        const message = String(
          msg.detail || msg.message || msg.error || "Live Studio protocol error",
        );
        const code = typeof msg.code === "string" ? msg.code : undefined;
        this.options.onError?.(message, code);
        break;
      }
      case "pong": {
        this.missedPongs = 0;
        const ts = typeof msg.ts === "number" ? msg.ts : undefined;
        const refTs = ts ?? this.lastPingSentAt ?? undefined;
        const rttMs =
          refTs !== undefined ? Math.max(0, Date.now() - refTs) : undefined;
        this.options.onPong?.(ts, rttMs);
        break;
      }
      default:
        break;
    }
  }

  private handleBinaryMessage(buffer: ArrayBuffer): void {
    const frame = colabOneUnpackLiveFrame(buffer);
    if (!frame) return;
    // Continuous H.264 fMP4 streams cannot drop in-flight moof/mdat fragments across
    // barge-in epoch boundaries without breaking the MediaSource decode sequence.
    if (
      frame.frameType !== COLABONE_LIVE_FRAME_FMP4 &&
      frame.epoch < this.currentEpoch
    ) {
      this.droppedStaleFrames += 1;
      return;
    }
    if (frame.epoch > this.currentEpoch) {
      this.currentEpoch = frame.epoch;
    }
    if (frame.frameType === COLABONE_LIVE_FRAME_VIDEO) {
      this.options.onVideoFrame?.(frame);
    } else if (frame.frameType === COLABONE_LIVE_FRAME_AUDIO_PCM) {
      this.options.onAudioFrame?.(frame);
    } else if (frame.frameType === COLABONE_LIVE_FRAME_FMP4) {
      this.options.onFmp4Chunk?.(frame);
    }
  }

  private sendJson(payload: Record<string, unknown>): boolean {
    if (
      !this.isConnected() ||
      !this.ws ||
      this.ws.readyState !== WebSocket.OPEN
    ) {
      return false;
    }
    try {
      this.ws.send(JSON.stringify(payload));
      return true;
    } catch {
      return false;
    }
  }

  sendMicPcm(pcm: Int16Array, ptsMs = Date.now()): boolean {
    if (
      !this.isConnected() ||
      !this.ws ||
      this.ws.readyState !== WebSocket.OPEN ||
      pcm.length === 0
    ) {
      return false;
    }
    const bytes = new Uint8Array(pcm.buffer, pcm.byteOffset, pcm.byteLength);
    const packet = colabOnePackLiveFrame(
      COLABONE_LIVE_FRAME_MIC_PCM,
      this.currentEpoch,
      ptsMs,
      bytes,
    );
    try {
      this.ws.send(packet);
      return true;
    } catch {
      return false;
    }
  }

  sendTextInput(text: string, mode?: ColabOneLiveDirectorMode): boolean {
    const trimmed = text.trim();
    if (!trimmed) return false;
    return this.sendJson({
      type: "text_input",
      text: trimmed,
      ...(mode ? { mode } : {}),
    });
  }

  sendSetMode(
    mode: ColabOneLiveDirectorMode,
    micMode?: ColabOneLiveMicMode,
  ): boolean {
    return this.sendJson({
      type: "set_mode",
      mode,
      director_mode: mode,
      ...(micMode ? { mic_mode: micMode } : {}),
    });
  }

  sendPttState(active: boolean): boolean {
    const isActive = Boolean(active);
    return this.sendJson({
      type: isActive ? "ptt_start" : "ptt_end",
      active: isActive,
    });
  }

  sendBargeIn(reason = "user_manual"): number {
    if (!this.isConnected()) {
      return this.currentEpoch;
    }
    this.currentEpoch += 1;
    const nextEpoch = this.currentEpoch;
    this.options.onBargeIn?.(nextEpoch, reason);
    this.sendJson({
      type: "barge_in",
      epoch: nextEpoch,
      reason,
    });
    return nextEpoch;
  }

  sendTriggerAction(
    actionId: string,
    extra?: Record<string, unknown>,
  ): boolean {
    if (!actionId.trim()) return false;
    return this.sendJson({
      type: "trigger_action",
      action_id: actionId.trim(),
      ...(extra ?? {}),
    });
  }

  sendSetCard(
    cardId: string | null,
    card?: ColabOneLiveProductItem | null,
  ): boolean {
    return this.sendJson({
      type: "show_card",
      product_id: cardId ?? "",
      card_id: cardId,
      card: card ?? null,
    });
  }

  sendSwitchEngine(options: {
    visual_engine?: ColabOneLiveVisualEngine;
    tts_engine?: ColabOneLiveTtsEngine;
    personalive_enabled?: boolean;
    avatar_id?: string;
  }): boolean {
    return this.sendJson({
      type: "switch_engine",
      ...options,
    });
  }
}

interface ColabOneH264NalSummary {
  hasIdr: boolean;
  hasSps: boolean;
  hasPps: boolean;
  hasVcl: boolean;
}

function inspectH264Nals(bytes: Uint8Array): ColabOneH264NalSummary {
  const summary: ColabOneH264NalSummary = {
    hasIdr: false,
    hasSps: false,
    hasPps: false,
    hasVcl: false,
  };
  const len = bytes.length;
  let i = 0;
  while (i < len - 3) {
    if (bytes[i] === 0 && bytes[i + 1] === 0) {
      let nalStart = -1;
      if (bytes[i + 2] === 1) {
        nalStart = i + 3;
      } else if (i + 4 < len && bytes[i + 2] === 0 && bytes[i + 3] === 1) {
        nalStart = i + 4;
      }
      if (nalStart >= 0 && nalStart < len) {
        const nalType = bytes[nalStart] & 0x1f;
        if (nalType === 5) {
          summary.hasIdr = true;
          summary.hasVcl = true;
        } else if (nalType === 7) {
          summary.hasSps = true;
        } else if (nalType === 8) {
          summary.hasPps = true;
        } else if (nalType >= 1 && nalType <= 4) {
          summary.hasVcl = true;
        }
        i = nalStart + 1;
        continue;
      }
    }
    i += 1;
  }
  return summary;
}

/**
 * Inspects Annex-B H.264 NAL units to determine whether the payload contains
 * an IDR keyframe (NAL unit type 5).
 */
export function colabOneIsH264Keyframe(bytes: Uint8Array): boolean {
  return inspectH264Nals(bytes).hasIdr;
}

/**
 * Checks whether an ISO BMFF / fMP4 byte buffer contains an initialization
 * segment (`ftyp` or `moov` top-level box).
 */
export function colabOneIsFmp4InitSegment(bytes: Uint8Array): boolean {
  const len = bytes.byteLength;
  if (len < 8) return false;
  const view = new DataView(bytes.buffer, bytes.byteOffset, len);
  let offset = 0;
  while (offset + 8 <= len) {
    const size32 = view.getUint32(offset, false);
    const boxType = String.fromCharCode(
      bytes[offset + 4],
      bytes[offset + 5],
      bytes[offset + 6],
      bytes[offset + 7],
    );
    if (boxType === "ftyp" || boxType === "moov") {
      return true;
    }
    let boxSize = size32;
    if (size32 === 0) {
      break;
    }
    if (size32 === 1) {
      if (offset + 16 > len) break;
      const hi = view.getUint32(offset + 8, false);
      const lo = view.getUint32(offset + 12, false);
      boxSize = hi * 4294967296 + lo;
      if (!Number.isSafeInteger(boxSize) || boxSize < 16) break;
    } else if (size32 < 8) {
      break;
    }
    offset += boxSize;
  }
  return false;
}

function splitFmp4InitAndMedia(bytes: Uint8Array<ArrayBuffer>): {
  initSegment: Uint8Array<ArrayBuffer> | null;
  mediaTail: Uint8Array<ArrayBuffer> | null;
  hasFtyp: boolean;
  hasMoov: boolean;
} {
  const len = bytes.byteLength;
  if (len < 8) {
    return { initSegment: null, mediaTail: null, hasFtyp: false, hasMoov: false };
  }
  const view = new DataView(bytes.buffer, bytes.byteOffset, len);
  let offset = 0;
  let hasFtyp = false;
  let hasMoov = false;
  let initEnd = 0;
  let sawMediaBox = false;
  while (offset + 8 <= len) {
    const size32 = view.getUint32(offset, false);
    const boxType = String.fromCharCode(
      bytes[offset + 4],
      bytes[offset + 5],
      bytes[offset + 6],
      bytes[offset + 7],
    );
    let boxSize = size32;
    if (size32 === 0) {
      boxSize = len - offset;
    } else if (size32 === 1) {
      if (offset + 16 > len) break;
      const hi = view.getUint32(offset + 8, false);
      const lo = view.getUint32(offset + 12, false);
      boxSize = hi * 4294967296 + lo;
      if (!Number.isSafeInteger(boxSize) || boxSize < 16) break;
    } else if (size32 < 8) {
      break;
    }
    if (offset + boxSize > len) {
      break;
    }
    if (boxType === "ftyp" || boxType === "moov") {
      if (!sawMediaBox) {
        if (boxType === "ftyp") hasFtyp = true;
        if (boxType === "moov") hasMoov = true;
        initEnd = offset + boxSize;
      }
    } else if (boxType === "moof" || boxType === "mdat") {
      sawMediaBox = true;
      break;
    }
    offset += boxSize;
  }
  if ((!hasFtyp && !hasMoov) || initEnd <= 0) {
    return { initSegment: null, mediaTail: null, hasFtyp: false, hasMoov: false };
  }
  if (initEnd >= len) {
    return { initSegment: bytes, mediaTail: null, hasFtyp, hasMoov };
  }
  const initCopy = new Uint8Array(new ArrayBuffer(initEnd));
  initCopy.set(bytes.subarray(0, initEnd));
  const tailLen = len - initEnd;
  const tailCopy = new Uint8Array(new ArrayBuffer(tailLen));
  tailCopy.set(bytes.subarray(initEnd));
  return { initSegment: initCopy, mediaTail: tailCopy, hasFtyp, hasMoov };
}

function normalizeRendererCodec(
  rawCodec: string,
  currentCodec?: string,
  hasActiveMse = false,
): string {
  const clean = String(rawCodec || "").trim().toLowerCase();
  if (!clean) return currentCodec || "h264";
  if (clean === "fmp4") return "fmp4";
  if (clean === "jpeg" || clean === "mjpeg") return "jpeg";
  if (
    clean.startsWith("avc1.") ||
    clean === "h264" ||
    clean === "avc" ||
    clean === "h264_nvenc" ||
    clean === "libx264" ||
    clean.startsWith("h264_")
  ) {
    if (currentCodec === "fmp4" || hasActiveMse) {
      return "fmp4";
    }
    return "h264";
  }
  return clean;
}

function isQuotaExceededError(err: unknown): boolean {
  if (!err || typeof err !== "object") return false;
  const maybe = err as { name?: unknown; code?: unknown };
  return maybe.name === "QuotaExceededError" || maybe.code === 22;
}

/**
 * Checks JPEG Start-of-Image marker (0xFF 0xD8).
 */
export function colabOneIsJpegPayload(bytes: Uint8Array): boolean {
  return bytes.length >= 2 && bytes[0] === 0xff && bytes[1] === 0xd8;
}

export interface ColabOneLiveVideoRendererOptions {
  canvas: HTMLCanvasElement;
  videoElement?: HTMLVideoElement | null;
  codec?: "h264" | "jpeg" | "fmp4" | string;
  h264CodecString?: string;
  videoCodecMime?: string;
  onFrameRendered?: (info: {
    width: number;
    height: number;
    fps: number;
    mode: "webcodecs" | "jpeg" | "mse";
  }) => void;
  onError?: (err: Error) => void;
}

function nowMs(): number {
  return typeof performance !== "undefined" ? performance.now() : Date.now();
}

/**
 * Renders live digital-human video onto an HTML `<canvas>` using:
 * 1. Primary: WebCodecs `VideoDecoder` (H.264 Annex-B) with strict `frame.close()`.
 * 2. Fallback 1: JPEG frame bitmaps via `createImageBitmap` (with `bitmap.close()`).
 * 3. Fallback 2: MSE `MediaSource` for `0x04` fMP4 chunks when WebCodecs is unavailable.
 */
export class ColabOneLiveVideoRenderer {
  private readonly canvas: HTMLCanvasElement;
  private readonly ctx: CanvasRenderingContext2D | null;
  private readonly videoElement: HTMLVideoElement | null;
  private readonly onFrameRendered?: ColabOneLiveVideoRendererOptions["onFrameRendered"];
  private readonly onError?: (err: Error) => void;

  private codec: string;
  private h264CodecString: string;
  private videoCodecMime: string;
  private decoder: VideoDecoder | null = null;
  private waitingForKeyframe = true;
  private pendingSpsPpsChunks: Uint8Array[] = [];
  private destroyed = false;

  // JPEG decode concurrency guard (latest-frame-wins to avoid lag buildup)
  private jpegBusy = false;
  private pendingJpeg: Uint8Array | null = null;
  private pendingImageUrls = new Set<string>();
  private pendingImages = new Set<HTMLImageElement>();

  // MSE fallback state
  private mediaSource: MediaSource | null = null;
  private mediaSourceUrl: string | null = null;
  private sourceBuffer: SourceBuffer | null = null;
  private mseInitSegment: Uint8Array<ArrayBuffer> | null = null;
  private mseInitHasMoov = false;
  private mseInitAppended = false;
  private mseQueue: Uint8Array<ArrayBuffer>[] = [];
  private msePendingResetRemove = false;
  private mseSeekToLiveEdge = false;
  private mseQuotaRetryCount = 0;
  private mseFailed = false;
  private mseRafId: number | null = null;
  private lastMseVideoTime = -1;

  // Sliding 1-second FPS measurement + 1 Hz decay watchdog
  private frameTimestamps: number[] = [];
  private renderedFrames = 0;
  private lastFrameTime = -1;
  private lastWidth = 0;
  private lastHeight = 0;
  private lastMode: "webcodecs" | "jpeg" | "mse" = "webcodecs";
  private lastReportedFps = 0;
  private fpsDecayTimer: ReturnType<typeof setInterval> | null = null;

  constructor(options: ColabOneLiveVideoRendererOptions) {
    this.canvas = options.canvas;
    this.ctx = options.canvas.getContext("2d");
    this.videoElement = options.videoElement ?? null;
    const rawCodec = options.codec ?? "h264";
    this.codec = normalizeRendererCodec(rawCodec);
    this.h264CodecString =
      options.h264CodecString ??
      (rawCodec.startsWith("avc1.") ? rawCodec : DEFAULT_H264_CODEC_STRING);
    this.videoCodecMime = String(options.videoCodecMime || "").trim();
    this.onFrameRendered = options.onFrameRendered;
    this.onError = options.onError;
  }

  getRenderedFrameCount(): number {
    return this.renderedFrames;
  }

  getH264CodecString(): string {
    return this.h264CodecString;
  }

  getMeasuredFps(referenceNow = nowMs()): number {
    if (
      this.lastFrameTime < 0 ||
      referenceNow - this.lastFrameTime >= FPS_STALE_DECAY_MS
    ) {
      return 0;
    }
    const cutoff = referenceNow - FPS_WINDOW_MS;
    while (this.frameTimestamps.length > 0 && this.frameTimestamps[0] < cutoff) {
      this.frameTimestamps.shift();
    }
    return this.frameTimestamps.length;
  }

  setCodec(
    codec: string,
    h264CodecString?: string,
    videoCodecMime?: string,
  ): void {
    const hasActiveMse = Boolean(this.mediaSource || this.sourceBuffer);
    const normalizedCodec = normalizeRendererCodec(
      codec,
      this.codec,
      hasActiveMse,
    );
    const nextAvc =
      h264CodecString ||
      (codec.startsWith("avc1.") ? codec : this.h264CodecString);
    const avcChanged = this.h264CodecString !== nextAvc;
    const explicitMime =
      typeof videoCodecMime === "string" && videoCodecMime.trim()
        ? videoCodecMime.trim()
        : "";
    const nextMime = explicitMime || (avcChanged ? "" : this.videoCodecMime);
    if (
      this.codec === normalizedCodec &&
      this.h264CodecString === nextAvc &&
      this.videoCodecMime === nextMime
    ) {
      return;
    }
    const mseCodecChanged = avcChanged || this.videoCodecMime !== nextMime;
    const hadMse = Boolean(
      this.mediaSource || this.sourceBuffer || this.mseFailed,
    );
    const prevCodec = this.codec;
    this.codec = normalizedCodec;
    this.h264CodecString = nextAvc;
    this.videoCodecMime = nextMime;
    if (mseCodecChanged) {
      this.teardownMsePipeline();
      this.mseFailed = false;
      this.resetEpoch();
      if ((hadMse || normalizedCodec === "fmp4") && this.videoElement && !this.destroyed) {
        this.ensureMsePipeline();
      }
      return;
    }
    // Switching from initial default "h264" to "fmp4" upon first fMP4 chunk must not wipe MSE.
    if (prevCodec === "h264" && normalizedCodec === "fmp4" && !hadMse) {
      this.resetEpoch({ preserveMseStream: true });
      return;
    }
    this.resetEpoch();
  }

  resetEpoch(opts?: { preserveMseStream?: boolean }): void {
    this.waitingForKeyframe = true;
    this.pendingJpeg = null;
    this.pendingSpsPpsChunks = [];
    if (this.decoder && this.decoder.state === "configured") {
      try {
        this.decoder.reset();
        this.configureDecoder(this.decoder);
      } catch {
        this.closeDecoder();
      }
    }
    if (opts?.preserveMseStream) {
      return;
    }
    this.mseQueue = [];
    this.lastMseVideoTime = -1;
    this.mseQuotaRetryCount = 0;
    this.mseInitAppended = false;
    this.mseSeekToLiveEdge = true;
    if (this.sourceBuffer && this.mediaSource?.readyState === "open") {
      try {
        this.sourceBuffer.abort();
      } catch {
        // Ignore MSE abort errors.
      }
      this.msePendingResetRemove = true;
      if (!this.flushPendingResetRemove(this.sourceBuffer)) {
        this.drainMseQueue();
      }
    }
  }

  pushVideoFrame(payload: Uint8Array, ptsMs: number): void {
    if (this.destroyed || payload.byteLength === 0) return;
    if (this.codec === "jpeg" || colabOneIsJpegPayload(payload)) {
      this.renderJpeg(payload);
      return;
    }
    this.decodeH264(payload, ptsMs);
  }

  pushFmp4Chunk(payload: Uint8Array): void {
    if (this.destroyed || payload.byteLength === 0) return;
    this.appendMseChunk(payload);
  }

  private ensureFpsDecayTimer(): void {
    if (this.fpsDecayTimer !== null || typeof setInterval !== "function") return;
    this.fpsDecayTimer = setInterval(() => {
      if (this.destroyed) return;
      const now = nowMs();
      if (
        this.lastFrameTime >= 0 &&
        now - this.lastFrameTime >= FPS_STALE_DECAY_MS
      ) {
        this.frameTimestamps = [];
        if (this.lastReportedFps !== 0) {
          this.lastReportedFps = 0;
          this.onFrameRendered?.({
            width: this.lastWidth,
            height: this.lastHeight,
            fps: 0,
            mode: this.lastMode,
          });
        }
      }
    }, 1000);
  }

  private recordFrame(
    width: number,
    height: number,
    mode: "webcodecs" | "jpeg" | "mse",
  ): void {
    if (this.destroyed) return;
    const now = nowMs();
    this.renderedFrames += 1;
    this.lastFrameTime = now;
    this.lastWidth = width;
    this.lastHeight = height;
    this.lastMode = mode;
    this.frameTimestamps.push(now);
    const fps = this.getMeasuredFps(now);
    this.lastReportedFps = fps;
    this.ensureFpsDecayTimer();
    this.onFrameRendered?.({
      width,
      height,
      fps,
      mode,
    });
  }

  private ensureWebCodecsDecoder(): VideoDecoder | null {
    if (this.decoder && this.decoder.state === "configured") {
      return this.decoder;
    }
    if (
      typeof VideoDecoder === "undefined" ||
      typeof EncodedVideoChunk === "undefined"
    ) {
      return null;
    }
    this.closeDecoder();
    try {
      const dec = new VideoDecoder({
        output: (frame: VideoFrame) => {
          try {
            if (!this.destroyed && this.ctx) {
              const w = frame.displayWidth || frame.codedWidth || this.canvas.width;
              const h =
                frame.displayHeight || frame.codedHeight || this.canvas.height;
              if (
                w > 0 &&
                h > 0 &&
                (this.canvas.width !== w || this.canvas.height !== h)
              ) {
                this.canvas.width = w;
                this.canvas.height = h;
              }
              this.ctx.drawImage(
                frame,
                0,
                0,
                this.canvas.width,
                this.canvas.height,
              );
              this.recordFrame(
                this.canvas.width,
                this.canvas.height,
                "webcodecs",
              );
            }
          } finally {
            // Strictly release VideoFrame GPU resources on every single frame.
            frame.close();
          }
        },
        error: (err: DOMException) => {
          this.waitingForKeyframe = true;
          if (!this.destroyed) {
            this.onError?.(err instanceof Error ? err : new Error(String(err)));
          }
        },
      });
      this.configureDecoder(dec);
      this.decoder = dec;
      this.waitingForKeyframe = true;
      return dec;
    } catch (err) {
      if (!this.destroyed) {
        this.onError?.(err instanceof Error ? err : new Error(String(err)));
      }
      return null;
    }
  }

  private configureDecoder(dec: VideoDecoder): void {
    dec.configure({
      codec: this.h264CodecString,
      optimizeForLatency: true,
    });
  }

  private decodeH264(payload: Uint8Array, ptsMs: number): void {
    const nals = inspectH264Nals(payload);
    if ((nals.hasSps || nals.hasPps) && !nals.hasVcl) {
      const copy = payload.slice();
      if (nals.hasSps) {
        this.pendingSpsPpsChunks = [copy];
      } else if (this.pendingSpsPpsChunks.length < 4) {
        this.pendingSpsPpsChunks.push(copy);
      }
      return;
    }
    const dec = this.ensureWebCodecsDecoder();
    if (!dec) return;
    const isKey = nals.hasIdr;
    if (
      !isKey &&
      typeof dec.decodeQueueSize === "number" &&
      dec.decodeQueueSize > 3
    ) {
      this.waitingForKeyframe = true;
      return;
    }
    if (this.waitingForKeyframe) {
      if (!isKey) return;
      this.waitingForKeyframe = false;
    }
    let frameData = payload;
    if (isKey && this.pendingSpsPpsChunks.length > 0) {
      if (!(nals.hasSps && nals.hasPps)) {
        let totalLen = payload.byteLength;
        for (const prefix of this.pendingSpsPpsChunks) {
          totalLen += prefix.byteLength;
        }
        const merged = new Uint8Array(totalLen);
        let offset = 0;
        for (const prefix of this.pendingSpsPpsChunks) {
          merged.set(prefix, offset);
          offset += prefix.byteLength;
        }
        merged.set(payload, offset);
        frameData = merged;
      }
      this.pendingSpsPpsChunks = [];
    }
    try {
      const chunk = new EncodedVideoChunk({
        type: isKey ? "key" : "delta",
        timestamp: Math.max(0, Math.round(ptsMs * 1000)),
        data: frameData,
      });
      dec.decode(chunk);
    } catch (err) {
      this.waitingForKeyframe = true;
      this.closeDecoder();
      if (!this.destroyed) {
        this.onError?.(err instanceof Error ? err : new Error(String(err)));
      }
    }
  }

  private renderJpeg(payload: Uint8Array): void {
    if (this.destroyed) return;
    if (this.jpegBusy) {
      this.pendingJpeg = payload;
      return;
    }
    this.jpegBusy = true;
    const copy = new Uint8Array(payload.byteLength);
    copy.set(payload);
    const blob = new Blob([copy], { type: "image/jpeg" });

    const finish = () => {
      this.jpegBusy = false;
      if (!this.destroyed && this.pendingJpeg) {
        const next = this.pendingJpeg;
        this.pendingJpeg = null;
        this.renderJpeg(next);
      }
    };

    if (typeof createImageBitmap === "function") {
      void createImageBitmap(blob)
        .then((bitmap) => {
          try {
            if (!this.destroyed && this.ctx) {
              if (
                bitmap.width > 0 &&
                bitmap.height > 0 &&
                (this.canvas.width !== bitmap.width ||
                  this.canvas.height !== bitmap.height)
              ) {
                this.canvas.width = bitmap.width;
                this.canvas.height = bitmap.height;
              }
              this.ctx.drawImage(
                bitmap,
                0,
                0,
                this.canvas.width,
                this.canvas.height,
              );
              this.recordFrame(this.canvas.width, this.canvas.height, "jpeg");
            }
          } finally {
            bitmap.close();
          }
        })
        .catch((err) => {
          if (!this.destroyed) {
            this.onError?.(err instanceof Error ? err : new Error(String(err)));
          }
        })
        .finally(finish);
      return;
    }

    // Fallback for environments without createImageBitmap
    if (
      typeof Image !== "undefined" &&
      typeof URL !== "undefined" &&
      typeof URL.createObjectURL === "function"
    ) {
      const url = URL.createObjectURL(blob);
      const img = new Image();
      this.pendingImageUrls.add(url);
      this.pendingImages.add(img);
      const cleanup = () => {
        this.pendingImages.delete(img);
        if (this.pendingImageUrls.delete(url)) {
          URL.revokeObjectURL(url);
        }
        img.onload = null;
        img.onerror = null;
      };
      img.onload = () => {
        try {
          if (!this.destroyed && this.ctx) {
            if (
              img.naturalWidth > 0 &&
              img.naturalHeight > 0 &&
              (this.canvas.width !== img.naturalWidth ||
                this.canvas.height !== img.naturalHeight)
            ) {
              this.canvas.width = img.naturalWidth;
              this.canvas.height = img.naturalHeight;
            }
            this.ctx.drawImage(
              img,
              0,
              0,
              this.canvas.width,
              this.canvas.height,
            );
            this.recordFrame(this.canvas.width, this.canvas.height, "jpeg");
          }
        } finally {
          cleanup();
          finish();
        }
      };
      img.onerror = () => {
        cleanup();
        finish();
      };
      img.src = url;
      return;
    }

    finish();
  }

  private ensureMsePipeline(): void {
    if (this.mseFailed || this.mediaSource) return;
    const video = this.videoElement;
    if (typeof MediaSource === "undefined" || !video) {
      this.mseFailed = true;
      return;
    }
    const mime =
      this.videoCodecMime || `video/mp4; codecs="${this.h264CodecString}"`;
    if (
      typeof MediaSource.isTypeSupported === "function" &&
      !MediaSource.isTypeSupported(mime)
    ) {
      this.mseFailed = true;
      return;
    }

    let ms: MediaSource;
    try {
      ms = new MediaSource();
    } catch (err) {
      this.mseFailed = true;
      if (!this.destroyed) {
        this.onError?.(err instanceof Error ? err : new Error(String(err)));
      }
      return;
    }
    this.mediaSource = ms;
    const url = URL.createObjectURL(ms);
    this.mediaSourceUrl = url;
    video.src = url;
    video.muted = true;
    video.playsInline = true;

    ms.addEventListener("sourceopen", () => {
      if (this.destroyed || this.mediaSource !== ms) return;
      try {
        const sb = ms.addSourceBuffer(mime);
        sb.mode = "sequence";
        sb.addEventListener("updateend", () => {
          if (this.destroyed || this.sourceBuffer !== sb) return;
          if (this.msePendingResetRemove) {
            if (this.flushPendingResetRemove(sb)) return;
          }
          try {
            const buffered = sb.buffered;
            if (
              !this.mseSeekToLiveEdge &&
              this.videoElement &&
              buffered &&
              buffered.length > 0 &&
              this.videoElement.currentTime < buffered.start(0) - 0.05
            ) {
              this.mseSeekToLiveEdge = true;
            }
          } catch {
            // Ignore buffered read errors.
          }
          this.maybeSeekMseToLiveEdge(sb);
          if (this.maybeEvictMseBuffer()) return;
          this.drainMseQueue();
        });
        this.sourceBuffer = sb;
        this.drainMseQueue();
        void video.play().catch(() => {});
        this.startMseCanvasPump();
      } catch (err) {
        this.mseFailed = true;
        this.mseQueue = [];
        if (!this.destroyed) {
          this.onError?.(err instanceof Error ? err : new Error(String(err)));
        }
      }
    });
  }

  private removeAllMseBuffered(sb: SourceBuffer): boolean {
    if (sb.updating) return false;
    try {
      const buffered = sb.buffered;
      if (buffered && buffered.length === 0) {
        return false;
      }
      try {
        sb.remove(0, Infinity);
        this.mseSeekToLiveEdge = true;
        return true;
      } catch {
        if (buffered && buffered.length > 0) {
          const start = buffered.start(0);
          const end = buffered.end(buffered.length - 1);
          if (end > start) {
            sb.remove(start, end);
            this.mseSeekToLiveEdge = true;
            return true;
          }
        }
      }
    } catch {
      // Ignore MediaSource range removal errors.
    }
    return false;
  }

  private flushPendingResetRemove(sb: SourceBuffer): boolean {
    if (!this.msePendingResetRemove || sb.updating) return false;
    this.msePendingResetRemove = false;
    return this.removeAllMseBuffered(sb);
  }

  private maybeSeekMseToLiveEdge(sb: SourceBuffer): void {
    if (!this.mseSeekToLiveEdge || !this.videoElement || sb.updating) return;
    try {
      const buffered = sb.buffered;
      if (!buffered || buffered.length === 0) return;
      const rangeStart = buffered.start(buffered.length - 1);
      const rangeEnd = buffered.end(buffered.length - 1);
      if (rangeEnd > rangeStart) {
        this.videoElement.currentTime = Math.max(rangeStart, rangeEnd - 0.05);
        this.mseSeekToLiveEdge = false;
      }
    } catch {
      // Ignore live-edge seek errors.
    }
  }

  private maybeEvictMseBuffer(force = false): boolean {
    const sb = this.sourceBuffer;
    if (!sb || sb.updating) return false;
    try {
      const buffered = sb.buffered;
      if (!buffered || buffered.length === 0) return false;
      const start = buffered.start(0);
      const end = buffered.end(buffered.length - 1);
      if (!force && end - start <= MSE_EVICT_THRESHOLD_SEC) return false;
      const currentTime = this.videoElement?.currentTime ?? end;
      let removeEnd = force
        ? Math.min(end - 2, Math.max(start + 1, currentTime - 1))
        : Math.min(end - 2, currentTime - 0.5);
      if (force && removeEnd <= start) {
        removeEnd = end;
      }
      if (removeEnd > start) {
        sb.remove(start, removeEnd);
        if (force || removeEnd > currentTime - 0.05) {
          this.mseSeekToLiveEdge = true;
        }
        return true;
      }
    } catch {
      // Ignore MediaSource range eviction errors.
    }
    return false;
  }

  private appendMseChunk(chunk: Uint8Array): void {
    this.ensureMsePipeline();
    if (this.mseFailed || !this.mediaSource) return;
    const copy = new Uint8Array(new ArrayBuffer(chunk.byteLength));
    copy.set(chunk);
    if (colabOneIsFmp4InitSegment(copy)) {
      const { initSegment, mediaTail, hasFtyp, hasMoov } =
        splitFmp4InitAndMedia(copy);
      if (initSegment) {
        if (hasFtyp) {
          // New stream initialization: discard stale pre-init queued media fragments.
          this.mseQueue = [];
          this.mseInitSegment = initSegment;
          this.mseInitHasMoov = hasMoov;
          this.mseInitAppended = false;
        } else if (hasMoov && this.mseInitSegment && !this.mseInitHasMoov) {
          // Merge split [ftyp] + [moov] into a complete cached init segment.
          const merged = new Uint8Array(
            new ArrayBuffer(this.mseInitSegment.byteLength + initSegment.byteLength),
          );
          merged.set(this.mseInitSegment, 0);
          merged.set(initSegment, this.mseInitSegment.byteLength);
          this.mseInitSegment = merged;
          this.mseInitHasMoov = true;
          if (this.mseInitAppended) {
            // ftyp was already appended; queue moov directly in front of media.
            this.mseQueue = [initSegment];
          } else {
            this.mseQueue = [];
          }
        } else {
          this.mseQueue = [];
          this.mseInitSegment = initSegment;
          this.mseInitHasMoov = hasMoov;
          this.mseInitAppended = false;
        }
        if (mediaTail && mediaTail.byteLength > 0) {
          while (this.mseQueue.length >= MAX_MSE_QUEUE_CHUNKS) {
            this.mseQueue.shift();
          }
          this.mseQueue.push(mediaTail);
        }
        this.drainMseQueue();
        return;
      }
    }
    while (this.mseQueue.length >= MAX_MSE_QUEUE_CHUNKS) {
      this.mseQueue.shift();
    }
    this.mseQueue.push(copy);
    this.drainMseQueue();
  }

  private handleMseAppendError(
    err: unknown,
    failedChunk: Uint8Array<ArrayBuffer> | null,
  ): void {
    const sb = this.sourceBuffer;
    if (isQuotaExceededError(err) && sb && this.mseQuotaRetryCount < 2) {
      this.mseQuotaRetryCount += 1;
      const evicted =
        this.mseQuotaRetryCount === 1
          ? this.maybeEvictMseBuffer(true) || this.removeAllMseBuffered(sb)
          : this.removeAllMseBuffered(sb);
      if (evicted) {
        if (failedChunk) {
          this.mseQueue.unshift(failedChunk);
        } else {
          this.mseInitAppended = false;
        }
        return;
      }
    }
    this.mseQuotaRetryCount = 0;
    if (!this.destroyed) {
      this.onError?.(err instanceof Error ? err : new Error(String(err)));
    }
  }

  private drainMseQueue(): void {
    const sb = this.sourceBuffer;
    if (this.destroyed || this.mseFailed || !sb || sb.updating) {
      return;
    }
    if (this.msePendingResetRemove) {
      if (this.flushPendingResetRemove(sb)) {
        return;
      }
    }
    if (!this.mseInitAppended && this.mseInitSegment) {
      const initSeg = this.mseInitSegment;
      try {
        sb.appendBuffer(initSeg);
        this.mseInitAppended = true;
        this.mseQuotaRetryCount = 0;
      } catch (err) {
        this.handleMseAppendError(err, null);
      }
      return;
    }
    if (this.mseQueue.length === 0) {
      return;
    }
    const next = this.mseQueue.shift();
    if (!next) return;
    try {
      sb.appendBuffer(next);
      this.mseQuotaRetryCount = 0;
    } catch (err) {
      this.handleMseAppendError(err, next);
    }
  }

  private teardownMsePipeline(): void {
    this.mseQueue = [];
    this.mseInitSegment = null;
    this.mseInitHasMoov = false;
    this.mseInitAppended = false;
    this.msePendingResetRemove = false;
    this.mseSeekToLiveEdge = false;
    this.mseQuotaRetryCount = 0;
    this.lastMseVideoTime = -1;
    if (this.mseRafId !== null && typeof cancelAnimationFrame === "function") {
      cancelAnimationFrame(this.mseRafId);
      this.mseRafId = null;
    }
    if (this.sourceBuffer && this.mediaSource?.readyState === "open") {
      try {
        this.sourceBuffer.abort();
      } catch {
        // Ignore teardown errors.
      }
    }
    if (this.mediaSourceUrl) {
      URL.revokeObjectURL(this.mediaSourceUrl);
      this.mediaSourceUrl = null;
    }
    if (this.videoElement) {
      try {
        this.videoElement.pause();
        this.videoElement.removeAttribute("src");
        this.videoElement.load();
      } catch {
        // Ignore teardown errors.
      }
    }
    this.sourceBuffer = null;
    this.mediaSource = null;
  }

  private startMseCanvasPump(): void {
    if (this.mseRafId !== null || typeof requestAnimationFrame !== "function") {
      return;
    }
    const tick = () => {
      if (this.destroyed) {
        this.mseRafId = null;
        return;
      }
      const video = this.videoElement;
      if (
        video &&
        this.ctx &&
        video.readyState >= 2 &&
        video.videoWidth > 0 &&
        video.currentTime !== this.lastMseVideoTime
      ) {
        this.lastMseVideoTime = video.currentTime;
        if (
          this.canvas.width !== video.videoWidth ||
          this.canvas.height !== video.videoHeight
        ) {
          this.canvas.width = video.videoWidth;
          this.canvas.height = video.videoHeight;
        }
        this.ctx.drawImage(video, 0, 0, this.canvas.width, this.canvas.height);
        this.recordFrame(this.canvas.width, this.canvas.height, "mse");
      }
      this.mseRafId = requestAnimationFrame(tick);
    };
    this.mseRafId = requestAnimationFrame(tick);
  }

  private closeDecoder(): void {
    if (!this.decoder) return;
    try {
      if (this.decoder.state !== "closed") {
        this.decoder.close();
      }
    } catch {
      // Ignore teardown errors.
    }
    this.decoder = null;
  }

  destroy(): void {
    this.destroyed = true;
    this.pendingJpeg = null;
    this.pendingSpsPpsChunks = [];
    this.frameTimestamps = [];
    if (this.fpsDecayTimer !== null) {
      clearInterval(this.fpsDecayTimer);
      this.fpsDecayTimer = null;
    }
    for (const img of this.pendingImages) {
      img.onload = null;
      img.onerror = null;
      try {
        img.src = "";
      } catch {
        // Ignore teardown errors.
      }
    }
    this.pendingImages.clear();
    for (const url of this.pendingImageUrls) {
      try {
        URL.revokeObjectURL(url);
      } catch {
        // Ignore teardown errors.
      }
    }
    this.pendingImageUrls.clear();

    this.closeDecoder();
    this.teardownMsePipeline();
  }
}
