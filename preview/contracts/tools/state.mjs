import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";

import { saveState, stringify } from "../client.mjs";
import { catalog } from "./catalog.mjs";

export function safeID(id) {
  if (!/^[a-zA-Z0-9][a-zA-Z0-9._-]{0,100}$/.test(id)) throw Error("Invalid run ID");
  return id;
}
export function privateDirectory(dir) {
  fs.mkdirSync(dir, { recursive: true, mode: 0o700 });
  const stat = fs.lstatSync(dir);
  if (!stat.isDirectory() || stat.isSymbolicLink() || stat.mode & 0o077)
    throw Error("Run directory must be private (0700)");
}
export function openRun(base, id) {
  const dir = path.join(base, "runs", safeID(id));
  privateDirectory(dir);
  const file = path.join(dir, "state.json");
  const stat = fs.lstatSync(file);
  if (!stat.isFile() || stat.isSymbolicLink() || stat.size > 32 * 1024 * 1024 || stat.mode & 0o077)
    throw Error("Invalid private run state");
  const state = JSON.parse(fs.readFileSync(file, "utf8"));
  if (state.schema !== 1 || state.id !== id) throw Error("Unsupported run state");
  return { dir, state, save: () => saveState(file, state) };
}
export function createRun(base, scenarios, endpoints, to) {
  privateDirectory(base);
  const id =
    new Date().toISOString().replace(/[:.]/g, "-") + "-" + crypto.randomBytes(4).toString("hex");
  const dir = path.join(base, "runs", id);
  privateDirectory(dir);
  privateDirectory(path.join(dir, "private"));
  const state = {
    schema: 1,
    id,
    createdAt: new Date().toISOString(),
    endpoints,
    to,
    scenarios: Object.fromEntries(
      scenarios.map((name) => [
        name,
        { name, steps: {}, entities: {}, checks: [], status: "created", actions: [] },
      ]),
    ),
    funding: { steps: {} },
  };
  const save = () => saveState(path.join(dir, "state.json"), state);
  save();
  return { dir, state, save };
}
export function acquireLock(base, identity) {
  const dir = path.join(base, "locks");
  fs.mkdirSync(dir, { recursive: true, mode: 0o700 });
  const file = path.join(dir, crypto.createHash("sha256").update(identity).digest("hex") + ".lock");
  let fd;
  try {
    fd = fs.openSync(file, "wx", 0o600);
  } catch {
    throw Error(
      "Another writer or an interrupted writer holds the lock; inspect locks before removing a stale lock",
    );
  }
  fs.writeFileSync(fd, stringify({ pid: process.pid, started: new Date().toISOString() }));
  fs.closeSync(fd);
  return () => fs.unlinkSync(file);
}
export function publicReport(state) {
  return {
    schema: 1,
    id: state.id,
    createdAt: state.createdAt,
    chainId: state.chainId,
    genesis: state.genesis,
    owner: state.owner,
    recipient: state.to,
    funding: Object.values(state.funding.steps).map((step) => ({
      hash: step.hash,
      status: step.status,
      receipt: step.receipt,
      valueWei: step.valueWei,
    })),
    manualStatus: "not_run",
    scenarios: Object.values(state.scenarios).map((s) => ({
      name: s.name,
      title: catalog[s.name].title,
      status: s.status,
      error: s.error,
      entities: s.entities,
      checks: s.checks,
      actions: s.actions,
      availableActions: catalog[s.name].actions,
      bindingJobs: [...Object.values(s.bindingJobs ?? {}), ...(s.staleBindingJobs ?? [])],
      artifacts: Object.entries(s.deployments ?? {}).map(([id, d]) => ({
        id,
        address: d.address,
        artifact: d.artifact,
        identity: d.identity,
        constructorArguments: d.constructorArguments,
        job: d.job,
      })),
      transactions: Object.entries(s.steps)
        .filter(([, v]) => v.hash)
        .map(([step, v]) => ({
          step,
          hash: v.hash,
          kind: v.kind,
          status: v.status,
          receipt: v.receipt,
          expected: v.expected,
          to: v.to,
          valueWei: v.valueWei,
          transactionType: v.transactionType,
          rejectionCode: v.rejectionCode,
          expectedRejection: v.expectedRejection,
          blobs: v.blobs,
        })),
      links: links(state, s),
      manual: catalog[s.name].checks,
    })),
    manual: [
      "API 已发布不等于 UI 已验收；打开链接后检查，状态变化后点击 Refresh/刷新。",
      "检查中文/英文、明暗主题、桌面/390px、分页、链接和返回。",
      "首页、搜索、统计、状态、genesis 可直接浏览；pending 是短暂状态。",
      "真实钱包签名、充值和通知权限由浏览器用户操作。管理员、外部来源和真实推送须另行验收。",
    ],
  };
}
export function links(state, s) {
  const base = state.endpoints.app;
  const out = [];
  const add = (label, route) => out.push({ label, url: base + route });
  for (const [label, entity] of Object.entries(s.entities)) {
    if (typeof entity === "string" && /^0x[0-9a-f]{40}$/i.test(entity))
      add(label, "/address/" + entity);
    if (
      typeof entity === "string" &&
      /^0x[0-9a-f]{40}$/i.test(entity) &&
      (label === "proxy" || label === "proxyReuse" || s.deployments?.[label])
    )
      add(label + " source", "/address/" + entity + "#code");
  }
  if (s.entities.name) add("ENS", "/name/" + encodeURIComponent(s.entities.name));
  for (const [step, t] of Object.entries(s.steps)) {
    if (t.hash && t.status !== "rejected")
      add(step, (t.kind === "userop" ? "/user-op/" : "/tx/") + t.hash);
    if (t.receipt?.blockHash) {
      add(step + " block", "/blocks/" + t.receipt.blockHash);
      if (t.blobs) add(step + " blobs", "/blocks/" + t.receipt.blockHash + "?tab=blobs");
    }
  }
  if (s.name === "eip7702" && s.entities.authority)
    add("delegation", "/address/" + s.entities.authority + "?tab=delegation");
  if (s.entities.erc20) add("holders", "/token/" + s.entities.erc20 + "?tab=holders");
  if (s.entities.erc721) add("NFT", "/nft/" + s.entities.erc721 + "/" + s.entities.tokenID);
  if (s.name === "wallet") {
    for (const tab of ["overview", "billing", "api-keys", "watchlist"])
      add(tab, "/account?tab=" + tab);
    add("CSV", "/address/" + state.to + "/export");
  }
  return out;
}
export function writeReport(run) {
  const report = publicReport(run.state);
  fs.writeFileSync(path.join(run.dir, "report.json"), stringify(report), { mode: 0o600 });
  const lines = [
    "# Preview " + report.id,
    "",
    "Chain: " + report.chainId + "; genesis: " + report.genesis,
    "",
    ...report.funding.map(
      (step) =>
        "- Funding: [" +
        step.hash +
        "](" +
        run.state.endpoints.app +
        "/tx/" +
        step.hash +
        ") — " +
        step.status,
    ),
    "",
    ...report.manual.map((x) => "- " + x),
  ];
  for (const s of report.scenarios) {
    lines.push(
      "",
      "## " + s.name + " — " + s.status,
      "",
      ...(s.error ? [s.error, ""] : []),
      ...s.manual.map((x) => "- [ ] " + x),
      "",
      ...s.links.map((x) => "- [" + x.label + "](" + x.url + ")"),
      "",
      ...s.transactions
        .filter((t) => t.valueWei !== undefined)
        .map(
          (t) =>
            "- " +
            t.step +
            ": " +
            t.valueWei +
            " wei, " +
            t.transactionType +
            (t.to ? " → " + t.to : " (contract creation)") +
            "; expected " +
            t.expected,
        ),
      ...s.checks.map(
        (x) =>
          "- API " + x.label + ": " + x.status + " — expected `" + JSON.stringify(x.expected) + "`",
      ),
    );
    const quote = (value) => "'" + value.replaceAll("'", "'\"'\"'") + "'";
    const base = path.dirname(path.dirname(run.dir));
    const custom =
      base === path.resolve(import.meta.dirname, "../../../.local/preview-tools")
        ? ""
        : " --dir " + quote(base);
    for (const a of s.availableActions) {
      const args = "act --run " + report.id + " --scenario " + s.name + custom + " " + a;
      lines.push("- 下一步: `make preview-tool ARGS=" + quote(args) + "`");
    }
  }
  fs.writeFileSync(path.join(run.dir, "report.md"), lines.join("\n") + "\n", { mode: 0o600 });
  return report;
}
