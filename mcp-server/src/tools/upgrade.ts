import { z } from "zod";
import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import type { Runner } from "../runner.js";
import { runYumJSON } from "../runner.js";
import { jsonResult, safeHandler } from "../result.js";

const inputSchema = {
  packages: z
    .array(z.string().min(1))
    .optional()
    .describe(
      "Specific packages to upgrade. Omit or pass an empty array to upgrade everything currently outdated.",
    ),
};

interface UpgradeResponse {
  upgraded?: string[];
  failed?: Array<{ package: string; error: string }>;
  [key: string]: unknown;
}

export function registerUpgrade(server: McpServer, runner: Runner): void {
  server.registerTool(
    "yum_upgrade",
    {
      title: "Upgrade packages",
      description:
        "Upgrade outdated packages to their latest upstream version. With no packages, upgrades everything reported by yum_outdated. Upgrades are journaled: if a newer version breaks the user's workflow, yum_history + `yum history undo` restores the previous state.",
      inputSchema,
      annotations: {
        destructiveHint: false,
        idempotentHint: true,
        openWorldHint: true,
      },
    },
    async ({ packages }) =>
      safeHandler(
        () => {
          const args = ["upgrade", "-y"];
          if (packages && packages.length > 0) args.push(...packages);
          return runYumJSON<UpgradeResponse>(runner, args);
        },
        (payload) =>
          jsonResult({
            upgraded: payload?.upgraded ?? packages ?? [],
            failed: payload?.failed ?? [],
          }),
      ),
  );
}
