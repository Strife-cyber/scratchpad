/**
 * Minimal REST client for the Scratchpad engine.
 *
 * Hand-written from the OpenAPI 3.0 spec served at /swagger.json (and
 * /openapi.json) — improvement-plan item 9. Uses the standard `fetch` API,
 * so it runs in browsers and Node 18+ without dependencies.
 *
 * Only the documented REST surface is exposed. The REST API validates six
 * action types today (navigate, observe, click, type, scroll, wait); a raw
 * protocol.ActionRequest body is passed through to the engine, but full
 * parity is a later milestone. Session listing has no REST endpoint yet.
 */

import {
  ActionEnvelope,
  ConsoleResponse,
  ErrorResponse,
  HealthzResponse,
  Observation,
  SessionCreateResponse,
  VersionResponse,
} from "./models";

/** The action types the REST API validates and documents today. */
export const DOCUMENTED_ACTIONS = [
  "navigate",
  "observe",
  "click",
  "type",
  "scroll",
  "wait",
] as const;
export type DocumentedAction = (typeof DOCUMENTED_ACTIONS)[number];

/** Raised when the server returns a typed ErrorResponse envelope. */
export class ScratchpadError extends Error {
  readonly code: string;
  readonly hint: string;
  readonly requestId: string;
  readonly errorType: string;
  readonly status: number;

  constructor(env: ErrorResponse, status: number) {
    super(env.message || `HTTP ${status}`);
    this.name = "ScratchpadError";
    this.code = env.code ?? "";
    this.hint = env.hint ?? "";
    this.requestId = env.request_id ?? "";
    this.errorType = env.type;
    this.status = status;
  }
}

type ResponseMeta = { status: number; headers: Headers };

/**
 * Client for the Scratchpad engine's documented REST surface. One client may
 * drive many sessions; pass an explicit `sessionId` when you hold several.
 */
export class ScratchpadClient {
  readonly baseUrl: string;
  readonly timeoutMs: number;
  private sessionId?: string;

  constructor(baseUrl = "http://localhost:8080", timeoutMs = 60_000) {
    this.baseUrl = baseUrl.replace(/\/+$/, "");
    this.timeoutMs = timeoutMs;
  }

  // ------------------------------------------------------------------
  // Transport
  // ------------------------------------------------------------------

  /**
   * Perform a request. The caller chooses `T`: for JSON responses pass the
   * parsed type; for binary responses pass `Uint8Array` (with `binary: true`).
   */
  private async request<T>(
    method: string,
    path: string,
    body?: unknown,
    binary = false,
  ): Promise<{ meta: ResponseMeta; data: T }> {
    const headers: Record<string, string> = { Accept: "application/json" };
    let payload: BodyInit | undefined;
    if (body !== undefined) {
      headers["Content-Type"] = "application/json";
      payload = JSON.stringify(body);
    }
    const resp = await fetch(this.baseUrl + path, {
      method,
      headers,
      body: payload,
      signal: AbortSignal.timeout(this.timeoutMs),
    });

    if (!resp.ok) {
      let env: ErrorResponse | undefined;
      try {
        env = (await resp.json()) as ErrorResponse;
      } catch {
        /* body was not JSON */
      }
      if (env && typeof env.message === "string") {
        throw new ScratchpadError(env, resp.status);
      }
      throw new ScratchpadError(
        { type: "warning", message: `HTTP ${resp.status}` },
        resp.status,
      );
    }

    if (binary) {
      const buf = new Uint8Array(await resp.arrayBuffer());
      return { meta: { status: resp.status, headers: resp.headers }, data: buf as T };
    }
    const data = (await resp.json()) as T;
    return { meta: { status: resp.status, headers: resp.headers }, data };
  }

  private sessionIdOrThrow(sessionId?: string): string {
    const sid = sessionId ?? this.sessionId;
    if (!sid) {
      throw new Error("no sessionId: call createSession() or pass sessionId");
    }
    return encodeURIComponent(sid);
  }

  // ------------------------------------------------------------------
  // Session lifecycle
  // ------------------------------------------------------------------

  /** POST /api/v1/sessions — create a session, returns its id. */
  async createSession(
    opts: { headless?: boolean; platform?: "web" | "android"; kind?: "chrome" | "android" } = {},
  ): Promise<string> {
    const { data } = await this.request<SessionCreateResponse>("POST", "/api/v1/sessions", {
      headless: opts.headless ?? false,
      platform: opts.platform ?? "web",
      kind: opts.kind ?? "chrome",
    });
    this.sessionId = data.sessionId;
    return data.sessionId;
  }

  /** DELETE /api/v1/sessions/{id} — close the session. */
  async deleteSession(sessionId?: string): Promise<void> {
    await this.request<never>("DELETE", `/api/v1/sessions/${this.sessionIdOrThrow(sessionId)}`);
  }

  /**
   * List sessions — NOT implemented: the REST API exposes no session-listing
   * endpoint in this wave (improvement-plan item 9). This stub exists so
   * callers discover the gap instead of guessing at a URL.
   */
  listSessions(): Promise<string[]> {
    return Promise.reject(
      new Error(
        "the REST API does not expose session listing yet; " +
          "see GET /healthz sessions.active for a liveness count",
      ),
    );
  }

  // ------------------------------------------------------------------
  // Actions (the six documented REST actions)
  // ------------------------------------------------------------------

  /** POST /api/v1/sessions/{id}/actions. */
  async runAction(
    action: DocumentedAction,
    args: Record<string, unknown> = {},
    sessionId?: string,
  ): Promise<Observation> {
    if (!DOCUMENTED_ACTIONS.includes(action)) {
      throw new Error(
        `action ${JSON.stringify(action)} is not a documented REST action; ` +
          `supported: ${DOCUMENTED_ACTIONS.join(", ")}. The full protocol.ActionRequest ` +
          "surface is passed through by the server but not validated/documented yet.",
      );
    }
    const envelope: ActionEnvelope = { action: { type: action, ...args } };
    const { data } = await this.request<Observation>(
      "POST",
      `/api/v1/sessions/${this.sessionIdOrThrow(sessionId)}/actions`,
      envelope,
    );
    return data;
  }

  navigate(url: string, sessionId?: string): Promise<Observation> {
    return this.runAction("navigate", { url }, sessionId);
  }

  observe(sessionId?: string): Promise<Observation> {
    return this.runAction("observe", {}, sessionId);
  }

  click(x: number, y: number, opts: { timeout_ms?: number } = {}, sessionId?: string): Promise<Observation> {
    return this.runAction("click", { x, y, ...opts }, sessionId);
  }

  typeText(text: string, sessionId?: string): Promise<Observation> {
    return this.runAction("type", { text }, sessionId);
  }

  scroll(
    opts: { x?: number; y?: number; delta_x?: number; delta_y?: number; timeout_ms?: number } = {},
    sessionId?: string,
  ): Promise<Observation> {
    return this.runAction("scroll", opts, sessionId);
  }

  wait(timeout_ms: number, sessionId?: string): Promise<Observation> {
    return this.runAction("wait", { timeout_ms }, sessionId);
  }

  // ------------------------------------------------------------------
  // Per-session data
  // ------------------------------------------------------------------

  /** GET /api/v1/sessions/{id}/har — captured network traffic. */
  async getHar(sessionId?: string): Promise<Record<string, unknown>> {
    const { data } = await this.request<Record<string, unknown>>(
      "GET",
      `/api/v1/sessions/${this.sessionIdOrThrow(sessionId)}/har`,
    );
    return data;
  }

  /** GET /api/v1/sessions/{id}/dom — current page DOM as HTML. */
  async getDom(sessionId?: string): Promise<string> {
    const { data } = await this.request<Uint8Array>(
      "GET",
      `/api/v1/sessions/${this.sessionIdOrThrow(sessionId)}/dom`,
      undefined,
      true,
    );
    return new TextDecoder().decode(data as Uint8Array);
  }

  /** GET /api/v1/sessions/{id}/console — console log ring buffer. */
  async getConsole(limit?: number, sessionId?: string): Promise<ConsoleResponse> {
    const qs = limit !== undefined ? `?limit=${limit}` : "";
    const { data } = await this.request<ConsoleResponse>(
      "GET",
      `/api/v1/sessions/${this.sessionIdOrThrow(sessionId)}/console${qs}`,
    );
    return data;
  }

  /** GET /api/v1/sessions/{id}/screenshot — raw image bytes. */
  async getScreenshot(
    opts: { format?: "jpeg" | "png" | "webp"; fullPage?: boolean } = {},
    sessionId?: string,
  ): Promise<Uint8Array> {
    const params = new URLSearchParams({
      format: opts.format ?? "jpeg",
      fullPage: String(opts.fullPage ?? false),
    });
    const { data } = await this.request<Uint8Array>(
      "GET",
      `/api/v1/sessions/${this.sessionIdOrThrow(sessionId)}/screenshot?${params}`,
      undefined,
      true,
    );
    return data;
  }

  /** POST /api/v1/sessions/{id}/screenshot/diff — perceptual diff. */
  async screenshotDiff(
    expectedBase64: string,
    tolerance?: number,
    sessionId?: string,
  ): Promise<{ sessionId: string; assertionResult: unknown }> {
    const body: Record<string, unknown> = { expected_base64: expectedBase64 };
    if (tolerance !== undefined) body.tolerance = tolerance;
    const { data } = await this.request<{ sessionId: string; assertionResult: unknown }>(
      "POST",
      `/api/v1/sessions/${this.sessionIdOrThrow(sessionId)}/screenshot/diff`,
      body,
    );
    return data;
  }

  /** POST /api/v1/sessions/{id}/recording/start. */
  async startRecording(dir?: string, sessionId?: string): Promise<{ sessionId: string; outputPath: string }> {
    const { data } = await this.request<{ sessionId: string; outputPath: string }>(
      "POST",
      `/api/v1/sessions/${this.sessionIdOrThrow(sessionId)}/recording/start`,
      dir !== undefined ? { dir } : undefined,
    );
    return data;
  }

  /** POST /api/v1/sessions/{id}/recording/stop — webm bytes. */
  async stopRecording(sessionId?: string): Promise<Uint8Array> {
    const { data } = await this.request<Uint8Array>(
      "POST",
      `/api/v1/sessions/${this.sessionIdOrThrow(sessionId)}/recording/stop`,
      undefined,
      true,
    );
    return data;
  }

  /** POST /api/v1/sessions/{id}/tracing/start. */
  async startTracing(dir?: string, sessionId?: string): Promise<{ sessionId: string; outputPath: string }> {
    const { data } = await this.request<{ sessionId: string; outputPath: string }>(
      "POST",
      `/api/v1/sessions/${this.sessionIdOrThrow(sessionId)}/tracing/start`,
      dir !== undefined ? { dir } : undefined,
    );
    return data;
  }

  /** POST /api/v1/sessions/{id}/tracing/stop — gzipped trace JSON. */
  async stopTracing(sessionId?: string): Promise<Uint8Array> {
    const { data } = await this.request<Uint8Array>(
      "POST",
      `/api/v1/sessions/${this.sessionIdOrThrow(sessionId)}/tracing/stop`,
      undefined,
      true,
    );
    return data;
  }

  // ------------------------------------------------------------------
  // Observability
  // ------------------------------------------------------------------

  /** GET /healthz — readiness probe. */
  async healthz(): Promise<HealthzResponse> {
    const { data } = await this.request<HealthzResponse>("GET", "/healthz");
    return data;
  }

  /** GET /version — build info. */
  async version(): Promise<VersionResponse> {
    const { data } = await this.request<VersionResponse>("GET", "/version");
    return data;
  }

  /** GET /metrics — Prometheus text exposition format. */
  async metrics(): Promise<string> {
    const { data } = await this.request<Uint8Array>("GET", "/metrics", undefined, true);
    return new TextDecoder().decode(data as Uint8Array);
  }
}
