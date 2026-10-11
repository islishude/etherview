import { execFile, execFileSync } from 'node:child_process';

// Async execution lets SIGINT/SIGTERM abort the CLI and remove its container.
export async function compile(docker, args, input, signal, timeout = 30000) {
  try {
    return await new Promise((resolve, reject) => {
      const child = execFile(docker, args, { encoding: 'utf8', maxBuffer: 33554432, timeout, signal },
        (error, stdout, stderr) => error ? reject(error) : resolve({ stdout, stderr }));
      child.stdin.on('error', () => {});
      child.stdin.end(input);
    });
  } catch (error) {
    const name = args[args.indexOf('--name') + 1];
    try { execFileSync(docker, ['rm', '-f', name], { timeout: 5000, stdio: 'ignore' }); }
    catch { console.error(`failed to clean up ${name}`); }
    throw error;
  }
}
