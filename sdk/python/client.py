import json
import os
from dataclasses import dataclass
from typing import Any, Dict, Optional

import urllib.request
import urllib.parse


def _post_json(url: str, payload: Dict[str, Any]) -> Dict[str, Any]:
    data = json.dumps(payload).encode("utf-8")
    req = urllib.request.Request(
        url,
        data=data,
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    with urllib.request.urlopen(req) as resp:
        body = resp.read().decode("utf-8")
        return json.loads(body) if body else {}


def _delete(url: str) -> None:
    req = urllib.request.Request(url, method="DELETE")
    with urllib.request.urlopen(req) as resp:
        _ = resp.read()


@dataclass
class SessionOptions:
    headless: bool = True


class ScratchpadClient:
    def __init__(self, base_url: str):
        self.base_url = base_url.rstrip("/")

    def create_session(self, opts: SessionOptions = SessionOptions()) -> str:
        res = _post_json(f"{self.base_url}/api/v1/sessions", {"headless": opts.headless})
        return res["sessionId"]

    def delete_session(self, session_id: str) -> None:
        _delete(f"{self.base_url}/api/v1/sessions/{session_id}")

    def page(self, session_id: str) -> "ScratchpadPage":
        return ScratchpadPage(self, session_id)

    def run_action(self, session_id: str, action: Dict[str, Any]) -> Dict[str, Any]:
        res = _post_json(
            f"{self.base_url}/api/v1/sessions/{session_id}/actions",
            action,
        )
        return res


class ScratchpadPage:
    def __init__(self, client: ScratchpadClient, session_id: str):
        self.client = client
        self.session_id = session_id

    def goto(self, url: str) -> None:
        self.client.run_action(
            self.session_id,
            {"url": url, "viewport": {"width": 0, "height": 0}},
        )

    def click(self, selector: str, timeout_ms: int = 10_000) -> None:
        self.client.run_action(
            self.session_id,
            {
                "action": "click",
                "selector": {"css": selector},
                "timeout_ms": timeout_ms,
            },
        )

    def type(self, selector: str, text: str, timeout_ms: int = 10_000) -> None:
        self.client.run_action(
            self.session_id,
            {
                "action": "type",
                "selector": {"css": selector},
                "text": text,
                "timeout_ms": timeout_ms,
            },
        )

    def wait_for_selector(self, selector: str, timeout_ms: int = 5_000) -> None:
        self.client.run_action(
            self.session_id,
            {
                "action": "wait",
                "condition": "selector_visible",
                "selector": {"css": selector},
                "timeout_ms": timeout_ms,
            },
        )

    def assert_text_contains(self, selector: str, text: str) -> None:
        obs = self.client.run_action(
            self.session_id,
            {
                "action": "assert",
                "assertion": {
                    "type": "text_contains",
                    "selector": {"css": selector},
                    "text": text,
                },
            },
        )
        ar = obs.get("assertion_result")
        if not ar or not ar.get("success", False):
            raise RuntimeError(ar.get("message") if ar else "assertion failed")

