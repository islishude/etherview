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
function worker(windows = []) {
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
      matchAll: async (options) => {
        assert.equal(options.type, "window");
        assert.equal(options.includeUncontrolled, true);
        return windows;
      },
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
  assert.equal(shown[0][1].data.path, data.path);
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
  for (const invalid of [
    "//evil.example",
    `https://evil.example${path}`,
    `https://explorer.example${path}`,
    "/account",
    `${path}?redirect=https://evil.example`,
    `${path}#fragment`,
    "/tx/0x1234",
    null,
    123,
  ]) {
    handlers.get("notificationclick")({
      notification: { close: () => closed++, data: { path: invalid } },
      waitUntil: () => assert.fail("unsafe navigation"),
    });
  }
  assert.equal(opened.length, 1);
});

const transactionPath = `/tx/0x${"a".repeat(64)}`;
const transactionURL = `https://explorer.example${transactionPath}`;

async function click(windows) {
  const { handlers, opened } = worker(windows);
  let pending;
  handlers.get("notificationclick")({
    notification: { close() {}, data: { path: transactionPath } },
    waitUntil: (promise) => (pending = promise),
  });
  await pending;
  return opened;
}

test("notification click prioritizes the transaction tab and preserves its selected view", async () => {
  for (const suffix of ["", "?tab=logs#event-1"]) {
    let focused = 0;
    const opened = await click([
      {
        url: "https://explorer.example/account?tab=watchlist",
        navigate: () => assert.fail("unrelated tab navigated"),
      },
      {
        url: transactionURL + suffix,
        focus: async () => focused++,
        navigate: () => assert.fail("transaction tab reloaded"),
      },
    ]);
    assert.equal(focused, 1);
    assert.deepEqual(opened, []);
  }
});

test("notification click navigates an existing explorer tab and focuses the returned client", async () => {
  let focused = 0;
  const navigated = [];
  const opened = await click([
    {
      url: "https://other.example/account",
      navigate: () => assert.fail("external tab navigated"),
    },
    {
      url: "https://explorer.example/account?tab=watchlist",
      navigate: async (url) => {
        navigated.push(url);
        return { focus: async () => focused++ };
      },
      focus: () => assert.fail("stale client focused"),
    },
  ]);
  assert.deepEqual(navigated, [transactionURL]);
  assert.equal(focused, 1);
  assert.deepEqual(opened, []);
});

test("notification click opens the transaction when existing windows cannot be reused", async () => {
  const unavailable = async () => {
    throw new Error("Window closed");
  };
  for (const windows of [
    [{ url: `https://other.example${transactionPath}`, focus: unavailable }],
    [{ url: transactionURL, focus: unavailable }],
    [{ url: "https://explorer.example/", navigate: unavailable }],
    [{ url: "https://explorer.example/", navigate: async () => null }],
    [{ url: "https://explorer.example/", navigate: async () => ({ focus: unavailable }) }],
  ]) {
    assert.deepEqual(await click(windows), [transactionURL]);
  }
});

test("notification click can reuse another explorer tab after the transaction tab closes", async () => {
  let focused = 0;
  const opened = await click([
    {
      url: transactionURL,
      focus: async () => {
        throw new Error("Window closed");
      },
    },
    {
      url: "https://explorer.example/",
      navigate: async (url) => {
        assert.equal(url, transactionURL);
        return { focus: async () => focused++ };
      },
    },
  ]);
  assert.equal(focused, 1);
  assert.deepEqual(opened, []);
});
