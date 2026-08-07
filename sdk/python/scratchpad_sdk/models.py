"""Typed models mirroring the OpenAPI 3.0 spec (internal/docs/swagger.json).

Each model exposes a ``from_dict`` classmethod that is tolerant of missing or
unknown fields so the client stays forward-compatible with the server.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Dict, List, Optional


@dataclass
class ErrorResponse:
    """The typed error envelope returned by every transport.

    Carries the fields the protocol defines for programmatic error handling:
    ``code`` (stable machine code), ``message``, ``hint``, ``request_id``,
    and ``type`` (fatal | action | warning).
    """

    type: str = ""
    message: str = ""
    code: str = ""
    hint: str = ""
    request_id: str = ""
    action: str = ""
    screenshot: str = ""

    @classmethod
    def from_dict(cls, d: Optional[Dict[str, Any]]) -> "ErrorResponse":
        if not d:
            return cls()
        return cls(
            type=d.get("type", ""),
            message=d.get("message", ""),
            code=d.get("code", ""),
            hint=d.get("hint", ""),
            request_id=d.get("request_id", ""),
            action=d.get("action", ""),
            screenshot=d.get("screenshot", ""),
        )


@dataclass
class Bounds:
    x: float = 0.0
    y: float = 0.0
    width: float = 0.0
    height: float = 0.0

    @classmethod
    def from_dict(cls, d: Optional[Dict[str, Any]]) -> "Bounds":
        if not d:
            return cls()
        return cls(
            x=d.get("x", 0.0),
            y=d.get("y", 0.0),
            width=d.get("width", 0.0),
            height=d.get("height", 0.0),
        )


@dataclass
class ScrollState:
    can_scroll_down: bool = False
    can_scroll_up: bool = False
    current_percentage: int = 0

    @classmethod
    def from_dict(cls, d: Optional[Dict[str, Any]]) -> "ScrollState":
        if not d:
            return cls()
        return cls(
            can_scroll_down=d.get("can_scroll_down", False),
            can_scroll_up=d.get("can_scroll_up", False),
            current_percentage=d.get("current_percentage", 0),
        )


@dataclass
class SpatialNode:
    """A UI element in the accessibility tree."""

    node_id: str = ""
    role: str = ""
    name: str = ""
    bounds: Bounds = field(default_factory=Bounds)
    scroll_state: ScrollState = field(default_factory=ScrollState)
    children: List["SpatialNode"] = field(default_factory=list)
    interactive: bool = False
    value: str = ""
    description: str = ""

    @classmethod
    def from_dict(cls, d: Optional[Dict[str, Any]]) -> "SpatialNode":
        if not d:
            return cls()
        return cls(
            node_id=d.get("node_id", ""),
            role=d.get("role", ""),
            name=d.get("name", ""),
            bounds=Bounds.from_dict(d.get("bounds")),
            scroll_state=ScrollState.from_dict(d.get("scroll_state")),
            children=[cls.from_dict(c) for c in d.get("children", []) or []],
            interactive=d.get("interactive", False),
            value=d.get("value", ""),
            description=d.get("description", ""),
        )


@dataclass
class ConsoleLog:
    level: str = ""
    message: str = ""
    timestamp: int = 0

    @classmethod
    def from_dict(cls, d: Optional[Dict[str, Any]]) -> "ConsoleLog":
        if not d:
            return cls()
        return cls(
            level=d.get("level", ""),
            message=d.get("message", ""),
            timestamp=d.get("timestamp", 0),
        )


@dataclass
class PageInfo:
    url: str = ""
    title: str = ""
    platform: str = ""
    load_status: str = ""
    navigation_id: int = 0
    dialog_state: str = ""
    tab_count: int = 0

    @classmethod
    def from_dict(cls, d: Optional[Dict[str, Any]]) -> "PageInfo":
        if not d:
            return cls()
        return cls(
            url=d.get("url", ""),
            title=d.get("title", ""),
            platform=d.get("platform", ""),
            load_status=d.get("load_status", ""),
            navigation_id=d.get("navigation_id", 0),
            dialog_state=d.get("dialog_state", ""),
            tab_count=d.get("tab_count", 0),
        )


@dataclass
class TabInfo:
    id: str = ""
    url: str = ""
    title: str = ""
    active: bool = False
    opener_id: str = ""

    @classmethod
    def from_dict(cls, d: Optional[Dict[str, Any]]) -> "TabInfo":
        if not d:
            return cls()
        return cls(
            id=d.get("id", ""),
            url=d.get("url", ""),
            title=d.get("title", ""),
            active=d.get("active", False),
            opener_id=d.get("opener_id", ""),
        )


@dataclass
class ActionResult:
    success: bool = False
    action: str = ""
    error: str = ""
    elapsed_ms: int = 0
    action_metadata: Dict[str, Any] = field(default_factory=dict)

    @classmethod
    def from_dict(cls, d: Optional[Dict[str, Any]]) -> "ActionResult":
        if not d:
            return cls()
        return cls(
            success=d.get("success", False),
            action=d.get("action", ""),
            error=d.get("error", ""),
            elapsed_ms=d.get("elapsed_ms", 0),
            action_metadata=d.get("action_metadata", {}) or {},
        )


@dataclass
class AssertionResult:
    success: bool = False
    type: str = ""
    message: str = ""
    elapsed_ms: int = 0
    attempts: int = 0
    poll_interval_ms: int = 0

    @classmethod
    def from_dict(cls, d: Optional[Dict[str, Any]]) -> "AssertionResult":
        if not d:
            return cls()
        return cls(
            success=d.get("success", False),
            type=d.get("type", ""),
            message=d.get("message", ""),
            elapsed_ms=d.get("elapsed_ms", 0),
            attempts=d.get("attempts", 0),
            poll_interval_ms=d.get("poll_interval_ms", 0),
        )


@dataclass
class Observation:
    """Snapshot of the current page/screen returned by every action.

    ``type`` is ``"observation"`` for a full snapshot or ``"delta"`` when the
    server decided a delta was smaller than the full tree. The raw payload is
    kept on ``raw`` so advanced callers can reach fields not yet modeled here.
    """

    type: str = "observation"
    spatial_tree: List[SpatialNode] = field(default_factory=list)
    logs: List[ConsoleLog] = field(default_factory=list)
    visual_context: str = ""
    page_info: Optional[PageInfo] = None
    tabs: List[TabInfo] = field(default_factory=list)
    action_result: Optional[ActionResult] = None
    assertion_result: Optional[AssertionResult] = None
    raw: Dict[str, Any] = field(default_factory=dict)

    @classmethod
    def from_dict(cls, d: Dict[str, Any]) -> "Observation":
        return cls(
            type=d.get("type", "observation"),
            spatial_tree=[SpatialNode.from_dict(n) for n in d.get("spatial_tree", []) or []],
            logs=[ConsoleLog.from_dict(l) for l in d.get("logs", []) or []],
            visual_context=d.get("visual_context", ""),
            page_info=PageInfo.from_dict(d.get("page_info")),
            tabs=[TabInfo.from_dict(t) for t in d.get("tabs", []) or []],
            action_result=ActionResult.from_dict(d.get("action_result")),
            assertion_result=AssertionResult.from_dict(d.get("assertion_result")),
            raw=d,
        )
