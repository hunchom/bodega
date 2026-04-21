import {
  ResourceTemplate,
  type McpServer,
} from "@modelcontextprotocol/sdk/server/mcp.js";
import type { Runner } from "../runner.js";
import { runYumJSON } from "../runner.js";

export function registerLogResource(server: McpServer, runner: Runner): void {
  server.registerResource(
    "package-log",
    new ResourceTemplate("yum://log/{name}", { list: undefined }),
    {
      title: "Per-package transaction log",
      description:
        "JSON array of journal events (install, upgrade, remove) for a single package, newest first.",
      mimeType: "application/json",
    },
    async (uri, { name }) => {
      const pkg = Array.isArray(name) ? name[0] : name;
      if (!pkg) {
        throw new Error(
          `yum://log/{name} requires a package name, got ${uri.href}`,
        );
      }
      const events = await runYumJSON<unknown[]>(runner, ["log", pkg]);
      return {
        contents: [
          {
            uri: uri.href,
            mimeType: "application/json",
            text: JSON.stringify(events ?? [], null, 2),
          },
        ],
      };
    },
  );
}
