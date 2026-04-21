import { z } from "zod";
import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import type { Runner } from "../runner.js";
import { runYumJSON } from "../runner.js";
import { jsonResult, safeHandler } from "../result.js";

export function registerOutdated(server: McpServer, runner: Runner): void {
  server.registerTool(
    "yum_outdated",
    {
      title: "List outdated packages",
      description:
        "Show every installed package with a newer upstream version. Prefer this over yum_list({filter:'outdated'}) when the user explicitly asks 'what's outdated?' - this shape is optimized for that question and includes the current and target versions side by side.",
      inputSchema: {
        _unused: z
          .undefined()
          .optional()
          .describe("This tool takes no arguments."),
      },
      annotations: {
        readOnlyHint: true,
        idempotentHint: true,
        openWorldHint: true,
      },
    },
    async () =>
      safeHandler(
        () => runYumJSON<unknown[]>(runner, ["outdated"]),
        (packages) => jsonResult({ packages: packages ?? [] }),
      ),
  );
}
