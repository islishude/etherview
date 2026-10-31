import { execFileSync } from "node:child_process";
import path from "node:path";
import { parseArgs } from "node:util";

import { isAddress } from "viem";

import { stringify, contracts } from "../client.mjs";
import { compile, root } from "./artifacts.mjs";
import { catalog, lifecycle, actionProblem } from "./catalog.mjs";
import { Engine, preflight, checkScenario, endpoint } from "./engine.mjs";
import { setup, action } from "./scenarios.mjs";
import { createRun, openRun, acquireLock, writeReport } from "./state.mjs";

export function argumentsFor(argv) {
  const { values, positionals } = parseArgs({
    args: argv,
    allowPositionals: true,
    options: {
      ...Object.fromEntries(
        [
          "run",
          "scenario",
          "to",
          "dir",
          "rpc",
          "bundler",
          "app",
          "timeout",
          "project",
          "compose-files",
          "blob-helper",
        ].map((key) => [key, { type: "string" }]),
      ),
      lifecycle: { type: "boolean" },
      "no-verify": { type: "boolean" },
      json: { type: "boolean" },
      help: { type: "boolean" },
    },
  });
  if (values.to && !isAddress(values.to)) throw Error("--to must be an Ethereum address");
  const timeout = Number(values.timeout ?? 180);
  if (!Number.isInteger(timeout) || timeout < 1 || timeout > 1800)
    throw Error("--timeout must be 1–1800 seconds");
  values.dir = path.resolve(values.dir ?? path.join(root, ".local/preview-tools"));
  for (const key of ["rpc", "bundler", "app"]) if (values[key]) values[key] = endpoint(values[key]);
  return { values, positionals };
}
export function safeError(error) {
  if (error instanceof SyntaxError)
    return "Malformed saved state or input; inspect the private file locally.";
  if (error.shortMessage || error.cause || error.code || error.stdout || error.stderr)
    return "Preview operation failed; inspect the saved hash and check endpoint readiness. Nested diagnostics were withheld.";
  return error.message?.replace(/https?:\/\/\S+/g, "[endpoint]") ?? "Preview operation failed";
}
function summary(report, dir) {
  console.log("Run: " + report.id);
  for (const s of report.scenarios) {
    console.log(s.name + ": " + s.status + (s.error ? " — " + s.error : ""));
    for (const link of s.links) console.log("  " + link.label + ": " + link.url);
  }
  console.log("Report: " + path.join(dir, "report.md"));
  console.log(
    "API checks are separate from manual UI acceptance. Refresh open pages after each action.",
  );
}
export async function main(argv = process.argv.slice(2)) {
  const { values: o, positionals: p } = argumentsFor(argv);
  const command = p[0] ?? "help";
  const maxPositionals = ["run", "wallet", "act"].includes(command) ? 2 : 1;
  if (p.length > maxPositionals) throw Error("Unexpected positional arguments");
  if (o.help || command === "help") {
    console.log(
      "Preview tools: list | doctor | run SCENARIO|all [--lifecycle] | wallet prepare --to ADDRESS | act ACTION --run ID [--scenario NAME] | resume --run ID | check --run ID | report --run ID\nOptions: --dir PATH --rpc ORIGIN --bundler ORIGIN --app ORIGIN --timeout SECONDS --no-verify --json --project NAME --compose-files FILE,...\nState transitions are explicit. check/report never submit transactions. Default endpoints use PREVIEW_HTTPS_PORT and the trusted Preview CA.",
    );
    return;
  }
  if (command === "list") {
    console.log(
      o.json
        ? stringify(catalog)
        : Object.entries(catalog)
            .map(
              ([id, c]) =>
                id +
                " — " +
                c.title +
                "\n  requires: " +
                (c.requires.join(", ") || "chain") +
                "; actions: " +
                (c.actions.join(", ") || "none"),
            )
            .join("\n"),
    );
    return;
  }
  const port = process.env.PREVIEW_HTTPS_PORT;
  const origin = (host) =>
    "https://" + host + ".etherview.localhost" + (port && port !== "443" ? ":" + port : "");
  const endpoints = {
    rpc: o.rpc ?? origin("geth"),
    bundler: o.bundler ?? origin("bundler"),
    app: o.app ?? origin("app"),
  };
  if (command === "doctor") {
    const { ctx, features } = await preflight(endpoints);
    let supported = [],
      bundlerReady = false;
    try {
      supported = await ctx.bundler.request({ method: "eth_supportedEntryPoints" });
      bundlerReady = supported.some(
        (address) => address.toLowerCase() === contracts.EntryPoint.address.toLowerCase(),
      );
    } catch {
      /* Report isolated capability failure. */
    }
    const tools = {};
    for (const [name, args] of [
      ["go", ["version"]],
      ["docker", ["version", "--format", "{{.Server.Version}}"]],
    ]) {
      try {
        tools[name] = execFileSync(name, args, {
          encoding: "utf8",
          timeout: 5000,
          stdio: ["ignore", "pipe", "pipe"],
        }).trim();
      } catch {
        tools[name] = "unavailable";
      }
    }
    console.log(
      stringify({
        chainId: "48815",
        genesis: ctx.genesis,
        endpoints,
        features,
        entryPoints: supported,
        bundlerReady,
        tools,
        node: process.version,
        scenarios: Object.fromEntries(
          Object.entries(catalog).map(([id, c]) => [
            id,
            id === "aa" && !bundlerReady
              ? "bundler unavailable"
              : id === "blob" && tools.go === "unavailable" && !o["blob-helper"]
                ? "blob signing helper unavailable"
                : c.requires.every((f) => features[f])
                  ? "available"
                  : "missing capability",
          ]),
        ),
      }),
    );
    return;
  }
  let run;
  if (command === "run" || command === "wallet") {
    if (command === "wallet" && (p[1] !== "prepare" || !o.to))
      throw Error("Use wallet prepare --to ADDRESS");
    const name = command === "wallet" ? "wallet" : p[1];
    if (name !== "all" && !catalog[name]) throw Error("Unknown scenario; use list");
    const names = name === "all" ? Object.keys(catalog) : [name];
    // run all still has a useful default browser wallet: the documented genesis account.
    const to = o.to ?? (name === "all" ? "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266" : undefined);
    run = createRun(o.dir, names, endpoints, to);
    run.state.options = {
      "no-verify": !!o["no-verify"],
      project: o.project,
      "compose-files": o["compose-files"],
      lifecycle: !!o.lifecycle,
    };
    run.save();
  } else if (["act", "resume", "check", "report"].includes(command)) {
    if (!o.run) throw Error("--run ID is required");
    run = openRun(o.dir, o.run);
    for (const key of ["rpc", "bundler", "app"]) if (o[key]) run.state.endpoints[key] = o[key];
    Object.assign(o, {
      ...run.state.options,
      ...Object.fromEntries(Object.entries(o).filter(([, v]) => v !== undefined)),
    });
  } else throw Error("Unknown command; use help");
  if (command === "report") {
    const report = writeReport(run);
    if (o.json) console.log(stringify(report));
    else summary(report, run.dir);
    return;
  }
  const unlock = acquireLock(o.dir, "run:" + run.state.id);
  let failure = false;
  try {
    let selected = Object.values(run.state.scenarios);
    if (o.scenario) {
      if (!run.state.scenarios[o.scenario]) throw Error("Scenario is not in this run");
      selected = [run.state.scenarios[o.scenario]];
    }
    if (command === "act" && selected.length !== 1)
      throw Error("Select an action scenario with --scenario NAME");
    if (command === "act" && !catalog[selected[0].name].actions.includes(p[1]))
      throw Error("Unsupported action for " + selected[0].name);
    if (command === "act") {
      const problem = actionProblem(selected[0], p[1]);
      if (problem) throw Error(problem);
    }
    const e = new Engine(run, o, command === "check" ? {} : compile(path.join(o.dir, "build")));
    await e.init(command === "check");
    if (command === "check") {
      for (const step of Object.values(run.state.funding.steps))
        if (!(await e.reconcile(step))) throw Error("Funding outcome is unknown: " + step.hash);
    } else await e.fund();
    for (const s of selected) {
      try {
        if (command !== "check") {
          const missing = catalog[s.name].requires.filter((f) => !e.features[f]);
          if (missing.length) throw Error("Missing capabilities: " + missing.join(", "));
          if (!s.setupComplete) await setup(e, s);
          if (command === "act") {
            const name = p[1];
            if (!catalog[s.name].actions.includes(name))
              throw Error("Unsupported action for " + s.name);
            if (s.actions.some((a) => a.status !== "complete"))
              throw Error("Previous action is incomplete; use resume");
            s.actions.push({
              id: "action-" + (s.actions.length + 1) + "-" + name,
              name,
              status: "started",
            });
            run.save();
          }
          if (o.lifecycle && !s.lifecycleQueued) {
            for (const name of lifecycle[s.name] ?? [])
              s.actions.push({
                id: "action-" + (s.actions.length + 1) + "-" + name,
                name,
                status: "started",
              });
            s.lifecycleQueued = true;
            run.save();
          }
          for (const a of s.actions.filter((a) => a.status !== "complete")) {
            await action(e, s, a.name, a.id);
            a.status = "complete";
            run.save();
          }
        }
        await checkScenario(e, s);
      } catch (error) {
        s.status = "incomplete";
        s.error = (s.activeStep ? s.activeStep + ": " : "") + safeError(error);
        failure = true;
        run.save();
      }
      if (!o.json) console.log(s.name + ": " + s.status + (s.error ? " — " + s.error : ""));
      writeReport(run);
    }
  } finally {
    unlock();
    const report = writeReport(run);
    if (o.json) console.log(stringify(report));
    else summary(report, run.dir);
  }
  if (failure) process.exitCode = 1;
}
if (import.meta.main)
  main().catch((e) => {
    console.error(safeError(e));
    process.exitCode = 1;
  });
