import assert from "node:assert/strict";
import fs from "node:fs";
import test from "node:test";

import { recoverMessageAddress } from "viem";

import { adminAddress, initializeAdmin } from "./admin.mjs";

const message = `https://app.etherview.localhost wants you to sign in with your Ethereum account:\n${adminAddress}\n\nURI: https://app.etherview.localhost\nVersion: 1\nChain ID: 48815\nNonce: testnonce`;

test("SIWE precedes promotion and helper session is revoked even if promotion fails", async () => {
  for (const fail of [false, true]) {
    const calls = [];
    const request = async (route, body, headers) => {
      calls.push(route);
      if (route === "/auth/challenge") {
        assert.equal(body.address, adminAddress);
        return { data: { message, challenge_id: "challenge" } };
      }
      if (route === "/auth/verify") {
        assert.equal(body.challenge_id, "challenge");
        assert.equal(
          await recoverMessageAddress({ message, signature: body.signature }),
          adminAddress,
        );
        return {
          data: { authenticated: true, user: { address: adminAddress }, csrf_token: "csrf" },
          cookie: "etherview_session=temporary",
        };
      }
      assert.equal(route, "/auth/logout");
      assert.deepEqual(headers, { Cookie: "etherview_session=temporary", "X-CSRF-Token": "csrf" });
      return {};
    };
    const compose = (args) => {
      calls.push("promote");
      assert.deepEqual(args.slice(-4), ["--address", adminAddress, "--role", "admin"]);
      assert.ok(args.includes("set-role"));
      if (fail) throw new Error("promotion failed");
      return JSON.stringify({ user: { address: adminAddress, role: "admin" } });
    };
    if (fail) await assert.rejects(initializeAdmin(request, compose), /promotion failed/);
    else await initializeAdmin(request, compose);
    assert.deepEqual(calls, ["/auth/challenge", "/auth/verify", "promote", "/auth/logout"]);
  }
});

test("wrong chain/origin and rejected login never promote a user", async () => {
  for (const altered of [
    message.replace("48815", "1"),
    message.replaceAll("app.etherview.localhost", "example.com"),
  ]) {
    await assert.rejects(
      initializeAdmin(
        async () => ({ data: { message: altered } }),
        () => assert.fail("promoted"),
      ),
      /Unexpected Preview login challenge/,
    );
  }
  await assert.rejects(
    initializeAdmin(
      async (route) => {
        if (route === "/auth/challenge") return { data: { message } };
        throw new Error("login rejected");
      },
      () => assert.fail("promoted"),
    ),
    /login rejected/,
  );
});

test("both Preview lifecycle targets initialize administrator after readiness", () => {
  const make = fs.readFileSync(new URL("../../Makefile", import.meta.url), "utf8");
  for (const target of ["start-preview", "recreate-preview"]) {
    const recipe = make.split(`\n${target}:`)[1].split(/\n\S/)[0];
    assert.ok(
      recipe.indexOf("preview/contracts/admin.mjs") > recipe.indexOf("preview/scripts/ready.mjs"),
    );
  }
});
