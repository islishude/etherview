/* Push only: no fetch handler, application cache, or notification history. */
self.addEventListener("install", (event) => event.waitUntil(self.skipWaiting()));
self.addEventListener("activate", (event) => event.waitUntil(self.clients.claim()));
self.addEventListener("push", (event) => {
  let data;
  try {
    data = event.data?.json();
  } catch {
    return;
  }
  if (
    !data ||
    typeof data.title !== "string" ||
    data.title.length > 100 ||
    typeof data.body !== "string" ||
    data.body.length > 200 ||
    typeof data.tag !== "string" ||
    !data.tag.startsWith("etherview:") ||
    data.tag.length > 300 ||
    typeof data.path !== "string" ||
    !/^\/tx\/0x[0-9a-fA-F]{64}$/.test(data.path) ||
    !Number.isSafeInteger(data.expires) ||
    data.expires <= Date.now()
  )
    return;
  event.waitUntil(
    self.registration.showNotification(data.title, {
      body: data.body,
      tag: data.tag,
      renotify: false,
      data: { path: data.path },
    }),
  );
});
self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  const path = event.notification.data?.path;
  if (typeof path !== "string" || !/^\/tx\/0x[0-9a-fA-F]{64}$/.test(path)) return;
  event.waitUntil(
    (async () => {
      const url = new URL(path, self.location.origin).href;
      const windows = await self.clients.matchAll({ type: "window", includeUncontrolled: true });
      const existing = windows.find((client) => client.url === url);
      if (existing) await existing.focus();
      else await self.clients.openWindow(url);
    })(),
  );
});
