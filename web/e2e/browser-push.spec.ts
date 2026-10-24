import { expect, test } from "@playwright/test";

// Full Chromium supports native notifications in headless mode; the default
// bundled headless shell does not expose them through getNotifications().
test.use({ channel: process.env.PLAYWRIGHT_USE_BUNDLED === "1" ? "chromium" : "chrome" });

test("embedded push retains its transaction target after the page closes and worker stops", async ({
  page,
  context,
}) => {
  await page.goto("/");
  const origin = new URL(page.url()).origin;
  await context.grantPermissions(["notifications"], { origin });
  // Keep a blank DevTools control target, not an open explorer document. This
  // injects a browser push event; it is not browser-provider delivery evidence.
  const control = await context.newPage();
  const protocol = await context.newCDPSession(control);
  let registrationId = "";
  let versionId = "";
  let runningStatus = "";
  protocol.on("ServiceWorker.workerRegistrationUpdated", ({ registrations }) => {
    for (const registration of registrations) {
      if (registration.scopeURL === `${origin}/` && !registration.isDeleted)
        registrationId = registration.registrationId;
    }
  });
  protocol.on("ServiceWorker.workerVersionUpdated", ({ versions }) => {
    for (const version of versions) {
      if (version.registrationId === registrationId && version.status === "activated") {
        versionId = version.versionId;
        runningStatus = version.runningStatus;
      }
    }
  });
  await protocol.send("ServiceWorker.enable");
  await page.evaluate(async () => {
    await navigator.serviceWorker.register("/push-sw.js", {
      type: "module",
      scope: "/",
      updateViaCache: "none",
    });
    await navigator.serviceWorker.ready;
  });
  await expect.poll(() => registrationId).not.toBe("");
  await page.close();
  await protocol.send("ServiceWorker.deliverPushMessage", {
    origin,
    registrationId,
    data: JSON.stringify({
      title: "Watchlist activity",
      body: "Test address · transaction · in",
      path: `/tx/0x${"a".repeat(64)}`,
      tag: "etherview:closed-page-test",
      expires: Date.now() + 10_000,
    }),
  });
  await expect.poll(() => versionId).not.toBe("");
  await protocol.send("ServiceWorker.stopWorker", { versionId });
  await expect.poll(() => runningStatus).toBe("stopped");
  const reopened = await context.newPage();
  await reopened.goto(origin);
  await expect
    .poll(() =>
      reopened.evaluate(async () => {
        const registration = await navigator.serviceWorker.ready;
        return (await registration.getNotifications()).map((n) => ({
          title: n.title,
          body: n.body,
          tag: n.tag,
          path: n.data?.path,
        }));
      }),
    )
    .toContainEqual({
      title: "Watchlist activity",
      body: "Test address · transaction · in",
      tag: "etherview:closed-page-test",
      path: `/tx/0x${"a".repeat(64)}`,
    });
  await reopened.evaluate(async () => {
    const registration = await navigator.serviceWorker.ready;
    for (const notification of await registration.getNotifications()) notification.close();
    await registration.unregister();
  });
  await control.close();
});
