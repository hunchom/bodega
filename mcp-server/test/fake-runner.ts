import type { Runner, YumResult } from "../src/runner.js";

/**
 * FakeRunner captures every call and returns a pre-programmed response.
 * Tests inject one per scenario.
 */
export class FakeRunner implements Runner {
  calls: string[][] = [];
  constructor(private readonly responder: (args: string[]) => YumResult) {}

  async run(args: string[]): Promise<YumResult> {
    this.calls.push(args);
    return this.responder(args);
  }

  lastCall(): string[] {
    if (this.calls.length === 0) throw new Error("no calls recorded");
    return this.calls[this.calls.length - 1]!;
  }
}

/** Respond with ok(payload) to return stdout=JSON.stringify(payload), exit 0. */
export function ok(payload: unknown): YumResult {
  return { stdout: JSON.stringify(payload), stderr: "", exitCode: 0 };
}

/** Respond with fail(msg) to simulate a non-zero exit with stderr=msg. */
export function fail(msg: string, code = 1): YumResult {
  return { stdout: "", stderr: msg, exitCode: code };
}
