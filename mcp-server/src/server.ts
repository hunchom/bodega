import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { ExecRunner, type Runner } from "./runner.js";

import { registerInstall } from "./tools/install.js";
import { registerRemove } from "./tools/remove.js";
import { registerSearch } from "./tools/search.js";
import { registerInfo } from "./tools/info.js";
import { registerList } from "./tools/list.js";
import { registerOutdated } from "./tools/outdated.js";
import { registerUpgrade } from "./tools/upgrade.js";
import { registerLog } from "./tools/log.js";
import { registerVerify } from "./tools/verify.js";
import { registerHistory } from "./tools/history.js";
import { registerDuplicates } from "./tools/duplicates.js";
import { registerServices } from "./tools/services.js";

import { registerInstalledResources } from "./resources/installed.js";
import { registerOutdatedResource } from "./resources/outdated.js";
import { registerLogResource } from "./resources/log.js";
import { registerVerifyResource } from "./resources/verify.js";

import { registerDiagnosePrompt } from "./prompts/diagnose.js";
import { registerCleanupPrompt } from "./prompts/cleanup.js";

const SERVER_INFO = {
  name: "bodega",
  version: "0.1.0",
} as const;

const INSTRUCTIONS = [
  "bodega exposes the `yum` macOS package manager as MCP tools.",
  "",
  "Prefer yum_search before yum_install when the user's phrasing is ambiguous.",
  "Call yum_verify and yum_duplicates before recommending a drastic fix like removing/reinstalling a package.",
  "Every mutating call (install, remove, upgrade) is journaled: `yum history undo <id>` rolls it back.",
  "For a guided repair flow, use the `diagnose` prompt. For a cleanup audit, use the `cleanup` prompt.",
].join("\n");

/**
 * Construct a fully-wired MCP server. The runner is injectable so tests can
 * swap in a fake.
 */
export function buildServer(runner: Runner = new ExecRunner()): McpServer {
  const server = new McpServer(SERVER_INFO, {
    capabilities: {
      tools: { listChanged: false },
      resources: { listChanged: false, subscribe: false },
      prompts: { listChanged: false },
      logging: {},
    },
    instructions: INSTRUCTIONS,
  });

  // Tools (12)
  registerInstall(server, runner);
  registerRemove(server, runner);
  registerSearch(server, runner);
  registerInfo(server, runner);
  registerList(server, runner);
  registerOutdated(server, runner);
  registerUpgrade(server, runner);
  registerLog(server, runner);
  registerVerify(server, runner);
  registerHistory(server, runner);
  registerDuplicates(server, runner);
  registerServices(server, runner);

  // Resources (4 registrations; installed contributes 2 URIs)
  registerInstalledResources(server, runner);
  registerOutdatedResource(server, runner);
  registerLogResource(server, runner);
  registerVerifyResource(server, runner);

  // Prompts (2)
  registerDiagnosePrompt(server);
  registerCleanupPrompt(server);

  return server;
}

export { SERVER_INFO };
