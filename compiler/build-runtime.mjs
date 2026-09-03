import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import {
  chmodSync,
  copyFileSync,
  cpSync,
  existsSync,
  lstatSync,
  mkdirSync,
  readdirSync,
  readFileSync,
  realpathSync,
  statSync,
  writeFileSync,
} from "node:fs";
import { basename, dirname, join, relative, resolve, sep } from "node:path";

import {
  elfSoname,
  indexELFProviders,
  validateAssembledRuntime,
} from "./elf-runtime.mjs";

const [executorArgument, targetRootArgument, licensesArgument] =
  process.argv.slice(2);
if (!executorArgument || !targetRootArgument || !licensesArgument) {
  throw new Error(
    "usage: node build-runtime.mjs <executor> <target-root> <licenses>",
  );
}

const executor = resolve(executorArgument);
const runtimeRoot = dirname(executor);
const privateLibraryRoot = join(runtimeRoot, "lib");
const targetRoot = resolve(targetRootArgument);
const licensesRoot = resolve(licensesArgument);
const targetRuntimeRoot = join(targetRoot, runtimeRoot.slice(1));
const fixedExecArgv = [
  "--permission",
  "--disable-sigusr1",
  "--no-addons",
  "--no-global-search-paths",
  "--max-old-space-size=384",
];

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

function md5(path) {
  return createHash("md5").update(readFileSync(path)).digest("hex");
}

function compareText(left, right) {
  return left < right ? -1 : left > right ? 1 : 0;
}

function pathInside(root, path) {
  const child = relative(root, path);
  return child !== ".." && !child.startsWith(`..${sep}`) && !child.startsWith(sep);
}

function elfInterpreter(path) {
  const headers = output("readelf", ["-lW", path]);
  const match = headers.match(/Requesting program interpreter:\s*([^\]]+)\]/);
  if (!match || !match[1].startsWith("/")) {
    throw new Error("SEA ELF interpreter is unavailable");
  }
  return match[1];
}

function builderPackageIdentity(path) {
  let owner = "";
  for (const candidate of [path, realpathSync(path)]) {
    try {
      const line = output("dpkg-query", ["-S", candidate], {
        stdio: ["ignore", "pipe", "pipe"],
      }).split("\n")[0];
      owner = line.slice(0, line.indexOf(":"));
      if (owner) break;
    } catch {
      // Try the resolved path before rejecting an unattributed library.
    }
  }
  if (!owner) {
    throw new Error(`ELF dependency has no Debian package owner: ${path}`);
  }
  const fields = output("dpkg-query", [
    "-W",
    "-f=${binary:Package}\t${Version}\t${Architecture}",
    owner,
  ]).split("\t");
  if (fields.length !== 3 || fields.some((field) => !field)) {
    throw new Error(`ELF dependency package identity is incomplete: ${path}`);
  }
  const packageName = fields[0];
  const documentationName = packageName.split(":")[0];
  const copyright = `/usr/share/doc/${documentationName}/copyright`;
  const copyrightInfo = lstatSync(copyright);
  if (!copyrightInfo.isFile() && !copyrightInfo.isSymbolicLink()) {
    throw new Error(`ELF dependency license is unsafe: ${copyright}`);
  }
  const resolvedCopyright = realpathSync(copyright);
  if (!statSync(resolvedCopyright).isFile()) {
    throw new Error(`ELF dependency license is unavailable: ${copyright}`);
  }
  return {
    package: packageName,
    package_version: fields[1],
    package_architecture: fields[2],
    copyright: resolvedCopyright,
  };
}

function targetPackageIdentity(path) {
  const resolvedPath = realpathSync(path);
  if (!pathInside(targetRoot, resolvedPath) || !statSync(resolvedPath).isFile()) {
    throw new Error(`base ELF dependency escapes target root: ${path}`);
  }
  const relativePath = relative(targetRoot, resolvedPath).split(sep).join("/");
  const statusRoot = join(targetRoot, "var/lib/dpkg/status.d");
  const owners = [];
  for (const entry of readdirSync(statusRoot, { withFileTypes: true })) {
    if (!entry.isFile() || !entry.name.endsWith(".md5sums")) continue;
    const sums = readFileSync(join(statusRoot, entry.name), "utf8")
      .split("\n")
      .filter(Boolean);
    for (const line of sums) {
      const match = line.match(/^([0-9a-f]{32})\s+(.+)$/);
      if (!match || match[2].replace(/^\.\//, "") !== relativePath) continue;
      if (md5(resolvedPath) !== match[1]) {
        throw new Error(`base ELF dependency differs from package metadata: ${path}`);
      }
      owners.push(entry.name.slice(0, -".md5sums".length));
    }
  }
  if (owners.length !== 1) {
    throw new Error(`base ELF dependency has ambiguous package ownership: ${path}`);
  }
  const statusPath = join(statusRoot, owners[0]);
  if (!existsSync(statusPath) || !lstatSync(statusPath).isFile()) {
    throw new Error(`base ELF dependency package status is unavailable: ${path}`);
  }
  const status = readFileSync(statusPath, "utf8");
  const packageName = status.match(/^Package:\s*(\S+)\s*$/m)?.[1] ?? "";
  const packageVersion = status.match(/^Version:\s*(\S+)\s*$/m)?.[1] ?? "";
  const packageArchitecture = status.match(/^Architecture:\s*(\S+)\s*$/m)?.[1] ?? "";
  if (!packageName || !packageVersion || !packageArchitecture) {
    throw new Error(`base ELF dependency package identity is incomplete: ${path}`);
  }
  const copyright = join(targetRoot, "usr/share/doc", packageName, "copyright");
  if (!existsSync(copyright)) {
    throw new Error(`base ELF dependency license is unavailable: ${path}`);
  }
  const resolvedCopyright = realpathSync(copyright);
  if (
    !pathInside(targetRoot, resolvedCopyright) ||
    !statSync(resolvedCopyright).isFile()
  ) {
    throw new Error(`base ELF dependency license is unsafe: ${path}`);
  }
  return {
    package: packageName,
    package_version: packageVersion,
    package_architecture: packageArchitecture,
    copyright: resolvedCopyright,
  };
}

mkdirSync(privateLibraryRoot, { recursive: true });
mkdirSync(licensesRoot, { recursive: true });

const interpreter = elfInterpreter(executor);
const interpreterInTarget = join(targetRoot, interpreter.slice(1));
if (
  !pathInside(targetRoot, interpreterInTarget) ||
  !statSync(interpreterInTarget).isFile() ||
  !pathInside(targetRoot, realpathSync(interpreterInTarget))
) {
  throw new Error(`target root does not provide ELF interpreter ${interpreter}`);
}

const targetCandidates = indexELFProviders(targetRoot);

const closure = output("lddtree", ["-l", executor])
  .split("\n")
  .filter(Boolean)
  .map((path) => resolve(path));
if (closure.length < 2 || closure[0] !== executor) {
  throw new Error("lddtree returned an invalid SEA closure");
}

const dependencies = [];
const runtimeSonames = new Map();
const sourceSonames = new Map();
for (const sourcePath of closure.slice(1)) {
  if (sourcePath === resolve(interpreter)) continue;
  if (!sourcePath.startsWith("/") || !statSync(sourcePath).isFile()) {
    throw new Error(`lddtree returned an unresolved dependency: ${sourcePath}`);
  }
  const soname = elfSoname(sourcePath);
  if (!soname || soname !== basename(soname)) {
    throw new Error(`invalid ELF SONAME for ${sourcePath}`);
  }
  const sourceDigest = sha256(realpathSync(sourcePath));
  if (sourceSonames.has(soname)) {
    if (sourceSonames.get(soname) !== sourceDigest) {
      throw new Error(`SEA closure has conflicting providers for ${soname}`);
    }
    continue;
  }
  sourceSonames.set(soname, sourceDigest);

  const baseCandidates = (targetCandidates.get(soname) ?? [])
    .slice()
    .sort((left, right) => compareText(left.path, right.path));
  let provider;
  let resolvedPath;
  let identity;
  if (baseCandidates.length > 0) {
    provider = "base";
    resolvedPath = `/${relative(targetRoot, baseCandidates[0].path).split(sep).join("/")}`;
    identity = targetPackageIdentity(baseCandidates[0].path);
  } else {
    provider = "runtime";
    identity = builderPackageIdentity(sourcePath);
    const destination = join(privateLibraryRoot, soname);
    if (
      runtimeSonames.has(soname) &&
      runtimeSonames.get(soname) !== sourceDigest
    ) {
      throw new Error(`conflicting runtime ELF dependency ${soname}`);
    }
    copyFileSync(realpathSync(sourcePath), destination);
    chmodSync(destination, 0o444);
    runtimeSonames.set(soname, sha256(destination));
    resolvedPath = `lib/${soname}`;

    const licenseName = `${identity.package.replaceAll(":", "_")}.copyright`;
    const licenseDestination = join(licensesRoot, licenseName);
    copyFileSync(identity.copyright, licenseDestination);
    chmodSync(licenseDestination, 0o444);
  }
  dependencies.push({
    soname,
    provider,
    path: resolvedPath,
    package: identity.package,
    package_version: identity.package_version,
    package_architecture: identity.package_architecture,
    license_sha256: sha256(identity.copyright),
  });
}
dependencies.sort((left, right) => compareText(left.soname, right.soname));

const files = [
  {
    path: basename(executor),
    kind: "executor",
    sha256: sha256(executor),
  },
  ...[...runtimeSonames.keys()]
    .sort(compareText)
    .map((soname) => ({
      path: `lib/${soname}`,
      kind: "library",
      soname,
      sha256: runtimeSonames.get(soname),
    })),
];
const manifest = {
  schema: "etherview-solcjs-sea-runtime-v1",
  node_version: "v26.8.1",
  wrapper_package: "solc@0.8.36",
  bundle_builder: "esbuild@0.28.2",
  sea: {
    main_format: "commonjs",
    use_snapshot: false,
    use_code_cache: false,
    exec_argv: fixedExecArgv,
    exec_argv_extension: "cli",
  },
  elf_interpreter: interpreter,
  dependencies,
  files,
};

const manifestPath = join(runtimeRoot, "runtime-manifest.json");
chmodSync(executor, 0o555);
chmodSync(privateLibraryRoot, 0o555);
writeFileSync(manifestPath, `${JSON.stringify(manifest)}\n`, { mode: 0o444 });
chmodSync(runtimeRoot, 0o555);

mkdirSync(dirname(targetRuntimeRoot), { recursive: true });
cpSync(runtimeRoot, targetRuntimeRoot, {
  recursive: true,
  dereference: false,
  errorOnExist: true,
  force: false,
});

const resolvedDependencies = validateAssembledRuntime({
  targetRoot,
  executorPath: `/${relative(targetRoot, join(targetRuntimeRoot, basename(executor)))
    .split(sep)
    .join("/")}`,
  interpreter,
  dependencies,
});
for (const dependency of dependencies) {
  if (dependency.provider !== "base") continue;
  const identity = targetPackageIdentity(
    resolvedDependencies.get(dependency.soname).hostPath,
  );
  if (
    identity.package !== dependency.package ||
    identity.package_version !== dependency.package_version ||
    identity.package_architecture !== dependency.package_architecture ||
    sha256(identity.copyright) !== dependency.license_sha256
  ) {
    throw new Error(`assembled base dependency identity changed for ${dependency.soname}`);
  }
}
