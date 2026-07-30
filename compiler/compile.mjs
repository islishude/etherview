import { createRequire } from "node:module";
import { fileURLToPath } from "node:url";

const require = createRequire(import.meta.url);
const solc = require("solc");
const solcPackage = require("solc/package.json");
const wrapperPath = fileURLToPath(import.meta.url);
const requiredNodeVersion = "v26.5.0";
const requiredWrapperPackageVersion = "0.8.36";
const selfTestResult = {
  node_version: requiredNodeVersion,
  wrapper_package: `solc@${requiredWrapperPackageVersion}`,
  permissions: "restricted",
};

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

const [mode, artifactPath, expectedVersion] = process.argv.slice(2);

if (mode === "--self-test") {
  const deniedPermissions = [
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
    process.version !== requiredNodeVersion ||
    solcPackage.version !== requiredWrapperPackageVersion ||
    typeof solc.setupMethods !== "function" ||
    typeof process.permission?.has !== "function" ||
    !process.permission.has("fs.read", wrapperPath) ||
    deniedPermissions.some((permission) => process.permission.has(permission))
  ) {
    fail("solc-js runtime self-test failed");
  } else {
    process.stdout.write(JSON.stringify(selfTestResult));
  }
} else if (mode !== "--compile" || !artifactPath || !expectedVersion) {
  fail("invalid solc-js wrapper invocation");
} else {
  try {
    const compiler = solc.setupMethods(require(artifactPath));
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
