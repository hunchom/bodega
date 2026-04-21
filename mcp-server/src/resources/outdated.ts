import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import type { Runner } from "../runner.js";
import { runYumJSON } from "../runner.js";

export function registerOutdatedResource(
  server: McpServer,
  runner: Runner,
): void {
  server.registerResource(
    "outdated",
    "yum://outdated",
    {
      title: "Outdated packages",
      description:
        "JSON array of installed packages with a newer upstream version available.",
      mimeType: "application/json",
    },
    async (uri) => {
      const packages = await runYumJSON<unknown[]>(runner, ["outdated"]);
      return {
        contents: [
          {
            uri: uri.href,
            mimeType: "application/json",
            text: JSON.stringify(packages ?? [], null, 2),
          },
        ],
      };
    },
  );
}
