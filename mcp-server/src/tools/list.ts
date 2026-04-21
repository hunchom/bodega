import { z } from "zod";
import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import type { Runner } from "../runner.js";
import { runYumJSON } from "../runner.js";
import { jsonResult, safeHandler } from "../result.js";

const FILTERS = [
  "all",
  "installed",
  "available",
  "updates",
  "outdated",
  "leaves",
  "pinned",
  "casks",
] as const;

const inputSchema = {
  filter: z
    .enum(FILTERS)
    .optional()
    .describe(
      "Which slice of packages to list. 'installed' shows everything currently present, 'leaves' shows packages nothing else depends on (candidates for cleanup), 'outdated' / 'updates' shows packages with a newer upstream version. Defaults to 'all'.",
    ),
};

// `outdated` and `all` are user-friendly aliases; translate to what the CLI
// actually accepts as a subcommand argument.
function mapFilter(filter?: (typeof FILTERS)[number]): string | undefined {
  if (!filter || filter === "all") return undefined;
  if (filter === "outdated") return "updates";
  return filter;
}

export function registerList(server: McpServer, runner: Runner): void {
  server.registerTool(
    "yum_list",
    {
      title: "List packages",
      description:
        "List installed, available, outdated, or leaf packages. Especially useful at the start of a cleanup task (filter='leaves') or when the user asks 'what do I have installed?'.",
      inputSchema,
      annotations: {
        readOnlyHint: true,
        idempotentHint: true,
        openWorldHint: true,
      },
    },
    async ({ filter }) =>
      safeHandler(
        () => {
          const args = ["list"];
          const sub = mapFilter(filter);
          if (sub) args.push(sub);
          return runYumJSON<unknown[]>(runner, args);
        },
        (packages) => jsonResult({ packages: packages ?? [] }),
      ),
  );
}
