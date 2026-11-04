import { spawnSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { pathToFileURL } from "node:url";

const root = path.resolve(import.meta.dirname, "../..");
const helperImage = "node:26.13.0-slim";

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
  const { runCompose } = await import("../../.github/scripts/compose.mjs");
  if (!["init", "clean"].includes(action)) throw new Error("Expected init or clean");
  const config = JSON.parse(runCompose([...composeArgs, "config", "--format", "json"]));
  const mount = config.services.verification.volumes.find(
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
  const runHelper = (helperAction) =>
    docker(
      [
        "run",
        "--rm",
        "--network=none",
        "--read-only",
        helperAction === "verify" ? "--user=65532:65532" : "--user=0:0",
        "--cap-drop=ALL",
        ...(helperAction === "verify"
          ? []
          : ["--cap-add=CHOWN", "--cap-add=FOWNER", "--cap-add=DAC_OVERRIDE"]),
        "--security-opt=no-new-privileges:true",
        "--mount",
        `type=bind,src=${source},dst=/compiler-cache`,
        "--mount",
        `type=bind,src=${path.join(root, "preview/scripts/compiler-cache.mjs")},dst=/compiler-cache.mjs,readonly`,
        "--entrypoint=node",
        helperImage,
        "/compiler-cache.mjs",
        "--container",
        helperAction,
      ],
      { stdio: "inherit", timeout: 60_000 },
    );
  runHelper(action);
  if (action === "init") runHelper("verify");
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
export function manageCache(action, root = "/compiler-cache") {
  if (!["init", "clean", "verify"].includes(action))
    throw new Error("Expected init, clean or verify");
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
  if (action === "verify") {
    if (process.getuid() !== uid || process.getgid() !== gid)
      throw new Error("Compiler cache verification requires application UID/GID 65532:65532");
    if (!owned(info) || !owned(directory(cache)))
      throw new Error(
        "Compiler cache ownership/mode differs from 65532:65532/0750 for the application user; stop its consumers and explicitly clean it first",
      );
    probeCache(cache);
    return;
  }
  if (entries.length > 0) {
    // Docker Desktop may present the bind owner as the container user. Check
    // ownership in the separate unprivileged helper, never from this root view.
    if ((info.mode & 0o7777) !== 0o750 || (directory(cache).mode & 0o7777) !== 0o750)
      throw new Error(
        "Existing compiler cache ownership/mode differs from 65532:65532/0750; stop its consumers and explicitly clean it first",
      );
    return;
  }
  assign(root);
  fs.mkdirSync(cache, { mode: 0o750 });
  assign(cache);
}

// Exercise the filesystem operations needed to install artifacts without touching
// existing entries. A unique private directory also isolates concurrent probes.
function probeCache(cache) {
  let probe;
  try {
    probe = fs.mkdtempSync(path.join(cache, ".preview-access-"));
    const temporary = path.join(probe, "temporary");
    const installed = path.join(probe, "installed");
    fs.writeFileSync(temporary, "preview compiler cache probe", { flag: "wx", mode: 0o600 });
    fs.chmodSync(temporary, 0o400);
    fs.renameSync(temporary, installed);
    if (fs.readFileSync(installed, "utf8") !== "preview compiler cache probe")
      throw new Error("Compiler cache probe readback failed");
  } finally {
    if (probe) fs.rmSync(probe, { recursive: true, force: true });
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
  try {
    if (process.argv[2] === "--container") {
      if (process.argv.length !== 4) throw new Error("Expected init, clean or verify");
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
}
