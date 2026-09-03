import { writeFileSync } from "node:fs";
import { createRequire } from "node:module";
import { isSea } from "node:sea";

import solcPackage from "solc/package.json" with { type: "json" };
import solcWrapper from "solc/wrapper";

const requiredNodeVersion = "v26.8.1";
const requiredWrapperPackageVersion = "0.8.36";
const fixedExecArgv = [
  "--permission",
  "--disable-sigusr1",
  "--no-addons",
  "--no-global-search-paths",
  "--max-old-space-size=384",
];
const selfTestResult = {
  schema: "etherview-solcjs-sea-self-test-v1",
  sea: true,
  node_version: requiredNodeVersion,
  wrapper_package: `solc@${requiredWrapperPackageVersion}`,
  exec_argv: fixedExecArgv,
  permissions: "restricted",
  write_denied: true,
};
const artifactRequire = createRequire(process.execPath);

function fail(message) {
  process.stderr.write(`${message}\n`);
  process.exitCode = 1;
}

function normalizeVersion(value) {
  return value.trim().replace(/^v/, "").replace(/\.Emscripten\.clang$/, "");
}

async function readStandardInput() {
  const chunks = [];
  for await (const chunk of process.stdin) {
    chunks.push(chunk);
  }
  return Buffer.concat(chunks).toString("utf8");
}

function sameStrings(left, right) {
  return (
    left.length === right.length &&
    left.every((value, index) => value === right[index])
  );
}

function writeIsDenied() {
  const deniedPath = `${process.env.TMPDIR ?? process.cwd()}/etherview-solcjs-self-test-write`;
  try {
    writeFileSync(deniedPath, "denied", { flag: "wx" });
    return false;
  } catch (error) {
    return error?.code === "ERR_ACCESS_DENIED";
  }
}

function childProcessIsDenied() {
  try {
    artifactRequire("node:child_process").spawnSync(process.execPath, ["--version"]);
    return false;
  } catch (error) {
    return error?.code === "ERR_ACCESS_DENIED";
  }
}

async function networkIsDenied() {
  try {
    await fetch("http://127.0.0.1:65534");
    return false;
  } catch (error) {
    return error?.cause?.code === "ERR_ACCESS_DENIED";
  }
}

async function main() {
  const invocationArguments = process.argv.slice(2);
  const [mode, artifactPath, expectedVersion] = invocationArguments;

  if (mode === "--self-test") {
    const deniedPermissions = [
      "fs.read",
      "fs.write",
      "net",
      "child",
      "worker",
      "addons",
      "ffi",
      "wasi",
      "inspector",
    ];
    if (
      invocationArguments.length !== 1 ||
      !isSea() ||
      process.version !== requiredNodeVersion ||
      solcPackage.version !== requiredWrapperPackageVersion ||
      typeof solcWrapper !== "function" ||
      typeof process.permission?.has !== "function" ||
      !sameStrings(process.execArgv, fixedExecArgv) ||
      deniedPermissions.some((permission) => process.permission.has(permission)) ||
      !writeIsDenied() ||
      !childProcessIsDenied() ||
      !(await networkIsDenied())
    ) {
      fail("solc-js SEA self-test failed");
    } else {
      process.stdout.write(JSON.stringify(selfTestResult));
    }
  } else if (
    invocationArguments.length !== 3 ||
    mode !== "--compile" ||
    !artifactPath ||
    !expectedVersion ||
    !artifactPath.startsWith("/") ||
    typeof process.permission?.has !== "function" ||
    !sameStrings(process.execArgv, [
      ...fixedExecArgv,
      `--allow-fs-read=${artifactPath}`,
    ]) ||
    !process.permission.has("fs.read", artifactPath)
  ) {
    fail("invalid solc-js SEA invocation");
  } else {
    try {
      const compiler = solcWrapper(artifactRequire(artifactPath));
      const actualVersion = normalizeVersion(compiler.version());
      if (actualVersion !== normalizeVersion(expectedVersion)) {
        fail("solc-js compiler version mismatch");
      } else {
        const input = await readStandardInput();
        const output = compiler.compile(input);
        if (typeof output !== "string") {
          fail("solc-js compiler returned an invalid output");
        } else {
          process.stdout.write(output);
        }
      }
    } catch {
      fail("solc-js compiler failed");
    }
  }
}

void main();
