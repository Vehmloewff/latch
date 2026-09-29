package typescript

// runtimeBody is the language runtime shared by every generated TypeScript
// client: WebSocket transport, connection setup, request-ID
// correlation, and event dispatch. It never depends on any particular
// protocol, so it is emitted verbatim rather than templated.
const runtimeBody = `export type WebSocketFactory = (url: string) => WebSocketLike;

type BinaryMessage = ArrayBuffer | Uint8Array;

/** The minimal WebSocket surface Latch's runtime needs, satisfied by
 * both the browser's global WebSocket and Node/Bun/Deno implementations. */
export interface WebSocketLike {
  readonly readyState: number;
  binaryType?: string;
  onopen: ((ev: unknown) => void) | null;
  onclose: ((ev: unknown) => void) | null;
  onerror: ((ev: unknown) => void) | null;
  onmessage: ((ev: { data: BinaryMessage }) => void) | null;
  send(data: Uint8Array): void;
  close(code?: number, reason?: string): void;
}

function asBytes(data: BinaryMessage): Uint8Array {
  return data instanceof Uint8Array ? data : new Uint8Array(data);
}

/** Thrown for every RPC rejection and connect failure. */
export class LatchError extends Error {
  readonly code: string;

  constructor(code: string, message: string) {
    super(message);
    this.name = "LatchError";
    this.code = code;
  }
}

export const ConnectionState = {
  Connecting: "connecting",
  Offline: "offline",
  Connected: "connected",
} as const;
export type ConnectionState = (typeof ConnectionState)[keyof typeof ConnectionState];

export interface ClientOptions {
  /** Called whenever the connection changes state. */
  onConnectionStateChange?: (state: ConnectionState) => void;
  /** The WebSocket URL to connect to, e.g. "wss://example.com/ws". */
  url: string;
  /** Supply a WebSocket implementation for environments with no global
   * WebSocket (older Node versions, some test runners). Defaults to
   * globalThis.WebSocket. */
  webSocketFactory?: WebSocketFactory;
}


interface PendingRequest {
  resolve: (value: unknown) => void;
  reject: (err: unknown) => void;
  decode: (data: Uint8Array) => unknown;
}


const connectionClosedError = () => new LatchError("connection_closed", "the connection is closed");

/** Base class for every generated "Connected*Client". Handles the
 * WebSocket message loop, request/response correlation by ID, and
 * dispatch of connection_error / close conditions. Generated subclasses
 * add typed methods (backed by call()) and event callbacks
 * (backed by dispatchEvent()). */
export abstract class BaseConnection {
  private ws: WebSocketLike;
  private nextId = 1;
  private pending = new Map<string, PendingRequest>();
  private _closed = false;
  private online = true;
  private queued: Array<() => void> = [];
  private queuedRejects = new Map<() => void, (err: unknown) => void>();

  private readonly onClose: () => void;
  private readonly onShutdown: () => void;

  constructor(handshake: HandshakeResult, onClose: () => void = () => {}, onShutdown: () => void = () => {}) {
    this.onClose = onClose;
    this.onShutdown = onShutdown;
    this.ws = handshake.ws;
    // Install the real handler before replaying anything, so a message
    // that arrives while we're replaying is still queued in order rather
    // than raced against this constructor.
    this.installHandlers();
    // Events from OnConnect can arrive before this constructor runs. Replay them
    // in order on the next turn, after the generated subclass has initialized
    // its event callback.
    if (handshake.bufferedMessages.length > 0) {
      const buffered = handshake.bufferedMessages;
      const socket = handshake.ws;
      setTimeout(() => {
        for (const data of buffered) {
          if (this.ws === socket && this.online && !this._closed) this.handleMessage(data);
        }
      }, 0);
    }
  }

  private installHandlers(): void {
    this.ws.onmessage = (ev) => this.handleMessage(asBytes(ev.data));
    this.ws.onclose = () => this.handleClose();
    this.ws.onerror = () => { const ws = this.ws; this.handleClose(); ws.close(); };
  }

  /** Installs a new transport without replacing the typed client. */
  reconnect(handshake: HandshakeResult): boolean {
    if (this._closed) {
      handshake.ws.close();
      return false;
    }
    this.ws = handshake.ws;
    this.online = true;
    this.installHandlers();
    for (const data of handshake.bufferedMessages) this.handleMessage(data);
    if (!this.online) return false;
    const queued = this.queued.splice(0);
    for (const send of queued) {
      if (this._closed) {
        this.queuedRejects.get(send)?.(connectionClosedError());
        this.queuedRejects.delete(send);
      } else if (!this.online) {
        this.queued.push(send);
      } else {
        this.queuedRejects.delete(send);
        send();
      }
    }
    return this.online;
  }

  /** True once explicitly closed; transient disconnects remain reusable. */
  get closed(): boolean {
    return this._closed;
  }

  /** Closes the connection. Safe to call more than once. */
  close(): void {
    if (this._closed) return;
    this._closed = true;
    this.online = false;
    this.onShutdown();
    this.failAllPending(connectionClosedError());
    this.failQueued(connectionClosedError());
    this.detach();
    this.ws.close(1000, "");
    this.onClose();
  }

  /** @internal used by generated method namespaces. */
  protected call<TResp>(
    method: string,
    payload: unknown,
    encodePayload: (value: unknown) => Uint8Array = encodeValue,
    decodePayload: (data: Uint8Array) => unknown = decodeValue,
  ): Promise<TResp> {
    if (this._closed) {
      return Promise.reject(connectionClosedError());
    }
    const id = String(this.nextId++);
    return new Promise<TResp>((resolve, reject) => {
      // Encode before queueing so invalid payloads fail immediately.
      let frame: Uint8Array;
      try {
        frame = encodeEnvelope({ type: "request", id, method, payload: encodePayload(payload) });
      } catch (error) {
        reject(error);
        return;
      }
      const send = () => {
        this.pending.set(id, { resolve: resolve as (v: unknown) => void, reject, decode: decodePayload });
        try {
          this.ws.send(frame);
        } catch (error) {
          this.pending.delete(id);
          reject(error);
          this.handleClose();
        }
      };
      if (this.online) send();
      else {
        this.queued.push(send);
        this.queuedRejects.set(send, reject);
      }
    });
  }

  /** Implemented by the generated subclass to route "event" frames to the
   * right typed EventStream. */
  protected abstract dispatchEvent(env: { event?: string; payload?: Uint8Array }): void;

  private handleMessage(data: Uint8Array): void {
    if (this._closed) return;
    let env: Envelope;
    try {
      env = decodeEnvelope(data);
    } catch {
      this.failAllPending(new LatchError("malformed_frame", "malformed binary frame"));
      const ws = this.ws;
      this.handleClose();
      try {
        ws.close(1002, "malformed binary frame");
      } catch {
        // The socket may already be closed; pending calls are already failed.
      }
      return;
    }
    const payload = env.payload;

    switch (env.type) {
      case "response": {
        const p = env.id ? this.pending.get(env.id) : undefined;
        if (p && env.id) {
          this.pending.delete(env.id);
          try {
            p.resolve(payload && payload.length > 0 ? p.decode(payload) : undefined);
          } catch (error) {
            p.reject(error);
          }
        }
        break;
      }
      case "error": {
        const p = env.id ? this.pending.get(env.id) : undefined;
        if (p && env.id) {
          this.pending.delete(env.id);
          p.reject(new LatchError(env.errorCode ?? "internal_error", env.error ?? "internal error"));
        }
        break;
      }
      case "event":
        try {
          this.dispatchEvent({ event: env.event, payload });
        } catch {
          // An invalid event is isolated from the WebSocket callback and RPCs.
        }
        break;
      case "connection_error": {
        const error = new LatchError(env.errorCode ?? "internal_error", env.error ?? "connection error");
        this.failAllPending(error);
        const ws = this.ws;
        this.handleClose();
        try {
          ws.close(1000, "connection error");
        } catch {
          // The socket may already be closed.
        }
        break;
      }
      default:
        break;
    }
  }

  private handleClose(): void {
    if (this._closed || !this.online) return;
    this.online = false;
    this.detach();
    this.failAllPending(connectionClosedError());
    this.onClose();
  }

  private detach(): void {
    this.ws.onopen = null;
    this.ws.onclose = null;
    this.ws.onerror = null;
    this.ws.onmessage = null;
  }

  private failQueued(err: Error): void {
    for (const send of this.queued) this.queuedRejects.get(send)?.(err);
    this.queued = [];
    this.queuedRejects.clear();
  }

  private failAllPending(err: Error): void {
    for (const [, p] of this.pending) {
      p.reject(err);
    }
    this.pending.clear();
  }
}

function defaultWebSocketFactory(url: string): WebSocketLike {
  const g = globalThis as unknown as { WebSocket?: new (url: string) => WebSocketLike };
  if (!g.WebSocket) {
    throw new Error(
      "Latch: no global WebSocket found; pass options.webSocketFactory (e.g. from the 'ws' package on Node)."
    );
  }
  return new g.WebSocket(url);
}

/** The live socket plus any frames that arrived before BaseConnection could
 * install its message handler. */
export interface HandshakeResult {
  ws: WebSocketLike;
  bufferedMessages: Uint8Array[];
}

/** Opens the WebSocket and resolves once it is open. Latch has no client
 * handshake; the server runs OnConnect for the HTTP upgrade request. */
export function connectSocket(
  url: string,
  factory: WebSocketFactory | undefined,
  version: string,
  signal?: AbortSignal
): Promise<HandshakeResult> {
  const target = new URL(url);
  target.searchParams.set("version", version);
  const ws = (factory ?? defaultWebSocketFactory)(target.toString());
  ws.binaryType = "arraybuffer";
  const bufferedMessages: Uint8Array[] = [];

  return new Promise<HandshakeResult>((resolve, reject) => {
    let settled = false;
    let opened = false;
    const abort = () => {
      if (settled) return;
      settled = true;
      ws.onopen = ws.onclose = ws.onerror = ws.onmessage = null;
      ws.close();
      reject(connectionClosedError());
    };
    if (signal?.aborted) { abort(); return; }
    signal?.addEventListener("abort", abort, { once: true });
    const cleanup = () => signal?.removeEventListener("abort", abort);

    ws.onopen = () => {
      opened = true;
      ws.onopen = null;
      // Yield once before resolving. A connection_error can be delivered in
      // the same turn as open and must reject connect() rather than racing the
      // caller into constructing a connected client.
      setTimeout(() => {
        if (settled) return;
        settled = true;
        cleanup();
        resolve({ ws, bufferedMessages });
      }, 0);
    };

    ws.onerror = () => {
      if (settled) return;
      settled = true;
      cleanup();
      ws.close();
      reject(new Error("Latch: WebSocket connection failed"));
    };

    ws.onclose = () => {
      if (settled) return;
      settled = true;
      cleanup();
      reject(new Error("Latch: connection closed before setup completed"));
    };

    ws.onmessage = (ev) => {
      const data = asBytes(ev.data);
      let env: Envelope;
      try {
        env = decodeEnvelope(data);
      } catch {
        if (!settled) {
          settled = true;
          cleanup();
          ws.close(1002, "malformed binary event before setup");
          reject(new Error("Latch: malformed binary response during setup"));
        } else {
          // BaseConnection will report malformed post-open frames after it
          // installs its handler. Keep this frame for that handler.
          bufferedMessages.push(data);
        }
        return;
      }

      // Inspect connection_error before ordinary buffering. In particular,
      // this covers a frame raced with the open transition.
      if (env.type === "connection_error") {
        if (!settled) {
          settled = true;
          cleanup();
          ws.close(1000, "connection rejected");
          reject(new LatchError(env.errorCode ?? "connect_rejected", env.error ?? "connection rejected"));
          return;
        }
      }

      if (settled) {
        // The socket is open, but BaseConnection has not installed its message
        // handler. Buffer binary frames for replay.
        bufferedMessages.push(data);
        return;
      }
      if (opened) {
        bufferedMessages.push(data);
      }
    };
  });
}
`
