import { z } from "zod";
import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import type { Runner } from "../runner.js";
import { runYumJSON } from "../runner.js";
import { jsonResult, safeHandler } from "../result.js";

const ACTIONS = ["list", "start", "stop", "restart", "run"] as const;

const inputSchema = {
  action: z
    .enum(ACTIONS)
    .optional()
    .describe(
      "What to do. 'list' (default) returns every launchd-managed service and its state. 'start'/'stop'/'restart' mutate a single named service. 'run' runs a service once without registering it with launchd.",
    ),
  name: z
    .string()
    .optional()
    .describe(
      "Service name. Required for start, stop, restart, run. Omit for list.",
    ),
};

export function registerServices(server: McpServer, runner: Runner): void {
  server.registerTool(
    "yum_services",
    {
      title: "Manage background services",
      description:
        "List or control Homebrew background services (launchd agents such as postgresql, redis, nginx). Use this when the user asks 'is postgres running?' (action=list) or 'start redis' (action=start, name=redis).",
      inputSchema,
      annotations: {
        destructiveHint: false,
        idempotentHint: false,
        openWorldHint: false,
      },
    },
    async ({ action, name }) =>
      safeHandler(
        () => {
          const verb = action ?? "list";
          if (verb !== "list" && !name) {
            throw new Error(
              `action '${verb}' requires a service name (pass the 'name' argument)`,
            );
          }
          const args = ["services", verb];
          if (name) args.push(name);
          return runYumJSON<unknown>(runner, args);
        },
        (data) => jsonResult({ result: data ?? null }),
      ),
  );
}
