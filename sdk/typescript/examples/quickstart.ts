/**
 * Quickstart for the Scratchpad TypeScript SDK.
 *
 * Requires a running Scratchpad server on :8080 (cmd/server).
 *
 *   npm install
 *   npx tsx examples/quickstart.ts      # or: npm run build && node dist/...
 */

import { ScratchpadClient, ScratchpadError } from "../src";

async function main(): Promise<void> {
  const client = new ScratchpadClient("http://localhost:8080");

  // Health check first.
  console.log("healthz:", await client.healthz());

  // Create a session (Chrome, headful), run it, then close it.
  const sid = await client.createSession({ headless: false });
  console.log("created session:", sid);
  try {
    await client.navigate("https://example.com", sid);

    // Observe: read the interactive elements from the accessibility tree.
    const obs = await client.observe(sid);
    for (const node of obs.spatial_tree ?? []) {
      if (node.interactive) {
        console.log(`  ${node.node_id}: role=${node.role} name=${JSON.stringify(node.name)}`);
      }
    }

    // A coordinate click, then read the console ring buffer.
    await client.click(320, 180, {}, sid);
    const consoleResp = await client.getConsole(undefined, sid);
    console.log("console entries:", consoleResp.logs.length);
  } catch (err) {
    // The typed error envelope surfaces code / message / hint / requestId.
    if (err instanceof ScratchpadError) {
      console.error("error:", err.code, err.message, err.hint, err.requestId);
    }
    throw err;
  } finally {
    await client.deleteSession(sid);
    console.log("deleted session:", sid);
  }
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
