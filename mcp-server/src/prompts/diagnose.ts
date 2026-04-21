import { z } from "zod";
import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";

/**
 * Pre-baked prompt: walk the user through diagnosing a failing brew/yum
 * command using bodega's tools.
 */
export function registerDiagnosePrompt(server: McpServer): void {
  server.registerPrompt(
    "diagnose",
    {
      title: "Diagnose a failing package or command",
      description:
        "Triage 'command not found', 'library not loaded', or a broken tool by walking through bodega's verify, duplicates, and log tools. Produces a concrete repair plan.",
      argsSchema: {
        command: z
          .string()
          .describe(
            "The failing command or error message (e.g. 'git: command not found', 'dyld: Library not loaded: /opt/homebrew/opt/readline/lib/libreadline.8.dylib').",
          ),
      },
    },
    ({ command }) => ({
      messages: [
        {
          role: "user" as const,
          content: {
            type: "text" as const,
            text: [
              `I hit this error: ${command}`,
              "",
              "Please diagnose it using the bodega MCP tools. Specifically:",
              "",
              "1. Call `yum_verify` to look for missing files, broken symlinks, and checksum mismatches.",
              "2. Call `yum_duplicates` to check for conflicting parallel installs.",
              "3. If the error names a specific package or library, call `yum_log` for that package to see when it last changed.",
              "4. Cross-reference with `yum_history` (limit: 20) to find the most recent transaction that might have caused this.",
              "",
              "Then propose a concrete fix - `yum_verify` with fix=true, a `yum_history` undo of a specific transaction, or an explicit `yum_install` of a missing dependency. Do not execute the fix automatically; recommend it and wait for confirmation.",
            ].join("\n"),
          },
        },
      ],
    }),
  );
}
