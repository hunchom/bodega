#!/usr/bin/env node
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import { buildServer } from "./server.js";

async function main(): Promise<void> {
  const server = buildServer();
  const transport = new StdioServerTransport();
  await server.connect(transport);
  // Log to stderr so stdout remains a clean JSON-RPC channel.
  process.stderr.write("bodega-mcp: stdio transport ready\n");
}

main().catch((err: unknown) => {
  const msg = err instanceof Error ? err.stack ?? err.message : String(err);
  process.stderr.write(`bodega-mcp fatal: ${msg}\n`);
  process.exit(1);
});
