self.addEventListener("install", () => self.skipWaiting());
self.addEventListener("activate", (event) =>
  event.waitUntil(self.clients.claim()),
);

// 各标签页共享此 Worker；串行处理避免同一更新在多个标签页重复弹出。
let delivery = Promise.resolve();
const seen = new Map();
self.addEventListener("message", (event) => {
  if (event.data?.type !== "card.updated") return;
  delivery = delivery
    .catch((error) => console.error("Card notification failed", error))
    .then(async () => {
      const update = event.data;
      const url = new URL(update.url, self.location.origin);
      if (url.origin !== self.location.origin) return;
      const key = `${update.workspaceID}:${update.cardID}`;
      if (seen.get(key) === update.revision) return;
      const windows = await self.clients.matchAll({
        type: "window",
        includeUncontrolled: true,
      });
      const viewing = windows.some((client) => {
        const path = new URL(client.url).pathname;
        return (
          client.visibilityState === "visible" &&
          client.focused &&
          /^\/(chat|cards)\/[^/]+\/[^/]+\/?$/.test(path) &&
          decodeURIComponent(path.split("/")[3]) === update.cardID
        );
      });
      // 正在查看时也消费版本，避免另一个标签页稍后把已读更新再次通知。
      const remember = () => {
        seen.set(key, update.revision);
        if (seen.size > 1000) seen.delete(seen.keys().next().value);
      };
      if (viewing) {
        remember();
        return;
      }
      const tag = `card:${key}`;
      const existing = await self.registration.getNotifications({ tag });
      if (
        existing.some(
          (notification) => notification.data?.revision === update.revision,
        )
      )
        return;
      await self.registration.showNotification(update.title, {
        body: update.body,
        icon: "/cycledock-logo.png",
        tag,
        data: { url: url.href, revision: update.revision },
      });
      remember();
    });
  event.waitUntil(delivery);
});

self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  event.waitUntil(
    (async () => {
      const url = new URL(event.notification.data.url, self.location.origin);
      if (url.origin !== self.location.origin) return;
      const windows = await self.clients.matchAll({
        type: "window",
        includeUncontrolled: true,
      });
      const existing = windows.find(
        (client) => new URL(client.url).pathname === url.pathname,
      );
      if (existing) return existing.focus();
      await self.clients.openWindow(url.href);
    })(),
  );
});
