import vm from "node:vm";
import test from "node:test";
import assert from "node:assert/strict";

import { build } from "vite";
import { fileURLToPath } from "node:url";

const [{ output }] = await build({
  configFile: false,
  logLevel: "silent",
  build: {
    write: false,
    lib: {
      entry: fileURLToPath(new URL("../src/workers/push-sw.ts", import.meta.url)),
      formats: ["es"],
    },
  },
});
const { code } = output[0];
function worker() {
  const handlers = new Map();
  const shown = [],
    opened = [];
  const self = {
    addEventListener: (name, handler) => handlers.set(name, handler),
    location: { origin: "https://explorer.example" },
    registration: {
      showNotification: async (...args) => {
        shown.push(args);
      },
    },
    clients: {
      matchAll: async () => [],
      openWindow: async (url) => opened.push(url),
      claim: async () => {},
    },
    skipWaiting: async () => {},
  };
  vm.runInNewContext(code, { self, URL, Date });
  return { handlers, shown, opened };
}
test("push worker displays bounded summaries without fetch/cache or history", async () => {
  const { handlers, shown } = worker();
  assert.equal(handlers.has("fetch"), false);
  let pending;
  const data = {
    title: "Watchlist activity",
    body: "Example · erc20 · in",
    path: `/tx/0x${"a".repeat(64)}`,
    tag: "etherview:one",
    expires: Date.now() + 10000,
  };
  handlers.get("push")({ data: { json: () => data }, waitUntil: (p) => (pending = p) });
  await pending;
  assert.equal(shown.length, 1);
  assert.equal(shown[0][1].renotify, false);
  for (const change of [
    { path: "https://evil.example" },
    { path: "/account" },
    { expires: 0 },
    { body: "x".repeat(201) },
    { tag: "other" },
    { title: "x".repeat(101) },
    { tag: `etherview:${"x".repeat(300)}` },
    { expires: "9999999999999" },
    { expires: Number.MAX_SAFE_INTEGER + 1 },
  ]) {
    handlers.get("push")({
      data: { json: () => ({ ...data, ...change }) },
      waitUntil: () => assert.fail("unsafe push displayed"),
    });
  }
  for (const json of [
    () => null,
    () => "invalid",
    () => ({}),
    () => {
      throw new SyntaxError("malformed JSON");
    },
  ]) {
    handlers.get("push")({
      data: { json },
      waitUntil: () => assert.fail("invalid push displayed"),
    });
  }
});
test("notification click closes alert and opens only a same-origin transaction", async () => {
  const { handlers, opened } = worker();
  let closed = 0,
    pending;
  const path = `/tx/0x${"a".repeat(64)}`;
  handlers.get("notificationclick")({
    notification: { close: () => closed++, data: { path } },
    waitUntil: (p) => (pending = p),
  });
  await pending;
  assert.equal(closed, 1);
  assert.deepEqual(opened, [`https://explorer.example${path}`]);
  handlers.get("notificationclick")({
    notification: { close: () => closed++, data: { path: "//evil.example" } },
    waitUntil: () => assert.fail("external navigation"),
  });
  assert.equal(opened.length, 1);
});
