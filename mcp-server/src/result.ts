import type { CallToolResult } from "@modelcontextprotocol/sdk/types.js";

/**
 * Build a structured + text tool result from any JSON-serializable value.
 * Claude Code displays the text block; downstream tools can consume the
 * structured content.
 */
export function jsonResult(value: unknown): CallToolResult {
  const text = JSON.stringify(value, null, 2);
  return {
    content: [{ type: "text", text }],
    structuredContent:
      value && typeof value === "object"
        ? (value as Record<string, unknown>)
        : { value },
  };
}

/**
 * Build an error tool result. Tool-level errors surface as isError:true so
 * Claude can recover; protocol-level errors throw instead.
 */
export function errorResult(message: string): CallToolResult {
  return {
    content: [{ type: "text", text: message }],
    isError: true,
  };
}

/**
 * Wrap an async handler so any thrown error becomes an isError tool result
 * instead of a JSON-RPC protocol error - what every tool handler uses.
 */
export async function safeHandler<T>(
  fn: () => Promise<T>,
  render: (value: T) => CallToolResult,
): Promise<CallToolResult> {
  try {
    const value = await fn();
    return render(value);
  } catch (err) {
    const msg = err instanceof Error ? err.message : String(err);
    return errorResult(msg);
  }
}
