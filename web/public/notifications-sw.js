self.addEventListener("install", () => self.skipWaiting());
self.addEventListener("activate", (event) =>
  event.waitUntil(self.clients.claim()),
);

// 各标签页共享此 Worker；串行处理避免同一更新在多个标签页重复弹出。
let delivery = Promise.resolve();
const seen = new Map();
// 页面用 document.visibilityState 上报。Worker 的 clients.matchAll()
// 在 Edge/Chrome 里会把前台标签报成 hidden 或未聚焦，不能用来决定是否弹通知。
const viewingByClient = new Map();

function enqueue(update) {
  const work = delivery
    .catch((error) => console.error("Card notification failed", error))
    .then(() => deliverCardUpdate(update));
  delivery = work;
  return work;
}

async function deliverCardUpdate(update) {
  const url = new URL(update.url || "/", self.location.origin);
  if (url.origin !== self.location.origin) return;
  const key = `${update.workspaceID}:${update.cardID}`;
  if (update.revision && seen.get(key) === update.revision) return;
  const windows = await self.clients.matchAll({
    type: "window",
    includeUncontrolled: true,
  });
  const alive = new Set(windows.map((client) => client.id));
  for (const id of viewingByClient.keys()) {
    if (!alive.has(id)) viewingByClient.delete(id);
  }
  const reported = [...viewingByClient.values()].some(
    (state) => state.visible && state.cardID === update.cardID,
  );
  // 没上报过的旧页面仍按窗口地址兜底。matchAll 的 focused 不可靠，这里不再看它。
  const visibleWindow = windows.some((client) => {
    const path = new URL(client.url).pathname;
    const cardID = path.split("/")[3];
    return (
      client.visibilityState === "visible" &&
      cardID &&
      /^\/(chat|cards|plans)\/[^/]+\/[^/]+\/?$/.test(path) &&
      decodeURIComponent(cardID) === update.cardID
    );
  });
  const viewing = reported || update.viewing === true || visibleWindow;
  // 正在查看时也消费版本，避免另一个标签页稍后把已读更新再次通知。
  const remember = () => {
    if (!update.revision) return;
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
    update.revision &&
    existing.some(
      (notification) => notification.data?.revision === update.revision,
    )
  )
    return;
  await self.registration.showNotification(update.title || "卡片有更新", {
    body: update.body || "卡片有更新，点击查看",
    icon: "/cycledock-logo.png",
    tag,
    data: { url: url.href, revision: update.revision },
  });
  remember();
}

self.addEventListener("message", (event) => {
  if (event.data?.type === "card.viewing" && event.source?.id) {
    viewingByClient.set(event.source.id, {
      cardID: event.data.cardID || "",
      visible: event.data.visible === true,
    });
    return;
  }
  if (event.data?.type !== "card.updated") return;
  event.waitUntil(enqueue(event.data));
});

// 页面已关闭时由服务端推送唤醒。正常情况下此时没有可见窗口，必须展示通知；
// Safari 对收到推送却不展示的网站会收回通知权限。
self.addEventListener("push", (event) => {
  let update = {
    type: "card.updated",
    workspaceID: "",
    cardID: "",
    revision: "",
    title: "卡片有更新",
    body: "卡片有更新，点击查看",
    url: "/",
  };
  try {
    const parsed = event.data?.json();
    if (parsed && typeof parsed === "object") update = { ...update, ...parsed };
  } catch {
    // 无法解析时仍展示一条通知，避免 Safari 收回权限。
  }
  event.waitUntil(enqueue(update));
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
