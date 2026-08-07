/**
 * Typed models mirroring the OpenAPI 3.0 spec served at /swagger.json
 * (improvement-plan item 9). Field names match the wire format exactly.
 */

/** The typed error envelope returned by every transport. */
export interface ErrorResponse {
  /** Correlation id stamped by the request-id middleware. */
  request_id?: string;
  /** Stable machine-readable error code, e.g. `selector_no_match`. */
  code?: string;
  /** fatal | action | warning. */
  type: "fatal" | "action" | "warning" | string;
  message: string;
  /** The action that was being attempted (empty for session-level errors). */
  action?: string;
  /** What the agent should try next. */
  hint?: string;
  /** Base64 JPEG captured at the moment of failure (WebSocket path). */
  screenshot?: string;
}

/** Structured locator: CSS > XPath > text > role > test_id > placeholder. */
export interface Selector {
  css?: string;
  xpath?: string;
  text?: string;
  role?: string;
  test_id?: string;
  placeholder?: string;
}

export interface Viewport {
  width: number;
  height: number;
}

export interface Bounds {
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface ScrollState {
  can_scroll_down: boolean;
  can_scroll_up: boolean;
  current_percentage: number;
}

/** A UI element in the accessibility tree. */
export interface SpatialNode {
  node_id: string;
  role: string;
  name?: string;
  bounds: Bounds;
  scroll_state?: ScrollState;
  children?: SpatialNode[];
  /** True for actionable elements (buttons, links, inputs). */
  interactive?: boolean;
  value?: string;
  description?: string;
}

export interface ConsoleLog {
  level: string;
  message: string;
  /** Unix milliseconds. */
  timestamp: number;
}

export interface PageInfo {
  url: string;
  title: string;
  platform: "web" | "android" | string;
  load_status: string;
  navigation_id: number;
  dialog_state?: string;
  tab_count?: number;
  extra?: Record<string, string>;
}

export interface TabInfo {
  id: string;
  url: string;
  title: string;
  active: boolean;
  opener_id?: string;
}

export interface ActionResult {
  success: boolean;
  action: string;
  error?: string;
  elapsed_ms: number;
  action_metadata?: Record<string, unknown>;
  screenshot?: string;
  element_highlight?: string;
}

export interface AssertionResult {
  success: boolean;
  type?: string;
  message?: string;
  elapsed_ms?: number;
  attempts?: number;
  poll_interval_ms?: number;
  extra?: Record<string, unknown>;
}

/**
 * Snapshot of the current page/screen returned by every action. `type` is
 * "observation" for a full snapshot or "delta" when the server decided a
 * delta was smaller than the full tree.
 */
export interface Observation {
  type: "observation" | "delta" | string;
  visual_context?: string;
  spatial_tree?: SpatialNode[];
  delta?: { added?: SpatialNode[]; removed?: string[]; updated?: SpatialNode[] };
  logs?: ConsoleLog[];
  page_info?: PageInfo;
  tabs?: TabInfo[];
  action_result?: ActionResult;
  assertion_result?: AssertionResult;
}

/** POST /api/v1/sessions response. */
export interface SessionCreateResponse {
  sessionId: string;
}

/** The typed action envelope accepted by the REST actions endpoint. */
export interface ActionEnvelope {
  action: {
    type: "navigate" | "observe" | "click" | "type" | "scroll" | "wait" | string;
    url?: string;
    x?: number;
    y?: number;
    text?: string;
    delta_x?: number;
    delta_y?: number;
    timeout_ms?: number;
  };
}

export interface HealthzResponse {
  status: string;
  uptime_seconds: number;
  version: string;
  sessions: { active: number; by_kind: Record<string, number> };
}

export interface VersionResponse {
  version: string;
  go: string;
  module?: string;
  module_version?: string;
}

export interface ConsoleResponse {
  sessionId: string;
  logs: ConsoleLog[];
}
