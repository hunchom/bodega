import { z } from "zod";
import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import type { Runner } from "../runner.js";
import { runYumJSON } from "../runner.js";
import { jsonResult, safeHandler } from "../result.js";

export function registerDuplicates(server: McpServer, runner: Runner): void {
  server.registerTool(
    "yum_duplicates",
    {
      title: "List duplicate installs",
      description:
        "Find packages installed under multiple versions simultaneously - a common cause of 'wrong library is loaded' bugs and surprising PATH resolution. Returns each duplicated package and the versions present. Use as the first step when a user reports a runtime version mismatch or stale headers showing up in a build.",
      inputSchema: {
        _unused: z
          .undefined()
          .optional()
          .describe("This tool takes no arguments."),
      },
      annotations: {
        readOnlyHint: true,
        idempotentHint: true,
        openWorldHint: false,
      },
    },
    async () =>
      safeHandler(
        () => runYumJSON<unknown[]>(runner, ["duplicates"]),
        (dups) => jsonResult({ duplicates: dups ?? [] }),
      ),
  );
}
