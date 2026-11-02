import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";

import {
  createWalletClient,
  http,
  encodeDeployData,
  encodeFunctionData,
  keccak256,
  parseEther,
} from "viem";
import { generatePrivateKey, privateKeyToAccount, mnemonicToAccount } from "viem/accounts";

import { runCompose } from "../../../.github/scripts/compose.mjs";
import { connect, contracts, stringify } from "../client.mjs";
import { digest, root } from "./artifacts.mjs";
import { acquireLock } from "./state.mjs";

export const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
export function retryDelay(value, now = Date.now()) {
  const seconds = Number(value);
  const duration = value && Number.isFinite(seconds) ? seconds * 1000 : Date.parse(value) - now;
  return Math.min(30000, Math.max(1000, Number.isFinite(duration) ? duration : 1000));
}
export function endpoint(value) {
  const u = new URL(value);
  if (
    u.username ||
    u.password ||
    u.search ||
    u.hash ||
    u.pathname !== "/" ||
    !["http:", "https:"].includes(u.protocol)
  )
    throw Error("Expected a local endpoint origin");
  if (
    !["localhost", "127.0.0.1", "[::1]"].includes(u.hostname) &&
    !u.hostname.endsWith(".etherview.localhost")
  )
    throw Error("Only local Preview endpoints are supported");
  return u.origin;
}
export async function request(origin, route, body, token) {
  const response = await fetch(origin + route, {
    method: body ? "POST" : "GET",
    redirect: "error",
    headers: {
      "Content-Type":
        typeof body === "string" ? "application/x-www-form-urlencoded" : "application/json",
      ...(token ? { "X-API-Key": token } : {}),
    },
    ...(body ? { body: typeof body === "string" ? body : stringify(body) } : {}),
    signal: AbortSignal.timeout(15000),
  });
  const chunks = [];
  let length = 0;
  for await (const chunk of response.body) {
    length += chunk.length;
    if (length > 8 * 1024 * 1024) throw Error("Preview response exceeds limit");
    chunks.push(chunk);
  }
  const bytes = Buffer.concat(chunks);
  // Return the original response, without replaying writes. Callers may retry
  // admitted read/verification operations after the server's bounded backoff.
  if (response.status === 429) await sleep(retryDelay(response.headers.get("Retry-After")));
  if (route.endsWith("/content")) return { status: response.status, bytes };
  let value;
  try {
    value = JSON.parse(bytes);
  } catch {
    throw Error("Invalid Preview JSON response");
  }
  return { status: response.status, body: value };
}
export async function preflight(endpoints) {
  for (const value of Object.values(endpoints)) endpoint(value);
  const ctx = await connect(endpoints);
  const config = await request(endpoints.app, "/api/v1/config");
  if (config.status !== 200 || config.body.data?.chain_id !== "48815")
    throw Error("Preview API chain identity mismatch");
  const genesis = await request(endpoints.app, "/api/v1/genesis/accounts?limit=100");
  if (
    genesis.status !== 200 ||
    genesis.body.meta?.chain_id !== "48815" ||
    !genesis.body.data?.length ||
    genesis.body.data.some((a) => a.block_hash !== ctx.genesis)
  )
    throw Error("Preview API genesis identity mismatch or genesis import pending");
  return { ctx, features: config.body.data.features };
}
export function assertNetwork(state, genesis) {
  if ((state.chainId && state.chainId !== "48815") || (state.genesis && state.genesis !== genesis))
    throw Error("Run belongs to another Preview network");
}
export function actor(run, name) {
  const file = path.join(run.dir, "private", name + ".key");
  if (!fs.existsSync(file))
    fs.writeFileSync(file, generatePrivateKey(), { flag: "wx", mode: 0o600 });
  return privateKeyToAccount(fs.readFileSync(file, "utf8").trim());
}
export function existingActor(run, name) {
  return privateKeyToAccount(
    fs.readFileSync(path.join(run.dir, "private", name + ".key"), "utf8").trim(),
  );
}
export function rpcCode(error) {
  for (let n = 0; error && n < 12; n++, error = error.cause)
    if (Number.isInteger(error.code)) return error.code;
}
export function isUncertain(step) {
  return ["prepared", "submitted"].includes(step?.status);
}
export function receiptView(r) {
  return {
    transactionHash: r.transactionHash,
    blockHash: r.blockHash,
    blockNumber: String(r.blockNumber),
    status: r.status,
    contractAddress: r.contractAddress ?? undefined,
  };
}
export class Engine {
  apiKeyAdmin(args) {
    return JSON.parse(
      runCompose([
        "exec",
        "-T",
        "api",
        "/etherview",
        "admin",
        "api-key",
        ...args,
        "--config=/etc/etherview/config.yaml",
      ]),
    );
  }

  constructor(run, options, artifacts) {
    this.run = run;
    this.options = options;
    this.artifacts = artifacts;
    this.timeout = Number(options.timeout ?? 180) * 1000;
  }
  async init(readOnly = false) {
    const { ctx, features } = await preflight(this.run.state.endpoints);
    assertNetwork(this.run.state, ctx.genesis);
    this.features = features;
    this.rpc = ctx.rpc;
    this.bundler = ctx.bundler;
    this.chain = ctx.wallet.chain;
    this.account = readOnly ? { address: this.run.state.owner } : actor(this.run, "owner");
    if (this.run.state.owner && this.run.state.owner !== this.account.address)
      throw Error("Run owner mismatch");
    this.run.state.owner = this.account.address;
    this.run.state.chainId = "48815";
    this.run.state.genesis = ctx.genesis;
    this.run.save();
  }
  wallet(account = this.account) {
    return createWalletClient({
      account,
      chain: this.chain,
      transport: http(this.run.state.endpoints.rpc, { retryCount: 0 }),
    });
  }
  lockSender(address) {
    return acquireLock(
      path.join(root, ".local/preview-tools"),
      "sender:" + this.run.state.genesis + ":" + address.toLowerCase(),
    );
  }
  unlockSender(unlock, step) {
    // A shared payer must stay fenced across runs after an ambiguous broadcast.
    // check can reconcile it without acquiring this writer lock.
    if (!isUncertain(step)) unlock();
  }
  async fund() {
    const developer = mnemonicToAccount(
      "test test test test test test test test test test test junk",
    );
    const unlock = this.lockSender(developer.address);
    try {
      await this.tx(
        this.run.state.funding,
        "owner",
        { to: this.account.address, value: parseEther("40") },
        { account: developer },
      );
    } finally {
      this.unlockSender(unlock, this.run.state.funding.steps.owner);
    }
  }
  async wait(read, predicate, label, timeout = this.timeout) {
    const end = Date.now() + timeout;
    do {
      const value = await read();
      if (predicate(value)) return value;
      await sleep(250);
    } while (Date.now() < end);
    throw Error("Timed out: " + label + "; check the saved run before retrying");
  }
  async assertKnownOperations() {
    for (const s of [this.run.state.funding, ...Object.values(this.run.state.scenarios)])
      for (const step of Object.values(s.steps))
        if (isUncertain(step) && !(await this.reconcile(step)))
          throw Error("Prior operation outcome is unknown: " + step.hash + "; use check/resume");
  }
  async reconcile(step) {
    if (step.kind === "userop") {
      if (step.status === "rejected") return step;
      const r = await this.bundler.request({
        method: "eth_getUserOperationReceipt",
        params: [step.hash],
      });
      if (!r) return null;
      if (step.expectedRejection?.length)
        throw Error("Expected admission rejection but UserOperation was included");
      if (r.userOpHash.toLowerCase() !== step.hash.toLowerCase())
        throw Error("UserOperation receipt identity mismatch");
      const status = r.success ? "success" : "reverted";
      if (status !== step.expected) throw Error("Unexpected UserOperation outcome");
      const receipt = await this.rpc.getTransactionReceipt({ hash: r.receipt.transactionHash });
      if (receipt.blockHash.toLowerCase() !== r.receipt.blockHash.toLowerCase())
        throw Error("UserOperation receipt block mismatch");
      const block = await this.rpc.getBlock({ blockNumber: receipt.blockNumber });
      if (block.hash !== receipt.blockHash)
        throw Error("UserOperation receipt is no longer canonical");
      step.receipt = receiptView(receipt);
      step.outcome = status;
      step.status = "mined";
      this.run.save();
      return step;
    }
    let r;
    try {
      r = await this.rpc.getTransactionReceipt({ hash: step.hash });
    } catch (e) {
      if (
        e.name === "TransactionReceiptNotFoundError" ||
        (rpcCode(e) === -32000 && /indexing is in progress/i.test(e.details ?? e.message))
      )
        return null;
      throw e;
    }
    if (r.transactionHash.toLowerCase() !== step.hash.toLowerCase())
      throw Error("Transaction receipt identity mismatch");
    const block = await this.rpc.getBlock({ blockNumber: r.blockNumber });
    if (block.hash !== r.blockHash) throw Error("Transaction receipt is no longer canonical");
    step.receipt = receiptView(r);
    step.status = r.status === step.expected ? "mined" : "unexpected_outcome";
    this.run.save();
    if (r.status !== step.expected) throw Error("Unexpected transaction outcome: " + r.status);
    return step;
  }
  async tx(s, id, input, { account = this.account, expected = "success", prepared } = {}) {
    s.activeStep = id;
    this.run.save();
    let step = s.steps[id];
    if (!step) {
      await this.assertKnownOperations();
      let raw, hash, blobs;
      if (prepared) ({ raw, hash, blobs } = await prepared());
      else {
        const wallet = this.wallet(account);
        const p = await wallet.prepareTransactionRequest({
          ...input,
          account,
          ...(expected === "reverted" && !input.gas ? { gas: 6000000n } : {}),
        });
        raw = await wallet.signTransaction(p);
        hash = keccak256(raw);
      }
      step = s.steps[id] = {
        kind: "transaction",
        hash,
        raw,
        blobs,
        expected,
        status: "prepared",
        to: blobs ? this.account.address : input.to,
        valueWei: String(input.value ?? 0n),
        transactionType: blobs ? "eip4844" : (input.type ?? "eip1559"),
      };
      this.run.save();
      // Write-ahead journal: every subsequent invocation only reconciles this hash.
      step.status = "submitted";
      this.run.save();
      const actual = await this.rpc.sendRawTransaction({ serializedTransaction: raw });
      if (actual.toLowerCase() !== hash.toLowerCase())
        throw Error("RPC returned inconsistent transaction hash");
    }
    await this.wait(() => this.reconcile(step), Boolean, id + " inclusion");
    return step.receipt;
  }
  async call(s, id, address, artifact, fn, args = [], options = {}) {
    return this.tx(
      s,
      id,
      {
        to: address,
        data: encodeFunctionData({ abi: this.artifacts[artifact].abi, functionName: fn, args }),
        ...(options.value ? { value: options.value } : {}),
      },
      options,
    );
  }
  async deploy(s, id, artifact, args = []) {
    const a = this.artifacts[artifact];
    if (s.deployments?.[id] && s.deployments[id].identity !== a.identity)
      throw Error("Fixture identity changed; start a new run");
    const encoded = encodeDeployData({ abi: a.abi, bytecode: a.bytecode, args });
    const r = await this.tx(s, id, { data: encoded });
    if (!r.contractAddress) throw Error("Missing creation address");
    s.entities[id] = r.contractAddress;
    s.deployments ??= {};
    s.deployments[id] = {
      ...s.deployments[id],
      address: r.contractAddress,
      artifact,
      identity: a.identity,
      constructorArguments: "0x" + encoded.slice(a.bytecode.length),
    };
    this.exportInput(s, id);
    this.run.save();
    return r.contractAddress;
  }
  exportInput(s, id) {
    const d = s.deployments[id];
    const a = this.artifacts[d.artifact];
    const directory = path.join(this.run.dir, "verification");
    fs.mkdirSync(directory, { recursive: true });
    if (a.input) {
      fs.writeFileSync(
        path.join(directory, s.name + "-" + id + "-input.json"),
        stringify(a.input),
        { mode: 0o600 },
      );
      fs.writeFileSync(
        path.join(directory, s.name + "-" + id + ".json"),
        stringify({
          language: "solidity",
          compiler_version: a.compiler,
          input_kind: "standard_json",
          contract_name_hint: a.name,
          input: a.input,
        }),
        { mode: 0o600 },
      );
    }
    fs.writeFileSync(
      path.join(directory, s.name + "-" + id + "-artifact.json"),
      stringify({
        address: d.address,
        identity: a.identity,
        compiler: a.compiler,
        upstream: a.upstream,
        contract: a.name,
        constructor_arguments: d.constructorArguments,
      }),
      { mode: 0o600 },
    );
  }
  read(address, artifact, fn, args = []) {
    return this.rpc.readContract({
      address,
      abi: this.artifacts[artifact].abi,
      functionName: fn,
      args,
    });
  }
  async userop(s, id, prepare, expected = "success", rejectedCodes = []) {
    s.activeStep = id;
    this.run.save();
    let step = s.steps[id];
    if (!step) {
      await this.assertKnownOperations();
      const { hash, operation } = await prepare();
      step = s.steps[id] = {
        kind: "userop",
        hash,
        operation,
        expected,
        expectedRejection: rejectedCodes,
        status: "prepared",
      };
      this.run.save();
      step.status = "submitted";
      this.run.save();
      let result;
      try {
        result = await this.bundler.request({
          method: "eth_sendUserOperation",
          params: [operation, contracts.EntryPoint.address],
        });
      } catch (e) {
        const code = rpcCode(e);
        if (!rejectedCodes.includes(code)) throw e;
        step.status = "rejected";
        step.rejectionCode = code;
        this.run.save();
        return step;
      }
      if (rejectedCodes.length)
        throw Error("Expected rejection but Bundler accepted the operation");
      if (result.toLowerCase() !== hash.toLowerCase()) throw Error("Bundler hash mismatch");
    }
    if (rejectedCodes.length && step.status !== "rejected")
      throw Error("Uncertain rejected operation; inspect saved hash");
    if (step.status !== "rejected")
      await this.wait(() => this.reconcile(step), Boolean, id + " UserOperation inclusion");
    return step;
  }
  check(s, label, route, expected = {}) {
    const existing = s.checks.find((c) => c.label === label);
    const value = { label, route, expected, status: "pending" };
    if (existing) Object.assign(existing, value);
    else s.checks.push(value);
    this.run.save();
  }
  compose(args, input) {
    const files = (this.options["compose-files"] ?? "compose.yaml").split(",");
    return runCompose(
      [
        ...files.flatMap((f) => ["-f", f]),
        ...(this.options.project ? ["-p", this.options.project] : []),
        ...args,
      ],
      { timeout: 30000, input },
    );
  }
  async verify(s, ids, bindings = []) {
    if (this.options["no-verify"]) {
      s.verification = "manual";
      return;
    }
    const targets = ids.filter((id) => this.artifacts[s.deployments[id]?.artifact]?.input);
    if (!targets.length && !bindings.length) return;
    const admin = (args) =>
      JSON.parse(
        this.compose([
          "exec",
          "-T",
          "api",
          "/etherview",
          "admin",
          "api-key",
          ...args,
          "--config=/etc/etherview/config.yaml",
        ]),
      );
    for (const prefix of this.run.state.verificationKeys ?? []) admin(["revoke", prefix]);
    this.run.state.verificationKeys = [];
    this.run.save();
    const key = admin([
      "create",
      "--name",
      "preview-tool-" + this.run.state.id,
      "--scope",
      "contract:verify",
      "--scope",
      "api:read",
    ]);
    this.run.state.verificationKeys = [key.prefix];
    this.run.save();
    const onSignal = () => {
      try {
        admin(["revoke", key.prefix]);
        this.run.state.verificationKeys = [];
        this.run.save();
      } finally {
        process.exitCode = 130;
        process.exit(130);
      }
    };
    process.once("SIGINT", onSignal);
    process.once("SIGTERM", onSignal);
    try {
      for (const id of targets) {
        s.activeStep = "verify:" + id;
        this.run.save();
        const d = s.deployments[id];
        this.exportInput(s, id);
        const route = "/api/v1/contracts/" + d.address + "/verification";
        const a = this.artifacts[d.artifact];
        const published = (v) =>
          v?.data?.kind === "verification_success" &&
          v.data.resolution === "exact_address" &&
          v.data.target?.address.toLowerCase() === d.address.toLowerCase();
        const prior = await request(this.run.state.endpoints.app, route);
        // Factory-derived exact source can omit the AST needed for CWIA decoding.
        // Submit this implementation's standard JSON directly at least once.
        const needsCWIAAnalysis = s.name === "proxy.cwia" && id === "implementation" && !d.job;
        if (prior.status === 200 && published(prior.body) && !needsCWIAAnalysis) continue;
        if (!d.job) {
          const response = await this.wait(
            () =>
              request(
                this.run.state.endpoints.app,
                route,
                {
                  language: "solidity",
                  compiler_version: a.compiler,
                  input_kind: "standard_json",
                  contract_name_hint: a.name,
                  input: a.input,
                },
                key.token,
              ),
            (r) => {
              if ([429, 503].includes(r.status)) return false;
              if (r.status !== 202) throw Error("Verification submission HTTP " + r.status);
              return true;
            },
            "verification admission",
          );
          d.job = response.body.data.id;
          this.run.save();
        }
        await this.wait(
          async () => {
            const response = await request(
              this.run.state.endpoints.app,
              "/api/v1/verifier/jobs/" + d.job,
              undefined,
              key.token,
            );
            if (response.status !== 200) throw Error("Verification status HTTP " + response.status);
            if (["failed", "cancelled"].includes(response.body.data.status))
              throw Error("Verification failed: " + (response.body.data.error_code ?? "unknown"));
            return response.body.data.status;
          },
          (status) => status === "succeeded",
          "verification job " + d.job,
        );
        const result = await request(this.run.state.endpoints.app, route);
        if (!published(result.body)) throw Error("Missing exact-address verification publication");
      }
      for (const [address, implementation] of bindings) {
        const bindingDeadline = Date.now() + this.timeout;
        const waitBinding = (read, predicate, label) => {
          const remaining = bindingDeadline - Date.now();
          if (remaining <= 0)
            throw Error("Timed out: " + label + "; check the saved run before retrying");
          return this.wait(read, predicate, label, remaining);
        };
        for (let attempt = 0; attempt < 30; attempt++) {
          s.activeStep = "bind-proxy";
          this.run.save();
          const route = "/api/v1/contracts/" + address + "/proxy";
          const current = await waitBinding(
            async () => {
              const r = await request(this.run.state.endpoints.app, route);
              return r.status === 200 &&
                r.body.data?.implementation?.address?.toLowerCase() === implementation.toLowerCase()
                ? r.body.data
                : null;
            },
            Boolean,
            "current proxy publication",
          );
          if (current.binding_id) break;
          s.bindingJobs ??= {};
          const identity = address.toLowerCase() + ":" + implementation.toLowerCase();
          if (!s.bindingJobs[identity]) {
            const form = new URLSearchParams({
              chainid: "48815",
              module: "contract",
              action: "verifyproxycontract",
              address,
              expectedimplementation: implementation,
            }).toString();
            const admitted = await waitBinding(
              async () => {
                const r = await request(this.run.state.endpoints.app, "/v2/api", form, key.token);
                if ([429, 503].includes(r.status)) return null;
                if (r.status !== 200) throw Error("Proxy verification HTTP " + r.status);
                // Index/verification-derived identities may not be published together yet.
                if (r.body.status !== "1") {
                  if (
                    /unavailable|not verified|not found|not detected|not ready/i.test(
                      String(r.body.result),
                    )
                  )
                    return null;
                  throw Error("Proxy verification rejected");
                }
                return r.body.result;
              },
              Boolean,
              "proxy verification admission",
            );
            s.bindingJobs[identity] = admitted;
            this.run.save();
          }
          const outcome = await waitBinding(
            async () => {
              const r = await request(
                this.run.state.endpoints.app,
                "/api/v1/verifier/jobs/" + s.bindingJobs[identity],
                undefined,
                key.token,
              );
              if (r.status !== 200) throw Error("Proxy job HTTP " + r.status);
              if (
                r.body.data.status === "failed" &&
                r.body.data.error_code === "target_not_canonical"
              )
                return "stale";
              if (["failed", "cancelled"].includes(r.body.data.status))
                throw Error(
                  "Proxy binding verification failed: " + (r.body.data.error_code ?? "unknown"),
                );
              return r.body.data.status;
            },
            (status) => status === "succeeded" || status === "stale",
            "proxy binding job",
          );
          if (outcome === "stale") {
            s.staleBindingJobs ??= [];
            s.staleBindingJobs.push(s.bindingJobs[identity]);
            delete s.bindingJobs[identity];
            this.run.save();
            if (attempt === 29 || Date.now() >= bindingDeadline)
              throw Error(
                "Proxy publication changed during verification; resume after indexing settles",
              );
            // Avoid phase-locking retries to Preview's three-second slots.
            await sleep(350 + (attempt % 5) * 275);
            continue;
          }
          await waitBinding(
            async () => (await request(this.run.state.endpoints.app, route)).body.data?.binding_id,
            Boolean,
            "proxy binding publication",
          );
          break;
        }
      }
    } finally {
      process.removeListener("SIGINT", onSignal);
      process.removeListener("SIGTERM", onSignal);
      admin(["revoke", key.prefix]);
      this.run.state.verificationKeys = [];
      this.run.save();
    }
  }
  async prepareBlob(count) {
    const key = fs
      .readFileSync(path.join(this.run.dir, "private", "owner.key"), "utf8")
      .trim()
      .slice(2);
    const fees = await this.rpc.estimateFeesPerGas();
    const blobBaseFee = BigInt(await this.rpc.request({ method: "eth_blobBaseFee" }));
    const helper = this.options["blob-helper"];
    const marker = Number(BigInt("0x" + digest(this.run.state.id).slice(0, 2)));
    const result = execFileSync(
      helper ?? "go",
      helper ? [] : ["run", "./preview/cmd/previewblob"],
      {
        cwd: root,
        input: stringify({
          chainId: "48815",
          key,
          to: this.account.address,
          nonce: await this.rpc.getTransactionCount({
            address: this.account.address,
            blockTag: "pending",
          }),
          fee: fees.maxFeePerGas,
          tip: fees.maxPriorityFeePerGas > 0n ? fees.maxPriorityFeePerGas : 1n,
          blobFee: blobBaseFee * 2n + 1000000000n,
          count,
          marker,
        }),
        encoding: "utf8",
        maxBuffer: 2 * 1024 * 1024,
        timeout: 180000,
        stdio: ["pipe", "pipe", "pipe"],
      },
    );
    const signed = JSON.parse(result);
    const directory = path.join(this.run.dir, "blobs");
    fs.mkdirSync(directory, { recursive: true });
    for (const [index, blob] of signed.blobs.entries()) {
      const original = Buffer.alloc(131072);
      for (let offset = 31; offset < original.length; offset += 32)
        original[offset] = marker + index;
      if (digest(original) !== blob.sha256) throw Error("Blob helper content identity mismatch");
      blob.file = "blobs/" + count + "-" + index + ".bin";
      fs.writeFileSync(path.join(this.run.dir, blob.file), original, { mode: 0o600 });
    }
    return signed;
  }
}
export function matches(body, expected) {
  return Object.entries(expected).every(([field, value]) => {
    const actual = field.split(".").reduce((o, k) => o?.[k], body);
    if (value && typeof value === "object" && "$contains" in value)
      return Array.isArray(actual) && actual.some((item) => matches(item, value.$contains));
    return typeof value === "string" && /^0x/i.test(value)
      ? String(actual).toLowerCase() === value.toLowerCase()
      : actual === value;
  });
}
export async function checkScenario(engine, s) {
  for (const [id, step] of Object.entries(s.steps)) {
    if (!step.hash || step.status === "rejected") continue;
    s.activeStep = "check:" + id;
    engine.run.save();
    const resolved = await engine.reconcile(step);
    if (!resolved) throw Error("Pending inclusion: " + id);
    const route = step.kind === "userop" ? "/api/v1/user-operations/" : "/api/v1/transactions/";
    engine.check(s, id + " indexed", route + step.hash, {
      ...(step.kind === "userop"
        ? { "data.hash": step.hash, "data.success": step.expected === "success" }
        : {
            "data.kind": "included",
            "data.transaction.hash": step.hash,
            "data.transaction.status": step.expected === "reverted" ? "failed" : "success",
          }),
    });
    if (step.blobs) {
      const key = engine.apiKeyAdmin([
        "create",
        "--name=preview-blob-readback",
        "--scope=api:read",
      ]);
      try {
        const base = "/api/v1/blocks/" + step.receipt.blockHash;
        const downloaded = new Set();
        engine.check(s, id + " consensus", base + "/consensus", { "data.state": "complete" });
        engine.check(s, id + " requests", base + "/requests", { "data.state": "complete" });
        await engine.wait(
          async () => {
            const result = await request(engine.run.state.endpoints.app, base + "/blobs");
            if (result.status !== 200 || result.body.data?.state !== "complete") return false;
            for (const wanted of step.blobs) {
              const item = result.body.data.items.find(
                (v) =>
                  v.versioned_hash === wanted.versionedHash && v.transaction_hash === step.hash,
              );
              if (
                !item ||
                item.commitment !== wanted.commitment ||
                item.sha256.replace(/^0x/, "") !== wanted.sha256
              )
                return false;
              if (downloaded.has(wanted.versionedHash)) continue;
              const download = await request(
                engine.run.state.endpoints.app,
                "/v2/api?" +
                  new URLSearchParams({
                    chainid: "48815",
                    module: "blob",
                    action: "getblob",
                    blockhash: step.receipt.blockHash,
                    index: item.index,
                  }),
                undefined,
                key.token,
              );
              if ([404, 409, 429, 503].includes(download.status) || download.body?.status === "0")
                return false;
              if (
                download.body?.status !== "1" ||
                !/^0x[0-9a-f]{262144}$/.test(download.body.result)
              )
                throw Error("Invalid blob content response");
              download.bytes = Buffer.from(download.body.result.slice(2), "hex");
              if (
                wanted.file &&
                !download.bytes?.equals(fs.readFileSync(path.join(engine.run.dir, wanted.file)))
              )
                throw Error("Blob download differs from saved original");
              if (
                download.status !== 200 ||
                download.bytes.length !== 131072 ||
                digest(download.bytes) !== wanted.sha256
              )
                throw Error("Blob archive readback mismatch");
              downloaded.add(wanted.versionedHash);
            }
            return true;
          },
          Boolean,
          "blob archive readback",
        );
      } finally {
        engine.apiKeyAdmin(["revoke", key.prefix]);
      }
    }
  }
  for (const c of s.checks) {
    c.status = "pending";
    s.activeStep = "check:" + c.label;
    engine.run.save();
    await engine.wait(
      async () => {
        const result = await request(engine.run.state.endpoints.app, c.route);
        if ([404, 409, 429, 503].includes(result.status)) return false;
        if (result.status !== 200) throw Error("API check HTTP " + result.status);
        return matches(result.body, c.expected);
      },
      Boolean,
      c.label,
    );
    c.status = "passed";
    engine.run.save();
  }
  if (!s.setupComplete || s.actions.some((a) => a.status !== "complete"))
    throw Error("Scenario has unfinished steps; use resume after checking the saved operations");
  s.status = "api_published";
  delete s.activeStep;
  delete s.error;
  engine.run.save();
}
