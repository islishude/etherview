import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import {
  lstatSync,
  readFileSync,
  readdirSync,
  realpathSync,
  statSync,
} from "node:fs";
import { basename, dirname, join, relative, resolve, sep } from "node:path";

function output(command, args, options = {}) {
  return execFileSync(command, args, {
    encoding: "utf8",
    maxBuffer: 16 << 20,
    ...options,
  }).trim();
}

function sha256(path) {
  return createHash("sha256").update(readFileSync(path)).digest("hex");
}

function pathInside(root, path) {
  const child = relative(root, path);
  return child !== ".." && !child.startsWith(`..${sep}`) && !child.startsWith(sep);
}

function walk(root) {
  const paths = [];
  for (const entry of readdirSync(root, { withFileTypes: true })) {
    const path = join(root, entry.name);
    if (entry.isDirectory()) {
      paths.push(...walk(path));
    } else if (entry.isFile() || entry.isSymbolicLink()) {
      paths.push(path);
    } else {
      throw new Error(`unsafe ELF root entry: ${path}`);
    }
  }
  return paths;
}

export function elfSoname(path) {
  let dynamic;
  try {
    dynamic = output("readelf", ["-dW", realpathSync(path)]);
  } catch {
    return "";
  }
  return dynamic.match(/\(SONAME\)[^\[]*\[([^\]]+)\]/)?.[1] ?? "";
}

export function indexELFProviders(rootArgument) {
  const root = resolve(rootArgument);
  const index = new Map();
  for (const path of walk(root)) {
    if (!basename(path).includes(".so")) continue;
    const soname = elfSoname(path);
    if (!soname) continue;
    if (soname !== basename(soname)) {
      throw new Error(`ELF root contains an invalid SONAME: ${path}`);
    }
    const resolvedPath = realpathSync(path);
    if (!pathInside(root, resolvedPath)) {
      throw new Error(`ELF provider escapes its root: ${path}`);
    }
    const candidates = index.get(soname) ?? [];
    candidates.push({ path, resolvedPath, digest: sha256(resolvedPath) });
    index.set(soname, candidates);
  }
  for (const [soname, candidates] of index) {
    if (new Set(candidates.map((candidate) => candidate.digest)).size !== 1) {
      throw new Error(`ELF root has conflicting providers for ${soname}`);
    }
  }
  return index;
}

export function validateAssembledRuntime({
  targetRoot: targetRootArgument,
  executorPath,
  interpreter,
  dependencies,
}) {
  const targetRoot = resolve(targetRootArgument);
  if (!executorPath.startsWith("/") || !interpreter.startsWith("/")) {
    throw new Error("assembled runtime paths must be absolute");
  }
  const targetExecutor = join(targetRoot, executorPath.slice(1));
  if (!pathInside(targetRoot, targetExecutor) || !statSync(targetExecutor).isFile()) {
    throw new Error("assembled target root does not contain the SEA executor");
  }
  const runtimeRoot = dirname(executorPath);
  const libraryDirectories = [
    join(targetRoot, runtimeRoot.slice(1), "lib"),
    ...dependencies
      .filter((dependency) => dependency.provider === "base")
      .map((dependency) => join(targetRoot, dirname(dependency.path).slice(1))),
  ];
  const closure = output("lddtree", ["-l", targetExecutor], {
    env: {
      ...process.env,
      LD_LIBRARY_PATH: [...new Set(libraryDirectories)].join(":"),
    },
  });
  const closurePaths = closure.split("\n").filter(Boolean);
  if (
    closurePaths.length < 2 ||
    closurePaths.some((path) => /not found|\bNone\b/i.test(path))
  ) {
    throw new Error("assembled target root has unresolved SEA dependencies");
  }

  function targetPath(path) {
    if (!path.startsWith("/")) {
      throw new Error(`assembled target root returned an unsafe path: ${path}`);
    }
    const hostPath = path === targetRoot || path.startsWith(`${targetRoot}${sep}`)
      ? resolve(path)
      : join(targetRoot, path.slice(1));
    if (!pathInside(targetRoot, hostPath)) {
      throw new Error(`assembled target dependency escapes root: ${path}`);
    }
    return {
      hostPath,
      runtimePath: `/${relative(targetRoot, hostPath).split(sep).join("/")}`,
    };
  }

  const targetEntry = targetPath(closurePaths[0]);
  if (targetEntry.hostPath !== targetExecutor) {
    throw new Error("assembled target root resolved the wrong SEA executable");
  }
  const expected = new Map(
    dependencies.map((dependency) => [dependency.soname, dependency]),
  );
  if (expected.size !== dependencies.length) {
    throw new Error("assembled target dependency inventory is duplicated");
  }
  const resolved = new Map();
  for (const closurePath of closurePaths.slice(1)) {
    if (resolve(closurePath) === resolve(interpreter)) continue;
    const target = targetPath(closurePath);
    const targetInfo = lstatSync(target.hostPath);
    const resolvedTarget = realpathSync(target.hostPath);
    if (
      (!targetInfo.isFile() && !targetInfo.isSymbolicLink()) ||
      !pathInside(targetRoot, resolvedTarget) ||
      !statSync(resolvedTarget).isFile()
    ) {
      throw new Error(`assembled target dependency is unavailable: ${closurePath}`);
    }
    const soname = elfSoname(target.hostPath);
    const dependency = expected.get(soname);
    if (!soname || !dependency) {
      throw new Error(`assembled target root contains an unexpected dependency: ${closurePath}`);
    }
    const digest = sha256(resolvedTarget);
    if (resolved.has(soname)) {
      if (resolved.get(soname).digest !== digest) {
        throw new Error(`assembled target root has conflicting providers for ${soname}`);
      }
      continue;
    }
    if (dependency.provider === "runtime") {
      if (target.runtimePath !== `${runtimeRoot}/${dependency.path}`) {
        throw new Error(`assembled target root bypassed private dependency ${soname}`);
      }
    } else if (
      dependency.provider !== "base" ||
      target.runtimePath !== dependency.path
    ) {
      throw new Error(`assembled target root resolved unexpected base dependency ${soname}`);
    }
    resolved.set(soname, { ...target, digest });
  }
  if (resolved.size !== expected.size) {
    throw new Error("assembled target root dependency closure is incomplete");
  }
  return resolved;
}
