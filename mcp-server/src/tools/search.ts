import { z } from "zod";
import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import type { Runner } from "../runner.js";
import { runYumJSON } from "../runner.js";
import { jsonResult, safeHandler } from "../result.js";

const inputSchema = {
  query: z
    .string()
    .min(1)
    .describe(
      "Free-form search term matched against formula names and descriptions (e.g. 'ripgrep', 'postgres', 'json parser').",
    ),
  deps: z
    .boolean()
    .optional()
    .describe("Also match packages that pull in <query> as a dependency."),
  name_only: z
    .boolean()
    .optional()
    .describe(
      "Restrict matching to the formula name. Use for exact-name lookups.",
    ),
  limit: z
    .number()
    .int()
    .min(0)
    .optional()
    .describe("Cap the number of results returned. 0 or omitted means no cap."),
};

export function registerSearch(server: McpServer, runner: Runner): void {
  server.registerTool(
    "yum_search",
    {
      title: "Search Homebrew formulae",
      description:
        "Search the Homebrew index for formulae matching a query. Use this before yum_install when the user names a tool but you aren't certain of the exact formula name (e.g. 'postgres' -> 'postgresql@16'). Returns a list of candidates with descriptions to help disambiguate.",
      inputSchema,
      annotations: {
        readOnlyHint: true,
        idempotentHint: true,
        openWorldHint: true,
      },
    },
    async ({ query, deps, name_only, limit }) =>
      safeHandler(
        () => {
          const args: string[] = ["search"];
          if (deps) args.push("--deps");
          if (name_only) args.push("--name-only");
          if (limit && limit > 0) args.push("--limit", String(limit));
          args.push(query);
          return runYumJSON<unknown[]>(runner, args);
        },
        (results) => jsonResult({ results: results ?? [] }),
      ),
  );
}
