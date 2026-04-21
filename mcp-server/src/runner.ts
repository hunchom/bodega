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
 * Resolution relies on the user's shell PATH; plugin docs instruct them to
 * `go install ./cmd/yum` so the binary is present.
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

/**
 * Run `yum` with the given arguments and JSON mode enabled.
 * Throws a descriptive Error with stderr on non-zero exit.
 */
export async function runYumJSON<T>(
  runner: Runner,
  args: string[],
): Promise<T> {
  const { stdout, stderr, exitCode } = await runner.run(["--json", ...args]);
  if (exitCode !== 0) {
    const msg = (stderr || stdout).trim() || `exit code ${exitCode}`;
    throw new Error(`yum ${args.join(" ")} failed: ${msg}`);
  }
  const trimmed = stdout.trim();
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

/**
 * Run `yum` without --json - for commands that don't yet support structured
 * output. Returns raw stdout trimmed.
 */
export async function runYumText(
  runner: Runner,
  args: string[],
): Promise<string> {
  const { stdout, stderr, exitCode } = await runner.run(args);
  if (exitCode !== 0) {
    const msg = (stderr || stdout).trim() || `exit code ${exitCode}`;
    throw new Error(`yum ${args.join(" ")} failed: ${msg}`);
  }
  return stdout.trim();
}
