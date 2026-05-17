export type Selector = { css?: string };

type Json = any;

export type SessionOptions = {
  headless?: boolean;
};

export class ScratchpadClient {
  baseUrl: string;

  constructor(baseUrl: string) {
    this.baseUrl = baseUrl.replace(/\/$/, "");
  }

  async createSession(opts: SessionOptions = {}): Promise<string> {
    const res = await fetch(`${this.baseUrl}/api/v1/sessions`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ headless: opts.headless ?? true }),
    });
    if (!res.ok) throw new Error(`createSession failed: ${res.status}`);
    const body = await res.json();
    return body.sessionId as string;
  }

  async deleteSession(sessionId: string): Promise<void> {
    await fetch(`${this.baseUrl}/api/v1/sessions/${sessionId}`, { method: "DELETE" });
  }

  async runAction<T = any>(sessionId: string, action: Json): Promise<T> {
    const res = await fetch(`${this.baseUrl}/api/v1/sessions/${sessionId}/actions`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      // Phase 0 handler supports unwrapped protocol.ActionRequest / InitializeRequest.
      body: JSON.stringify(action),
    });
    if (!res.ok) throw new Error(`action failed: ${res.status}`);
    return (await res.json()) as T;
  }

  async page(sessionId: string): Promise<ScratchpadPage> {
    return new ScratchpadPage(this, sessionId);
  }
}

export class ScratchpadPage {
  client: ScratchpadClient;
  sessionId: string;

  constructor(client: ScratchpadClient, sessionId: string) {
    this.client = client;
    this.sessionId = sessionId;
  }

  async goto(url: string): Promise<void> {
    await this.client.runAction(this.sessionId, {
      url,
      viewport: { width: 0, height: 0 },
    });
  }

  async click(selector: string, timeoutMs = 10_000): Promise<void> {
    await this.client.runAction(this.sessionId, {
      action: "click",
      selector: { css: selector },
      timeout_ms: timeoutMs,
    });
  }

  async type(selector: string, text: string, timeoutMs = 10_000): Promise<void> {
    await this.client.runAction(this.sessionId, {
      action: "type",
      selector: { css: selector },
      text,
      timeout_ms: timeoutMs,
    });
  }

  async waitForSelector(selector: string, timeoutMs = 5_000): Promise<void> {
    await this.client.runAction(this.sessionId, {
      action: "wait",
      condition: "selector_visible",
      selector: { css: selector },
      timeout_ms: timeoutMs,
    });
  }

  async assertTextContains(selector: string, text: string): Promise<void> {
    const obs = await this.client.runAction(this.sessionId, {
      action: "assert",
      assertion: {
        type: "text_contains",
        selector: { css: selector },
        text,
      },
    });
    const ar = obs?.assertion_result;
    if (!ar?.success) {
      throw new Error(ar?.message ?? "assertTextContains failed");
    }
  }
}

