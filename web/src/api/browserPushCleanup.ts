import { getAuthSession } from "./auth";
import { listPushSubscriptions } from "./watchlist";
import { endpointHash, withPushLock } from "./browserPush";

import pushWorkerURL from "@/workers/push-sw.ts?worker&url";

export async function clearBrowserPush(expectedCSRF: string) {
  if (!("serviceWorker" in navigator)) return;
  await withPushLock(async () => {
    const registration = await navigator.serviceWorker.getRegistration("/");
    if (registration?.active?.scriptURL !== new URL(pushWorkerURL, location.href).href) return;
    const subscription = await registration.pushManager.getSubscription();
    // Cookies are shared across tabs, but each tab's session/expiry state is not.
    // Check ownership inside the same lock as enrollment, before touching a
    // subscription that another tab may already have rebound to a new session.
    const session = await getAuthSession();
    if (subscription && session.authenticated && session.csrf_token !== expectedCSRF) {
      const current = await listPushSubscriptions();
      const hash = await endpointHash(subscription.endpoint);
      if (current.items.some((item) => item.endpoint_hash === hash)) return;
    }
    await subscription?.unsubscribe();
    for (const notification of await registration.getNotifications()) notification.close();
  });
}
