import assert from "node:assert/strict";
import fs from "node:fs";
import test from "node:test";

test("startup verifies after chain readiness and recreation skips verification", () => {
  const make = fs.readFileSync(new URL("../../Makefile", import.meta.url), "utf8");
  const startup = make.split("\nstart-preview:")[1].split(/\n\S/)[0];
  assert.ok(
    startup.indexOf("preview/scripts/verify.mjs") > startup.indexOf("preview/scripts/ready.mjs"),
  );
  const recreation = make.split("\nrecreate-preview:")[1].split(/\n\S/)[0];
  assert.doesNotMatch(recreation, /preview\/scripts\/verify|verify-preview-genesis|genesis-verify/);
  const service = fs
    .readFileSync(new URL("../compose/chain.yaml", import.meta.url), "utf8")
    .split("  genesis-verify:\n")[1]
    .split("\n  bundler:")[0];
  assert.match(service, /profiles: \["genesis-verify"\]/);
  assert.match(service, /read_only: true/);
  assert.doesNotMatch(service, /docker.sock|DATABASE_URL|PEPPER|ports:/);
});

test("temporary key is passed only through stdin and revoked on success or failure", async () => {
  const { verifyPreview } = await import("./verify.mjs");
  for (const fail of [false, true]) {
    const calls = [];
    const invoke = (args, options) => {
      calls.push(args);
      assert.ok(!args.includes("test-token"));
      if (args.includes("create"))
        return JSON.stringify({ token: "test-token", prefix: "test-prefix" });
      if (args.includes("run")) {
        assert.equal(options.input, "test-token");
        assert.equal(options.env, undefined);
        if (fail) throw Error("test failed");
      }
      return "{}";
    };
    if (fail) assert.throws(() => verifyPreview(invoke), /test failed/);
    else verifyPreview(invoke);
    assert.ok(calls.at(-1).includes("revoke"));
    assert.ok(calls.at(-1).includes("test-prefix"));
  }
});
