import { z } from "zod";
import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import type { Runner } from "../runner.js";
import { runYumJSON } from "../runner.js";
import { jsonResult, safeHandler } from "../result.js";

const inputSchema = {
  packages: z
    .array(z.string().min(1))
    .min(1)
    .describe(
      "Package names to install (e.g. ['git', 'ripgrep']). Must be Homebrew formulae; casks are not supported on this fast path.",
    ),
};

interface InstallResponse {
  installed?: string[];
  failed?: Array<{ package: string; error: string }>;
  [key: string]: unknown;
}

export function registerInstall(server: McpServer, runner: Runner): void {
  server.registerTool(
    "yum_install",
    {
      title: "Install packages",
      description:
        "Install one or more macOS Homebrew packages via bodega's native fast path (~4x faster than `brew install` for bottled formulae). Use this when the user asks for a CLI tool that isn't yet installed, or when a build/compilation error indicates a missing dependency. Each install is recorded in bodega's transaction journal and can be rolled back with yum_history + `yum history undo`.",
      inputSchema,
      annotations: {
        destructiveHint: false,
        idempotentHint: true,
        openWorldHint: true,
      },
    },
    async ({ packages }) =>
      safeHandler(
        () =>
          runYumJSON<InstallResponse>(runner, ["install", "-y", ...packages]),
        (payload) =>
          jsonResult({
            installed: payload?.installed ?? packages,
            failed: payload?.failed ?? [],
          }),
      ),
  );
}
