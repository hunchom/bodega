import { z } from "zod";
import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import type { Runner } from "../runner.js";
import { runYumJSON } from "../runner.js";
import { jsonResult, safeHandler } from "../result.js";

const inputSchema = {
  package: z
    .string()
    .min(1)
    .describe("Formula name to look up (e.g. 'git', 'postgresql@16')."),
};

export function registerInfo(server: McpServer, runner: Runner): void {
  server.registerTool(
    "yum_info",
    {
      title: "Show package info",
      description:
        "Return metadata, dependencies, and install state for a single package: version, description, homepage, license, install location, bottled platforms, and whether it is currently installed. Use this before recommending an install, when the user asks 'what does X do?', or to confirm a specific version is available.",
      inputSchema,
      annotations: {
        readOnlyHint: true,
        idempotentHint: true,
        openWorldHint: true,
      },
    },
    async ({ package: pkg }) =>
      safeHandler(
        () => runYumJSON<unknown>(runner, ["info", pkg]),
        (data) => jsonResult({ package: data ?? null }),
      ),
  );
}
