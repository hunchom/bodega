import { z } from "zod";
import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import type { Runner } from "../runner.js";
import { runYumJSON } from "../runner.js";
import { jsonResult, safeHandler } from "../result.js";

const inputSchema = {
  package: z
    .string()
    .min(1)
    .describe("Package whose transaction journal should be returned."),
  limit: z
    .number()
    .int()
    .min(0)
    .optional()
    .describe("Return at most this many events, newest first. 0 means all."),
};

export function registerLog(server: McpServer, runner: Runner): void {
  server.registerTool(
    "yum_log",
    {
      title: "Show per-package transaction log",
      description:
        "Return the chronological journal of installs, upgrades, and removals for a single package, newest first. Each entry carries a transaction id usable with `yum history undo`. Use this when diagnosing 'why did this package change?' or 'when did X break?' questions.",
      inputSchema,
      annotations: {
        readOnlyHint: true,
        idempotentHint: true,
        openWorldHint: false,
      },
    },
    async ({ package: pkg, limit }) =>
      safeHandler(
        () => {
          const args = ["log", pkg];
          if (limit && limit > 0) args.push("--limit", String(limit));
          return runYumJSON<unknown[]>(runner, args);
        },
        (events) => jsonResult({ events: events ?? [] }),
      ),
  );
}
