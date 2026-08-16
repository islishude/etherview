import { execFileSync } from "node:child_process";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";

import { build } from "esbuild";

const [outputArgument] = process.argv.slice(2);
if (!outputArgument) {
  throw new Error("usage: node build-sea.mjs <output>");
}

const output = resolve(outputArgument);
const buildDirectory = mkdtempSync(join(tmpdir(), "etherview-solcjs-sea-"));
const bundle = join(buildDirectory, "compile.cjs");
const config = join(buildDirectory, "sea-config.json");

mkdirSync(dirname(output), { recursive: true });
mkdirSync(buildDirectory, { recursive: true });

try {
  const bundleResult = await build({
    entryPoints: [resolve("compile.mjs")],
    outfile: bundle,
    bundle: true,
    platform: "node",
    target: "node26",
    format: "cjs",
    sourcemap: false,
    legalComments: "eof",
    logLevel: "info",
    metafile: true,
  });
  const bundleInputs = Object.keys(bundleResult.metafile.inputs).map((path) =>
    path.replaceAll("\\", "/"),
  );
  const includesBundleInput = (input) =>
    bundleInputs.some((path) => path === input || path.endsWith(`/${input}`));
  if (
    !includesBundleInput("node_modules/solc/wrapper.js") ||
    includesBundleInput("node_modules/solc/soljson.js")
  ) {
    throw new Error("SEA bundle must contain solc/wrapper without a default soljson");
  }

  writeFileSync(
    config,
    `${JSON.stringify(
      {
        main: bundle,
        mainFormat: "commonjs",
        output,
        disableExperimentalSEAWarning: true,
        useSnapshot: false,
        useCodeCache: false,
        execArgv: [
          "--permission",
          "--disable-sigusr1",
          "--no-addons",
          "--no-global-search-paths",
          "--max-old-space-size=384",
        ],
        execArgvExtension: "cli",
      },
      null,
      2,
    )}\n`,
    { mode: 0o600 },
  );

  execFileSync(process.execPath, ["--build-sea", config], { stdio: "inherit" });
} finally {
  rmSync(buildDirectory, { recursive: true, force: true });
}
