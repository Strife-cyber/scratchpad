"""Minimal REST client for the Scratchpad engine.

Hand-written from the OpenAPI 3.0 spec served at /swagger.json (and
/openapi.json) — improvement-plan item 9. Uses only the standard library.

Only the documented REST surface is exposed. The REST API validates six
action types today (navigate, observe, click, type, scroll, wait); a raw
protocol.ActionRequest body is passed through to the engine, but full parity
is a later milestone. Session listing has no REST endpoint yet.
"""

from __future__ import annotations

import json
import urllib.error
import urllib.parse
import urllib.request
from typing import Any, Dict, List, Optional, Tuple

from .models import ErrorResponse, Observation

#: The action types the REST API validates and documents today.
DOCUMENTED_ACTIONS = ("navigate", "observe", "click", "type", "scroll", "wait")


class ScratchpadError(Exception):
    """Raised when the server returns a typed ErrorResponse envelope.

    Exposes the protocol fields for programmatic handling: ``code`` (stable
    machine code), ``message``, ``hint`` (what to try next), ``request_id``
    (correlation id), ``error_type`` (fatal | action | warning), and the raw
    HTTP ``status``.
    """

    def __init__(
        self,
        message: str,
        *,
        code: str = "",
        hint: str = "",
        request_id: str = "",
        error_type: str = "",
        status: int = 0,
    ) -> None:
        super().__init__(message)
        self.message = message
        self.code = code
        self.hint = hint
        self.request_id = request_id
        self.error_type = error_type
        self.status = status


class ScratchpadClient:
    """Client for the Scratchpad engine's documented REST surface."""

    def __init__(self, base_url: str = "http://localhost:8080", timeout: float = 60.0) -> None:
        self.base_url = base_url.rstrip("/")
        self.timeout = timeout
        self._session_id: Optional[str] = None

    # ------------------------------------------------------------------
    # Transport
    # ------------------------------------------------------------------

    def _request(
        self,
        method: str,
        path: str,
        body: Optional[Dict[str, Any]] = None,
        *,
        binary: bool = False,
    ) -> Tuple[int, Any, Any]:
        """Send a request and return (status, headers, parsed_json_or_bytes).

        Raises ScratchpadError on any non-2xx response, surfacing the typed
        ErrorResponse envelope when the body is JSON.
        """
        url = self.base_url + path
        data: Optional[bytes] = None
        headers = {"Accept": "application/json"}
        if body is not None:
            data = json.dumps(body).encode("utf-8")
            headers["Content-Type"] = "application/json"

        req = urllib.request.Request(url, data=data, headers=headers, method=method)
        try:
            with urllib.request.urlopen(req, timeout=self.timeout) as resp:
                raw = resp.read()
                if binary:
                    return resp.status, resp.headers, raw
                if not raw:
                    return resp.status, resp.headers, None
                return resp.status, resp.headers, json.loads(raw.decode("utf-8"))
        except urllib.error.HTTPError as exc:
            raw = exc.read()
            try:
                payload = json.loads(raw.decode("utf-8"))
            except (ValueError, UnicodeDecodeError):
                payload = None
            if isinstance(payload, dict):
                env = ErrorResponse.from_dict(payload)
                raise ScratchpadError(
                    env.message or f"HTTP {exc.code}",
                    code=env.code,
                    hint=env.hint,
                    request_id=env.request_id,
                    error_type=env.type,
                    status=exc.code,
                ) from None
            raise ScratchpadError(
                f"HTTP {exc.code}: {raw.decode('utf-8', errors='replace')}",
                status=exc.code,
            ) from None

    def _session_id(self, session_id: Optional[str] = None) -> str:
        sid = session_id or self._session_id
        if not sid:
            raise ValueError("no session_id: call create_session() or pass session_id=")
        return urllib.parse.quote(sid)

    # ------------------------------------------------------------------
    # Session lifecycle
    # ------------------------------------------------------------------

    def create_session(
        self,
        headless: bool = False,
        platform: str = "web",
        kind: str = "chrome",
    ) -> str:
        """POST /api/v1/sessions — create a session and return its id."""
        _, _, payload = self._request(
            "POST",
            "/api/v1/sessions",
            {"headless": headless, "platform": platform, "kind": kind},
        )
        sid = payload["sessionId"]
        self._session_id = sid
        return sid

    def delete_session(self, session_id: Optional[str] = None) -> None:
        """DELETE /api/v1/sessions/{id} — close the session."""
        self._request("DELETE", f"/api/v1/sessions/{self._session_id(session_id)}")

    def list_sessions(self) -> List[str]:
        """List sessions.

        Not implemented: the REST API exposes no session-listing endpoint in
        this wave (improvement-plan item 9). This stub exists so callers
        discover the gap instead of guessing at a URL. Watch
        GET /healthz ``sessions.active`` for a liveness count meanwhile.
        """
        raise NotImplementedError(
            "the REST API does not expose session listing yet; "
            "see GET /healthz sessions.active for a liveness count"
        )

    # ------------------------------------------------------------------
    # Actions (the six documented REST actions)
    # ------------------------------------------------------------------

    def run_action(
        self,
        action: str,
        session_id: Optional[str] = None,
        **kwargs: Any,
    ) -> Observation:
        """POST /api/v1/sessions/{id}/actions.

        ``action`` must be one of the six documented REST actions:
        navigate, observe, click, type, scroll, wait.
        """
        if action not in DOCUMENTED_ACTIONS:
            raise ValueError(
                f"action {action!r} is not a documented REST action; "
                f"supported: {', '.join(DOCUMENTED_ACTIONS)}. "
                "The full protocol.ActionRequest surface is passed through "
                "by the server but not validated/documented yet."
            )
        sid = self._session_id(session_id)
        payload: Dict[str, Any] = {"type": action}
        payload.update(kwargs)
        _, _, data = self._request(
            "POST", f"/api/v1/sessions/{sid}/actions", {"action": payload}
        )
        return Observation.from_dict(data)

    def navigate(self, url: str, session_id: Optional[str] = None) -> Observation:
        return self.run_action("navigate", session_id, url=url)

    def observe(self, session_id: Optional[str] = None) -> Observation:
        return self.run_action("observe", session_id)

    def click(
        self,
        x: int,
        y: int,
        session_id: Optional[str] = None,
        timeout_ms: Optional[int] = None,
    ) -> Observation:
        kwargs: Dict[str, Any] = {"x": x, "y": y}
        if timeout_ms is not None:
            kwargs["timeout_ms"] = timeout_ms
        return self.run_action("click", session_id, **kwargs)

    def type_text(self, text: str, session_id: Optional[str] = None) -> Observation:
        return self.run_action("type", session_id, text=text)

    def scroll(
        self,
        x: int = 0,
        y: int = 0,
        delta_x: int = 0,
        delta_y: int = 0,
        session_id: Optional[str] = None,
        timeout_ms: Optional[int] = None,
    ) -> Observation:
        kwargs: Dict[str, Any] = {"x": x, "y": y, "delta_x": delta_x, "delta_y": delta_y}
        if timeout_ms is not None:
            kwargs["timeout_ms"] = timeout_ms
        return self.run_action("scroll", session_id, **kwargs)

    def wait(
        self,
        timeout_ms: int,
        session_id: Optional[str] = None,
    ) -> Observation:
        return self.run_action("wait", session_id, timeout_ms=timeout_ms)

    # ------------------------------------------------------------------
    # Per-session data
    # ------------------------------------------------------------------

    def get_har(self, session_id: Optional[str] = None) -> Dict[str, Any]:
        """GET /api/v1/sessions/{id}/har — captured network traffic."""
        _, _, data = self._request("GET", f"/api/v1/sessions/{self._session_id(session_id)}/har")
        return data

    def get_dom(self, session_id: Optional[str] = None) -> str:
        """GET /api/v1/sessions/{id}/dom — current page DOM as HTML."""
        _, _, raw = self._request(
            "GET", f"/api/v1/sessions/{self._session_id(session_id)}/dom", binary=True
        )
        return raw.decode("utf-8")

    def get_console(
        self,
        limit: Optional[int] = None,
        session_id: Optional[str] = None,
    ) -> Dict[str, Any]:
        """GET /api/v1/sessions/{id}/console — console log ring buffer."""
        path = f"/api/v1/sessions/{self._session_id(session_id)}/console"
        if limit is not None:
            path += f"?limit={int(limit)}"
        _, _, data = self._request("GET", path)
        return data

    def get_screenshot(
        self,
        format: str = "jpeg",
        full_page: bool = False,
        session_id: Optional[str] = None,
    ) -> bytes:
        """GET /api/v1/sessions/{id}/screenshot — raw image bytes."""
        sid = self._session_id(session_id)
        path = f"/api/v1/sessions/{sid}/screenshot"
        path += f"?format={urllib.parse.quote(format)}&fullPage={str(full_page).lower()}"
        _, _, raw = self._request("GET", path, binary=True)
        return raw

    def screenshot_diff(
        self,
        expected_base64: str,
        tolerance: Optional[int] = None,
        session_id: Optional[str] = None,
    ) -> Dict[str, Any]:
        """POST /api/v1/sessions/{id}/screenshot/diff — perceptual diff."""
        body: Dict[str, Any] = {"expected_base64": expected_base64}
        if tolerance is not None:
            body["tolerance"] = tolerance
        _, _, data = self._request(
            "POST", f"/api/v1/sessions/{self._session_id(session_id)}/screenshot/diff", body
        )
        return data

    def start_recording(
        self,
        dir: Optional[str] = None,
        session_id: Optional[str] = None,
    ) -> Dict[str, str]:
        """POST /api/v1/sessions/{id}/recording/start."""
        body = {"dir": dir} if dir else None
        _, _, data = self._request(
            "POST", f"/api/v1/sessions/{self._session_id(session_id)}/recording/start", body
        )
        return data

    def stop_recording(self, session_id: Optional[str] = None) -> bytes:
        """POST /api/v1/sessions/{id}/recording/stop — webm bytes."""
        _, _, raw = self._request(
            "POST", f"/api/v1/sessions/{self._session_id(session_id)}/recording/stop", binary=True
        )
        return raw

    def start_tracing(
        self,
        dir: Optional[str] = None,
        session_id: Optional[str] = None,
    ) -> Dict[str, str]:
        """POST /api/v1/sessions/{id}/tracing/start."""
        body = {"dir": dir} if dir else None
        _, _, data = self._request(
            "POST", f"/api/v1/sessions/{self._session_id(session_id)}/tracing/start", body
        )
        return data

    def stop_tracing(self, session_id: Optional[str] = None) -> bytes:
        """POST /api/v1/sessions/{id}/tracing/stop — gzipped trace JSON."""
        _, _, raw = self._request(
            "POST", f"/api/v1/sessions/{self._session_id(session_id)}/tracing/stop", binary=True
        )
        return raw

    # ------------------------------------------------------------------
    # Observability
    # ------------------------------------------------------------------

    def healthz(self) -> Dict[str, Any]:
        """GET /healthz — readiness probe."""
        _, _, data = self._request("GET", "/healthz")
        return data

    def version(self) -> Dict[str, Any]:
        """GET /version — build info."""
        _, _, data = self._request("GET", "/version")
        return data

    def metrics(self) -> str:
        """GET /metrics — Prometheus text exposition format."""
        _, _, raw = self._request("GET", "/metrics", binary=True)
        return raw.decode("utf-8")
