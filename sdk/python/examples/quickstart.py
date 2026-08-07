"""Quickstart for the Scratchpad Python SDK.

Requires a running Scratchpad server on :8080 (cmd/server).

Run with the SDK on the path:

    PYTHONPATH=.. python quickstart.py
"""

import sys

from scratchpad_sdk import ScratchpadClient, ScratchpadError

BASE_URL = "http://localhost:8080"


def main() -> None:
    client = ScratchpadClient(BASE_URL)

    # Health check first.
    print("healthz:", client.healthz())

    # Create a session (Chrome, headful), run it, then close it.
    sid = client.create_session(headless=False)
    print("created session:", sid)
    try:
        client.navigate("https://example.com", session_id=sid)

        # Observe: read the interactive elements from the accessibility tree.
        obs = client.observe(session_id=sid)
        for node in obs.spatial_tree:
            if node.interactive:
                print(f"  {node.node_id}: role={node.role} name={node.name!r}")

        # A coordinate click, then read the console ring buffer.
        client.click(x=320, y=180, session_id=sid)
        console = client.get_console(session_id=sid)
        print("console entries:", len(console.get("logs", [])))
    except ScratchpadError as exc:
        # The typed error envelope surfaces code / message / hint / request_id.
        print("error:", exc.code, exc.message, exc.hint, exc.request_id, file=sys.stderr)
        sys.exit(1)
    finally:
        client.delete_session(session_id=sid)
        print("deleted session:", sid)


if __name__ == "__main__":
    main()
