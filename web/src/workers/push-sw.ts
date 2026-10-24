export {};

declare const self: ServiceWorkerGlobalScope;

/* Push only: no fetch handler, application cache, or notification history. */
self.addEventListener("install", (event) => event.waitUntil(self.skipWaiting()));
self.addEventListener("activate", (event) => event.waitUntil(self.clients.claim()));
self.addEventListener("push", (event) => {
  let data: unknown;
  try {
    data = event.data?.json();
  } catch {
    return;
  }
  if (
    !data ||
    typeof data !== "object" ||
    !("title" in data) ||
    typeof data.title !== "string" ||
    data.title.length > 100 ||
    !("body" in data) ||
    typeof data.body !== "string" ||
    data.body.length > 200 ||
    !("tag" in data) ||
    typeof data.tag !== "string" ||
    !data.tag.startsWith("etherview:") ||
    data.tag.length > 300 ||
    !("path" in data) ||
    typeof data.path !== "string" ||
    !/^\/tx\/0x[0-9a-fA-F]{64}$/.test(data.path) ||
    !("expires" in data) ||
    typeof data.expires !== "number" ||
    !Number.isSafeInteger(data.expires) ||
    data.expires <= Date.now()
  )
    return;
  // TypeScript's WebWorker library omits this supported notification option.
  const options: NotificationOptions & { renotify: boolean } = {
    body: data.body,
    tag: data.tag,
    renotify: false,
    data: { path: data.path },
  };
  event.waitUntil(self.registration.showNotification(data.title, options));
});
self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  const path = event.notification.data?.path;
  if (typeof path !== "string" || !/^\/tx\/0x[0-9a-fA-F]{64}$/.test(path)) return;
  event.waitUntil(
    (async () => {
      const url = new URL(path, self.location.origin).href;
      const windows = await self.clients.matchAll({ type: "window", includeUncontrolled: true });
      const sameOrigin = windows.filter(
        (client) => new URL(client.url).origin === self.location.origin,
      );
      // Preserve the selected transaction tab, including its query and fragment.
      const existing = sameOrigin.find((client) => new URL(client.url).pathname === path);
      if (existing) {
        try {
          await existing.focus();
          return;
        } catch {
          // The tab may have closed since matchAll().
        }
      }
      const reusable = sameOrigin.find((client) => client !== existing);
      if (reusable) {
        try {
          const navigated = await reusable.navigate(url);
          if (navigated) {
            await navigated.focus();
            return;
          }
        } catch {
          // A closed or no-longer-navigable tab must not swallow the click.
        }
      }
      await self.clients.openWindow(url);
    })(),
  );
});
