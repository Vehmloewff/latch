package typescript

// runtimeBody is the language runtime shared by every generated TypeScript
// client: WebSocket transport, the connect handshake, request-ID
// correlation, and event dispatch. It never depends on any particular
// protocol, so it is emitted verbatim rather than templated.
const runtimeBody = `export type WebSocketFactory = (url: string) => WebSocketLike;

/** The minimal WebSocket surface Latchwire's runtime needs, satisfied by
 * both the browser's global WebSocket and Node/Bun/Deno implementations. */
export interface WebSocketLike {
  readonly readyState: number;
  onopen: ((ev: unknown) => void) | null;
  onclose: ((ev: unknown) => void) | null;
  onerror: ((ev: unknown) => void) | null;
  onmessage: ((ev: { data: string }) => void) | null;
  send(data: string): void;
  close(code?: number, reason?: string): void;
}

/** Thrown for every RPC rejection and connect failure. */
export class LatchwireError extends Error {
  readonly code: string;

  constructor(code: string, message: string) {
    super(message);
    this.code = code;
    this.name = "LatchwireError";
  }
}

export interface ClientOptions {
  /** The WebSocket URL to connect to, e.g. "wss://example.com/ws". */
  url: string;
  /** Supply a WebSocket implementation for environments with no global
   * WebSocket (older Node versions, some test runners). Defaults to
   * globalThis.WebSocket. */
  webSocketFactory?: WebSocketFactory;
}

interface WireEnvelope {
  type: string;
  protocol?: string;
  version?: string;
  id?: string;
  method?: string;
  event?: string;
  payload?: unknown;
  error?: { code: string; message: string };
}

interface PendingRequest {
  resolve: (value: unknown) => void;
  reject: (err: unknown) => void;
}

/** A single typed server-to-client event stream. Listener callbacks are
 * always scheduled as microtasks, so a slow or throwing listener can never
 * block delivery of the next incoming WebSocket message. */
export class EventStream<T> {
  private listeners = new Set<(event: T) => void>();

  /** Registers listener and returns a function that unsubscribes it. */
  subscribe(listener: (event: T) => void): () => void {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  }

  /** @internal */
  _emit(event: T): void {
    for (const listener of Array.from(this.listeners)) {
      queueMicrotask(() => listener(event));
    }
  }
}

const connectionClosedError = () => new LatchwireError("connection_closed", "the connection is closed");

/** Base class for every generated "Connected*Client". Handles the
 * WebSocket message loop, request/response correlation by ID, and
 * dispatch of connection_error / close conditions. Generated subclasses
 * add typed method namespaces (backed by call()) and typed event streams
 * (backed by dispatchEvent()). */
export abstract class BaseConnection {
  private ws: WebSocketLike;
  private nextId = 1;
  private pending = new Map<string, PendingRequest>();
  private _closed = false;

  constructor(handshake: HandshakeResult) {
    this.ws = handshake.ws;
    // Install the real handler before replaying anything, so a message
    // that arrives while we're replaying is still queued in order rather
    // than raced against this constructor.
    this.ws.onmessage = (ev) => this.handleMessage(ev.data);
    this.ws.onclose = () => this.handleClose();
    this.ws.onerror = () => {
      /* surfaced to callers via rejected/failed pending requests */
    };
    // The server may have sent events immediately after "connected" (e.g.
    // from OnConnect); those can arrive in the same underlying read as the
    // handshake response, before this constructor ever runs. Replay them
    // in order, deferred with setTimeout (a macrotask) rather than
    // queueMicrotask: the caller's own code right after awaiting connect()
    // - most importantly a synchronous events.x.subscribe(...) call - runs
    // as a microtask continuation of that same await, which would still
    // run after a microtask queued from inside this constructor but
    // before a macrotask. Only a macrotask reliably comes after the
    // caller has had a chance to subscribe.
    if (handshake.bufferedMessages.length > 0) {
      const buffered = handshake.bufferedMessages;
      setTimeout(() => {
        for (const data of buffered) {
          this.handleMessage(data);
        }
      }, 0);
    }
  }

  /** True once the connection has closed, for any reason. */
  get closed(): boolean {
    return this._closed;
  }

  /** Closes the connection. Safe to call more than once. */
  close(): void {
    if (this._closed) return;
    this.ws.close(1000, "");
    this.handleClose();
  }

  /** @internal used by generated method namespaces. */
  protected call<TResp>(method: string, payload: unknown): Promise<TResp> {
    if (this._closed) {
      return Promise.reject(connectionClosedError());
    }
    const id = String(this.nextId++);
    return new Promise<TResp>((resolve, reject) => {
      this.pending.set(id, { resolve: resolve as (v: unknown) => void, reject });
      this.ws.send(JSON.stringify({ type: "request", id, method, payload }));
    });
  }

  /** Implemented by the generated subclass to route "event" frames to the
   * right typed EventStream. */
  protected abstract dispatchEvent(env: { event?: string; payload?: unknown }): void;

  private handleMessage(data: string): void {
    let env: WireEnvelope;
    try {
      env = JSON.parse(data);
    } catch {
      return;
    }

    switch (env.type) {
      case "response": {
        const p = env.id ? this.pending.get(env.id) : undefined;
        if (p && env.id) {
          this.pending.delete(env.id);
          p.resolve(env.payload);
        }
        break;
      }
      case "error": {
        const p = env.id ? this.pending.get(env.id) : undefined;
        if (p && env.id) {
          this.pending.delete(env.id);
          p.reject(new LatchwireError(env.error?.code ?? "internal_error", env.error?.message ?? "internal error"));
        }
        break;
      }
      case "event":
        this.dispatchEvent(env);
        break;
      case "connection_error":
        this.failAllPending(new LatchwireError(env.error?.code ?? "internal_error", env.error?.message ?? "connection error"));
        this.handleClose();
        break;
      default:
        break;
    }
  }

  private handleClose(): void {
    if (this._closed) return;
    this._closed = true;
    this.failAllPending(connectionClosedError());
  }

  private failAllPending(err: LatchwireError): void {
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
      "Latchwire: no global WebSocket found; pass options.webSocketFactory (e.g. from the 'ws' package on Node)."
    );
  }
  return new g.WebSocket(url);
}

/** The live socket plus any frames that arrived after "connected" but
 * before the caller (BaseConnection's constructor) could install its own
 * message handler — e.g. an event sent from OnConnect can share the same
 * underlying read as the handshake response. BaseConnection replays these,
 * in order, so no frame is ever silently dropped by the handshake race. */
export interface HandshakeResult {
  ws: WebSocketLike;
  bufferedMessages: string[];
}

/** Performs the WebSocket connect + Latchwire handshake, resolving only
 * after the server's "connected" frame arrives (or rejecting with the
 * connection's connection_error). Used by every generated Client.connect(). */
export function connectSocket(
  url: string,
  factory: WebSocketFactory | undefined,
  protocolName: string,
  protocolVersion: string,
  payload: unknown
): Promise<HandshakeResult> {
  const ws = (factory ?? defaultWebSocketFactory)(url);
  const bufferedMessages: string[] = [];

  return new Promise<HandshakeResult>((resolve, reject) => {
    let settled = false;

    ws.onopen = () => {
      ws.send(
        JSON.stringify({
          type: "connect",
          protocol: protocolName,
          version: protocolVersion,
          payload,
        })
      );
    };

    ws.onerror = () => {
      if (settled) return;
      settled = true;
      reject(new Error("Latchwire: WebSocket connection failed"));
    };

    ws.onclose = () => {
      if (settled) return;
      settled = true;
      reject(new Error("Latchwire: connection closed before the handshake completed"));
    };

    ws.onmessage = (ev) => {
      if (settled) {
        // The handshake already resolved, but BaseConnection hasn't
        // installed its own handler yet (same synchronous read as
        // "connected", or a still-pending microtask). Buffer for replay.
        bufferedMessages.push(ev.data);
        return;
      }

      let env: WireEnvelope;
      try {
        env = JSON.parse(ev.data);
      } catch {
        settled = true;
        ws.close(1002, "malformed handshake response");
        reject(new Error("Latchwire: malformed response during connect"));
        return;
      }

      if (env.type === "connected") {
        settled = true;
        ws.onopen = null;
        ws.onerror = null;
        ws.onclose = null;
        // Leave onmessage installed (see the settled branch above) until
        // BaseConnection takes over.
        resolve({ ws, bufferedMessages });
      } else if (env.type === "connection_error") {
        settled = true;
        ws.close();
        reject(new LatchwireError(env.error?.code ?? "internal_error", env.error?.message ?? "connection rejected"));
      }
    };
  });
}
`
