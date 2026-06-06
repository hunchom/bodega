import { spawn } from "node:child_process";

/**
 * Result of invoking the `yum` binary.
 */
export interface YumResult {
  stdout: string;
  stderr: string;
  exitCode: number;
}

/**
 * Runner abstracts subprocess execution so handlers are unit-testable
 * without shelling out to a real `yum` binary.
 */
export interface Runner {
  run(args: string[]): Promise<YumResult>;
}

/**
 * ExecRunner spawns the real `yum` binary on PATH.
 * Resolution relies on the user's shell PATH; `./build.sh --install` places
 * `yum` at `~/.local/bin/yum`.
 */
export class ExecRunner implements Runner {
  constructor(private readonly binary: string = "yum") {}

  run(args: string[]): Promise<YumResult> {
    return new Promise((resolve, reject) => {
      const proc = spawn(this.binary, args, {
        stdio: ["ignore", "pipe", "pipe"],
        env: process.env,
      });
      let stdout = "";
      let stderr = "";
      proc.stdout.on("data", (chunk: Buffer) => {
        stdout += chunk.toString();
      });
      proc.stderr.on("data", (chunk: Buffer) => {
        stderr += chunk.toString();
      });
      proc.on("error", (err: NodeJS.ErrnoException) => {
        if (err.code === "ENOENT") {
          reject(
            new Error(
              `yum binary not found on PATH. Install it from the bodega repo: \`go install ./cmd/yum\`.`,
            ),
          );
          return;
        }
        reject(err);
      });
      proc.on("close", (code) => {
        resolve({ stdout, stderr, exitCode: code ?? 0 });
      });
    });
  }
}

// Keys that mark a structured result envelope the CLI deliberately prints to
// stdout *alongside* a non-zero exit: `yum verify` exits 1 with {issues,passed}
// when the tree has problems, and mutations exit 1 on partial failure while
// still emitting {installed|removed|upgraded, failed}. Recognizing these lets
// us surface the payload instead of collapsing it to one opaque error.
const ENVELOPE_KEYS = [
  "installed",
  "removed",
  "upgraded",
  "failed",
  "passed",
  "issues",
];

// parseEnvelope returns the parsed object when stdout is one of those
// structured envelopes, else undefined so the caller throws. Gated on known
// keys so genuine hard failures (empty/garbage stdout, or unrelated read
// commands that happen to print JSON before erroring) still surface as errors.
function parseEnvelope(trimmed: string): unknown | undefined {
  let parsed: unknown;
  try {
    parsed = JSON.parse(trimmed);
  } catch {
    return undefined;
  }
  if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
    const obj = parsed as Record<string, unknown>;
    if (ENVELOPE_KEYS.some((k) => k in obj)) return parsed;
  }
  return undefined;
}

/**
 * Run `yum` with the given arguments and JSON mode enabled.
 * On a non-zero exit, returns the structured stdout envelope when the command
 * emitted one (verify report, mutation partial-failure); otherwise throws a
 * descriptive Error with stderr.
 */
export async function runYumJSON<T>(
  runner: Runner,
  args: string[],
): Promise<T> {
  const { stdout, stderr, exitCode } = await runner.run(["--json", ...args]);
  const trimmed = stdout.trim();
  if (exitCode !== 0) {
    if (trimmed) {
      const env = parseEnvelope(trimmed);
      if (env !== undefined) return env as T;
    }
    const msg = (stderr || stdout).trim() || `exit code ${exitCode}`;
    throw new Error(`yum ${args.join(" ")} failed: ${msg}`);
  }
  if (!trimmed) {
    // Some yum commands emit nothing on success (e.g. empty result sets).
    // Return a zero-valued object by type assertion; callers expect arrays or
    // objects and can guard with Array.isArray or property checks.
    return undefined as unknown as T;
  }
  try {
    return JSON.parse(trimmed) as T;
  } catch (err) {
    throw new Error(
      `yum ${args.join(" ")} returned non-JSON output: ${trimmed.slice(0, 200)}`,
    );
  }
}

