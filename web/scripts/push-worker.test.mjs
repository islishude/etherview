import { readFileSync } from "node:fs";
import vm from "node:vm";
import test from "node:test";
import assert from "node:assert/strict";

const code = readFileSync(new URL("../public/push-sw.js", import.meta.url), "utf8");
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
  ]) {
    handlers.get("push")({
      data: { json: () => ({ ...data, ...change }) },
      waitUntil: () => assert.fail("unsafe push displayed"),
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
