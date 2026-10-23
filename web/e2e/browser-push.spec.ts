import { expect, test } from "@playwright/test";

test("embedded worker displays an injected push after the website page closes", async ({
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
  protocol.on("ServiceWorker.workerRegistrationUpdated", ({ registrations }) => {
    for (const registration of registrations) {
      if (registration.scopeURL === `${origin}/` && !registration.isDeleted)
        registrationId = registration.registrationId;
    }
  });
  await protocol.send("ServiceWorker.enable");
  await page.evaluate(async () => {
    await navigator.serviceWorker.register("/push-sw.js", { scope: "/", updateViaCache: "none" });
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
        }));
      }),
    )
    .toContainEqual({
      title: "Watchlist activity",
      body: "Test address · transaction · in",
      tag: "etherview:closed-page-test",
    });
  await reopened.evaluate(async () => {
    const registration = await navigator.serviceWorker.ready;
    for (const notification of await registration.getNotifications()) notification.close();
    await registration.unregister();
  });
  await control.close();
});
