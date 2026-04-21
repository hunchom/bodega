import {
  ResourceTemplate,
  type McpServer,
} from "@modelcontextprotocol/sdk/server/mcp.js";
import type { Runner } from "../runner.js";
import { runYumJSON } from "../runner.js";

interface InstalledPkg {
  name?: string;
  [key: string]: unknown;
}

/**
 * Register `yum://installed` (collection) and `yum://installed/{name}` (detail).
 * The detail resource is backed by a ResourceTemplate so the SDK parses `name`
 * out of the URI and hands it to the read callback.
 */
export function registerInstalledResources(
  server: McpServer,
  runner: Runner,
): void {
  server.registerResource(
    "installed",
    "yum://installed",
    {
      title: "Installed packages",
      description:
        "JSON array of every Homebrew formula currently installed on this machine, as reported by bodega.",
      mimeType: "application/json",
    },
    async (uri) => {
      const packages = await runYumJSON<InstalledPkg[]>(runner, [
        "list",
        "installed",
      ]);
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

  // Don't enumerate per-package resources in the `list` callback — on a
  // typical machine that emits 200-300 entries into every resources/list
  // response. Clients that want a package can read the template URI
  // directly via `yum://installed/{name}`; `yum://installed` already
  // gives them the enumeration cheaply as a single resource read.
  server.registerResource(
    "installed-detail",
    new ResourceTemplate("yum://installed/{name}", { list: undefined }),
    {
      title: "Installed package detail",
      description:
        "Full metadata for a single installed package: version, dependencies, bottled platforms, install time.",
      mimeType: "application/json",
    },
    async (uri, { name }) => {
      const pkg = Array.isArray(name) ? name[0] : name;
      if (!pkg) {
        throw new Error(
          `yum://installed/{name} requires a package name, got ${uri.href}`,
        );
      }
      const data = await runYumJSON<unknown>(runner, ["info", pkg]);
      return {
        contents: [
          {
            uri: uri.href,
            mimeType: "application/json",
            text: JSON.stringify(data ?? null, null, 2),
          },
        ],
      };
    },
  );
}
