import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";

/**
 * Pre-baked prompt: audit installed packages and propose a pruning plan.
 */
export function registerCleanupPrompt(server: McpServer): void {
  server.registerPrompt(
    "cleanup",
    {
      title: "Review and prune installed packages",
      description:
        "Walk the user through the installed tree - leaves, duplicates, outdated packages - and propose a safe cleanup plan using bodega.",
      // No arguments - the user invokes this when they want a full audit.
    },
    () => ({
      messages: [
        {
          role: "user" as const,
          content: {
            type: "text" as const,
            text: [
              "Audit my installed Homebrew packages and propose a cleanup plan.",
              "",
              "Use the bodega MCP tools:",
              "",
              "1. `yum_list` with filter='leaves' to identify packages nothing else depends on - these are the easiest to prune.",
              "2. `yum_duplicates` to find parallel version installs.",
              "3. `yum_outdated` to find packages with upgrades available.",
              "",
              "Then produce a three-section plan:",
              "",
              "- **Safe to remove**: leaf packages the user probably installed for a one-off and doesn't need anymore. Err on the side of asking.",
              "- **Duplicates to consolidate**: explain which version is currently linked and which are vestigial.",
              "- **Safe upgrades**: packages where upgrading is low risk (patch-level bumps of well-known tools).",
              "",
              "Never execute any removal or upgrade. Present the plan, list the exact tool calls that would carry it out, and wait for explicit user approval.",
            ].join("\n"),
          },
        },
      ],
    }),
  );
}
