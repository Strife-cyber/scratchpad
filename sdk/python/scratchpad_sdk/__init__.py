"""Scratchpad REST client SDK.

A minimal, hand-written client for the documented REST surface of the
Scratchpad engine (OpenAPI 3.0 spec served at /swagger.json). Uses only the
Python standard library.
"""

from .client import ScratchpadClient, ScratchpadError
from .models import (
    ActionResult,
    AssertionResult,
    Bounds,
    ConsoleLog,
    ErrorResponse,
    Observation,
    PageInfo,
    ScrollState,
    SpatialNode,
    TabInfo,
)

__version__ = "0.1.0"

__all__ = [
    "ScratchpadClient",
    "ScratchpadError",
    "ActionResult",
    "AssertionResult",
    "Bounds",
    "ConsoleLog",
    "ErrorResponse",
    "Observation",
    "PageInfo",
    "ScrollState",
    "SpatialNode",
    "TabInfo",
]
