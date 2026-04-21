import { z } from "zod";
import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import type { Runner } from "../runner.js";
import { runYumJSON } from "../runner.js";
import { jsonResult, safeHandler } from "../result.js";

const inputSchema = {
  fix: z
    .boolean()
    .optional()
    .describe(
      "Automatically repair issues the checker knows how to fix (broken symlinks, stale linkage). Leaves checksum or structural issues to the user.",
    ),
};

export function registerVerify(server: McpServer, runner: Runner): void {
  server.registerTool(
    "yum_verify",
    {
      title: "Verify install integrity",
      description:
        "Run bodega's integrity check over the installed package set. Reports missing files, broken symlinks, checksum mismatches, missing dependencies, and orphaned files. Use this when the user reports a 'command not found' error for something they know they installed, or when a formerly working tool starts misbehaving. With fix=true, repairs what is safely automatable.",
      inputSchema,
      annotations: {
        readOnlyHint: false,
        destructiveHint: false,
        idempotentHint: true,
        openWorldHint: false,
      },
    },
    async ({ fix }) =>
      safeHandler(
        () => {
          const args = ["verify"];
          if (fix) args.push("--fix");
          return runYumJSON<unknown>(runner, args);
        },
        (issues) => jsonResult({ issues: issues ?? {} }),
      ),
  );
}
