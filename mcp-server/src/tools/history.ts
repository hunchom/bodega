import { z } from "zod";
import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import type { Runner } from "../runner.js";
import { runYumJSON } from "../runner.js";
import { jsonResult, safeHandler } from "../result.js";

const inputSchema = {
  limit: z
    .number()
    .int()
    .min(0)
    .optional()
    .describe("Cap the number of recent transactions returned. 0 means all."),
};

export function registerHistory(server: McpServer, runner: Runner): void {
  server.registerTool(
    "yum_history",
    {
      title: "Show transaction history",
      description:
        "List recent bodega transactions across every package, newest first. Each entry has an id that `yum history undo <id>` can roll back. Use this when the user asks 'what did we change recently?' or wants to undo an install, remove, or upgrade.",
      inputSchema,
      annotations: {
        readOnlyHint: true,
        idempotentHint: true,
        openWorldHint: false,
      },
    },
    async ({ limit }) =>
      safeHandler(
        () => {
          const args = ["history"];
          if (limit && limit > 0) args.push("--limit", String(limit));
          return runYumJSON<unknown[]>(runner, args);
        },
        (txs) => jsonResult({ transactions: txs ?? [] }),
      ),
  );
}
