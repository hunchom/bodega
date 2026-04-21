import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import type { Runner } from "../runner.js";
import { runYumJSON } from "../runner.js";

export function registerVerifyResource(
  server: McpServer,
  runner: Runner,
): void {
  server.registerResource(
    "verify-report",
    "yum://verify",
    {
      title: "Integrity report",
      description:
        "Current output of `yum verify`: grouped issues (missing files, broken symlinks, checksum mismatches, missing deps, orphans). Read-only - does not attempt any fix.",
      mimeType: "application/json",
    },
    async (uri) => {
      const issues = await runYumJSON<unknown>(runner, ["verify"]);
      return {
        contents: [
          {
            uri: uri.href,
            mimeType: "application/json",
            text: JSON.stringify(issues ?? {}, null, 2),
          },
        ],
      };
    },
  );
}
