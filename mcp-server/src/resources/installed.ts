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

  server.registerResource(
    "installed-detail",
    new ResourceTemplate("yum://installed/{name}", {
      list: async () => {
        const packages = await runYumJSON<InstalledPkg[]>(runner, [
          "list",
          "installed",
        ]);
        return {
          resources: (packages ?? [])
            .filter((p): p is InstalledPkg & { name: string } =>
              typeof p?.name === "string",
            )
            .map((p) => ({
              uri: `yum://installed/${encodeURIComponent(p.name)}`,
              name: p.name,
              mimeType: "application/json",
            })),
        };
      },
    }),
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
