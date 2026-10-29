import { spawnSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";

const root = path.resolve(import.meta.dirname, "../..");

function docker(args, options = {}) {
  const command = process.env.DOCKER || "docker";
  const result = spawnSync("/bin/sh", ["-c", `exec ${command} "$@"`, "preview-cache", ...args], {
    encoding: "utf8",
    timeout: 30_000,
    ...options,
  });
  if (result.error || result.status !== 0)
    throw new Error(
      `Compiler cache Docker command failed: ${result.error?.message || result.stderr || result.status}`,
    );
  return result.stdout;
}

function assertCacheStopped(source) {
  const ids = docker(["ps", "-q"]).trim().split(/\s+/).filter(Boolean);
  if (!ids.length) return;
  const containers = JSON.parse(docker(["inspect", ...ids]));
  if (
    containers.some((container) =>
      container.Mounts.some(
        (mount) => mount.Type === "bind" && path.resolve(mount.Source) === source,
      ),
    )
  )
    throw new Error("Stop every container mounting the compiler cache before cleaning it");
}

async function managePreviewCache(action, composeArgs = ["-f", "compose.yaml"]) {
  const { runCompose } = await import("./preview-compose.mjs");
  if (!["init", "clean"].includes(action)) throw new Error("Expected init or clean");
  const config = JSON.parse(runCompose([...composeArgs, "config", "--format", "json"]));
  const mount = config.services.api.volumes.find(
    (volume) => volume.target === "/var/lib/etherview/compilers",
  );
  if (mount?.type !== "bind") throw new Error("Preview compiler cache requires a bind mount");
  const source = path.resolve(root, mount.source);
  // Reject redirected paths before any creation, chown or deletion. macOS's
  // system /var alias is resolved by callers supplying temporary directories.
  let ancestor = source;
  while (!fs.existsSync(ancestor)) ancestor = path.dirname(ancestor);
  if (fs.realpathSync(ancestor) !== ancestor)
    throw new Error("Compiler cache path must not contain symlinks");
  if (action === "clean") {
    assertCacheStopped(source);
    if (!fs.existsSync(source)) return;
  } else {
    fs.mkdirSync(source, { recursive: true, mode: 0o700 });
  }
  const info = fs.lstatSync(source);
  if (!info.isDirectory() || info.isSymbolicLink())
    throw new Error("Invalid compiler cache directory");
  docker(
    [
      "run",
      "--rm",
      "--network=none",
      "--read-only",
      "--user=0:0",
      "--cap-drop=ALL",
      "--cap-add=CHOWN",
      "--cap-add=FOWNER",
      "--cap-add=DAC_OVERRIDE",
      "--security-opt=no-new-privileges:true",
      "--mount",
      `type=bind,src=${source},dst=/compiler-cache`,
      "--mount",
      `type=bind,src=${path.join(root, ".github/scripts/preview-compiler-cache.mjs")},dst=/compiler-cache.mjs,readonly`,
      "--entrypoint=node",
      "etherview-preview-bundler:local",
      "/compiler-cache.mjs",
      "--container",
      action,
    ],
    { stdio: "inherit", timeout: 60_000 },
  );
  // The container emptied the directory; unlinking an empty child only needs
  // its host-owned parent's permissions, regardless of the child's UID.
  if (action === "clean") fs.rmdirSync(source);
}

const uid = 65532;
const gid = 65532;

function directory(directoryPath) {
  const info = fs.lstatSync(directoryPath);
  if (!info.isDirectory() || info.isSymbolicLink())
    throw new Error("Compiler cache must be an ordinary directory");
  return info;
}

function owned(info) {
  return info.uid === uid && info.gid === gid && (info.mode & 0o7777) === 0o750;
}

function assign(directoryPath) {
  const fd = fs.openSync(
    directoryPath,
    fs.constants.O_RDONLY | fs.constants.O_DIRECTORY | fs.constants.O_NOFOLLOW,
  );
  try {
    fs.fchownSync(fd, uid, gid);
    fs.fchmodSync(fd, 0o750);
  } finally {
    fs.closeSync(fd);
  }
}

// The container only receives this dedicated bind mount. No caller-supplied
// container path or recursive ownership repair is part of the interface.
function manageCache(action) {
  const root = "/compiler-cache";
  if (!["init", "clean"].includes(action)) throw new Error("Expected init or clean");
  const info = directory(root);
  const entries = fs.readdirSync(root);
  if (entries.some((entry) => entry !== "cache"))
    throw new Error("Compiler cache directory contains unrelated entries");
  const cache = path.join(root, "cache");
  if (action === "clean") {
    // Callers stop all consumers first. rm unlinks symlinks rather than
    // following them, including links inside immutable Vyper package trees.
    fs.rmSync(cache, { recursive: true, force: true });
    return;
  }
  if (entries.length > 0) {
    if (!owned(info) || !owned(directory(cache)))
      throw new Error(
        "Existing compiler cache ownership/mode differs from 65532:65532/0750; stop its consumers and explicitly clean it first",
      );
    return;
  }
  assign(root);
  fs.mkdirSync(cache, { mode: 0o750 });
  assign(cache);
}

try {
  if (process.argv[2] === "--container") {
    if (process.argv.length !== 4) throw new Error("Expected init or clean");
    manageCache(process.argv[3]);
  } else {
    await managePreviewCache(
      process.argv[2],
      process.argv.length > 3 ? process.argv.slice(3) : undefined,
    );
  }
} catch (error) {
  console.error(`preview compiler cache: ${error.message}`);
  process.exitCode = 1;
}
