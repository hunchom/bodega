import { test } from "node:test";
import assert from "node:assert/strict";
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { InMemoryTransport } from "@modelcontextprotocol/sdk/inMemory.js";
import { buildServer, SERVER_INFO } from "../src/server.js";
import { FakeRunner, ok, fail } from "./fake-runner.js";

/**
 * Spin up a buildServer + an in-memory client, returning both so tests can
 * drive the MCP protocol end-to-end without shelling out or using stdio.
 */
async function withClient(runner: FakeRunner) {
  const server = buildServer(runner);
  const [clientTransport, serverTransport] =
    InMemoryTransport.createLinkedPair();

  const client = new Client({ name: "test", version: "0.0.0" });
  await Promise.all([
    server.connect(serverTransport),
    client.connect(clientTransport),
  ]);
  return {
    client,
    close: async () => {
      await client.close();
      await server.close();
    },
  };
}

test("initialize advertises tools, resources, prompts, logging", async () => {
  const runner = new FakeRunner(() => ok([]));
  const { client, close } = await withClient(runner);
  try {
    const caps = client.getServerCapabilities();
    assert.ok(caps?.tools, "tools capability missing");
    assert.ok(caps?.resources, "resources capability missing");
    assert.ok(caps?.prompts, "prompts capability missing");
    assert.ok(caps?.logging, "logging capability missing");
    const version = client.getServerVersion();
    assert.equal(version?.name, SERVER_INFO.name);
    assert.equal(version?.version, SERVER_INFO.version);
  } finally {
    await close();
  }
});

test("tools/list returns all 12 tools with valid schemas", async () => {
  const runner = new FakeRunner(() => ok([]));
  const { client, close } = await withClient(runner);
  try {
    const { tools } = await client.listTools();
    const names = tools.map((t) => t.name).sort();
    assert.deepEqual(names, [
      "yum_duplicates",
      "yum_history",
      "yum_info",
      "yum_install",
      "yum_list",
      "yum_log",
      "yum_outdated",
      "yum_remove",
      "yum_search",
      "yum_services",
      "yum_upgrade",
      "yum_verify",
    ]);
    for (const tool of tools) {
      assert.ok(tool.description, `${tool.name} missing description`);
      assert.equal(
        tool.inputSchema.type,
        "object",
        `${tool.name} input schema must be an object`,
      );
    }
  } finally {
    await close();
  }
});

test("resources/list returns the 4 registered URIs", async () => {
  const runner = new FakeRunner(() => ok([]));
  const { client, close } = await withClient(runner);
  try {
    const { resources } = await client.listResources();
    const uris = resources.map((r) => r.uri).sort();
    // Parameterized resources (yum://installed/{name}, yum://log/{name}) show
    // up as resource templates, not concrete URIs, so we expect only the
    // non-templated ones here.
    assert.ok(
      uris.includes("yum://installed"),
      "yum://installed resource missing",
    );
    assert.ok(uris.includes("yum://outdated"), "yum://outdated missing");
    assert.ok(uris.includes("yum://verify"), "yum://verify missing");

    const { resourceTemplates } = await client.listResourceTemplates();
    const templateUris = resourceTemplates.map((t) => t.uriTemplate).sort();
    assert.ok(
      templateUris.includes("yum://installed/{name}"),
      "installed template missing",
    );
    assert.ok(
      templateUris.includes("yum://log/{name}"),
      "log template missing",
    );
  } finally {
    await close();
  }
});

test("prompts/list returns diagnose and cleanup", async () => {
  const runner = new FakeRunner(() => ok([]));
  const { client, close } = await withClient(runner);
  try {
    const { prompts } = await client.listPrompts();
    const names = prompts.map((p) => p.name).sort();
    assert.deepEqual(names, ["cleanup", "diagnose"]);
  } finally {
    await close();
  }
});

test("yum_search invokes yum with correct args and returns results", async () => {
  const runner = new FakeRunner((args) => {
    assert.deepEqual(args, [
      "--json",
      "search",
      "--name-only",
      "--limit",
      "5",
      "ripgrep",
    ]);
    return ok([{ name: "ripgrep", desc: "recursive grep" }]);
  });
  const { client, close } = await withClient(runner);
  try {
    const result = await client.callTool({
      name: "yum_search",
      arguments: { query: "ripgrep", name_only: true, limit: 5 },
    });
    assert.equal(result.isError, undefined);
    const structured = result.structuredContent as { results: unknown[] };
    assert.equal(structured.results.length, 1);
  } finally {
    await close();
  }
});

test("yum_install passes packages verbatim and surfaces JSON response", async () => {
  const runner = new FakeRunner((args) => {
    assert.deepEqual(args, ["--json", "install", "-y", "git", "jq"]);
    return ok({ installed: ["git", "jq"], failed: [] });
  });
  const { client, close } = await withClient(runner);
  try {
    const result = await client.callTool({
      name: "yum_install",
      arguments: { packages: ["git", "jq"] },
    });
    assert.equal(result.isError, undefined);
    const structured = result.structuredContent as {
      installed: string[];
      failed: unknown[];
    };
    assert.deepEqual(structured.installed, ["git", "jq"]);
    assert.deepEqual(structured.failed, []);
  } finally {
    await close();
  }
});

test("yum_list normalizes 'outdated' -> 'updates' subcommand", async () => {
  const runner = new FakeRunner((args) => {
    assert.deepEqual(args, ["--json", "list", "updates"]);
    return ok([{ name: "git", current: "2.50", latest: "2.51" }]);
  });
  const { client, close } = await withClient(runner);
  try {
    const result = await client.callTool({
      name: "yum_list",
      arguments: { filter: "outdated" },
    });
    assert.equal(result.isError, undefined);
  } finally {
    await close();
  }
});

test("yum_remove with force adds --force flag", async () => {
  const runner = new FakeRunner((args) => {
    assert.deepEqual(args, ["--json", "remove", "-y", "--force", "openssl"]);
    return ok({ removed: ["openssl"] });
  });
  const { client, close } = await withClient(runner);
  try {
    const result = await client.callTool({
      name: "yum_remove",
      arguments: { packages: ["openssl"], force: true },
    });
    assert.equal(result.isError, undefined);
  } finally {
    await close();
  }
});

test("yum_services rejects mutating action without a name", async () => {
  const runner = new FakeRunner(() => ok({}));
  const { client, close } = await withClient(runner);
  try {
    const result = await client.callTool({
      name: "yum_services",
      arguments: { action: "start" },
    });
    assert.equal(result.isError, true);
    const content = result.content as Array<{ type: string; text: string }>;
    assert.match(content[0]!.text, /requires a service name/);
  } finally {
    await close();
  }
});

test("subprocess failure surfaces as isError with stderr", async () => {
  const runner = new FakeRunner(() => fail("Error: network unreachable"));
  const { client, close } = await withClient(runner);
  try {
    const result = await client.callTool({
      name: "yum_outdated",
      arguments: {},
    });
    assert.equal(result.isError, true);
    const content = result.content as Array<{ type: string; text: string }>;
    assert.match(content[0]!.text, /network unreachable/);
  } finally {
    await close();
  }
});

test("malformed JSON from yum surfaces a clear parse error", async () => {
  const runner = new FakeRunner(() => ({
    stdout: "not json at all",
    stderr: "",
    exitCode: 0,
  }));
  const { client, close } = await withClient(runner);
  try {
    const result = await client.callTool({
      name: "yum_search",
      arguments: { query: "git" },
    });
    assert.equal(result.isError, true);
    const content = result.content as Array<{ type: string; text: string }>;
    assert.match(content[0]!.text, /non-JSON output/);
  } finally {
    await close();
  }
});

test("resource yum://installed returns installed packages as JSON", async () => {
  const runner = new FakeRunner((args) => {
    assert.deepEqual(args, ["--json", "list", "installed"]);
    return ok([{ name: "git" }]);
  });
  const { client, close } = await withClient(runner);
  try {
    const result = await client.readResource({ uri: "yum://installed" });
    assert.equal(result.contents.length, 1);
    const first = result.contents[0]!;
    assert.equal(first.mimeType, "application/json");
    assert.ok("text" in first, "expected text content, got blob");
    const parsed = JSON.parse(first.text);
    assert.deepEqual(parsed, [{ name: "git" }]);
  } finally {
    await close();
  }
});

test("prompt 'diagnose' renders the triage checklist for the user command", async () => {
  const runner = new FakeRunner(() => ok([]));
  const { client, close } = await withClient(runner);
  try {
    const result = await client.getPrompt({
      name: "diagnose",
      arguments: { command: "git: command not found" },
    });
    assert.equal(result.messages.length, 1);
    const msg = result.messages[0]!;
    assert.equal(msg.role, "user");
    const content = msg.content as { type: string; text: string };
    assert.match(content.text, /yum_verify/);
    assert.match(content.text, /yum_duplicates/);
    assert.match(content.text, /git: command not found/);
  } finally {
    await close();
  }
});
