import { z } from "zod";
import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import type { Runner } from "../runner.js";
import { runYumJSON } from "../runner.js";
import { jsonResult, safeHandler } from "../result.js";

const inputSchema = {
  packages: z
    .array(z.string().min(1))
    .min(1)
    .describe("Package names to uninstall."),
  force: z
    .boolean()
    .optional()
    .describe(
      "Remove even if other packages depend on it. Dangerous - use only when the user explicitly requests force.",
    ),
};

interface RemoveResponse {
  removed?: string[];
  failed?: Array<{ package: string; error: string }>;
  [key: string]: unknown;
}

export function registerRemove(server: McpServer, runner: Runner): void {
  server.registerTool(
    "yum_remove",
    {
      title: "Remove packages",
      description:
        "Remove one or more Homebrew formulae. Every removal is journaled; reverse with yum_history + `yum history undo <id>`. Use this when the user wants to free disk space, resolve a duplicate install, or roll off a dependency they no longer need.",
      inputSchema,
      annotations: {
        destructiveHint: true,
        idempotentHint: false,
        openWorldHint: true,
      },
    },
    async ({ packages, force }) =>
      safeHandler(
        () => {
          const args = ["remove", "-y"];
          if (force) args.push("--force");
          return runYumJSON<RemoveResponse>(runner, [...args, ...packages]);
        },
        (payload) =>
          jsonResult({
            removed: payload?.removed ?? packages,
            failed: payload?.failed ?? [],
          }),
      ),
  );
}
