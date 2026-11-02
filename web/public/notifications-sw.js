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

// 暂存用户点击通知但尚未被前端消费的跳转意图（应对 iOS PWA 冷启动或重定向丢失）
let pendingNavigation = null;
const PENDING_NAVIGATION_TTL_MS = 30_000;

function setPendingNavigation(url) {
  pendingNavigation = {
    url,
    timestamp: Date.now(),
  };
}

function consumePendingNavigation() {
  if (!pendingNavigation) return null;
  const isFresh =
    Date.now() - pendingNavigation.timestamp < PENDING_NAVIGATION_TTL_MS;
  const nav = isFresh ? pendingNavigation.url : null;
  pendingNavigation = null;
  return nav;
}

function enqueue(update, options = {}) {
  const work = delivery
    .catch((error) => console.error("Card notification failed", error))
    .then(() => deliverCardUpdate(update, options));
  delivery = work;
  return work;
}

function resolveTargetURL(rawURL, fallbackToRoot) {
  try {
    const parsed = new URL(rawURL || "/", self.location.origin);
    if (parsed.origin === self.location.origin) return parsed;
  } catch {
    // 非法 URL 时按调用来源决定忽略或回退根路径。
  }
  return fallbackToRoot ? new URL("/", self.location.origin) : null;
}

async function deliverCardUpdate(update, options = {}) {
  const fromPush = options.fromPush === true;
  const url = resolveTargetURL(update.url, fromPush);
  if (!url) return;
  const key = `${update.workspaceID}:${update.cardID}`;
  // 正在查看时也消费版本，避免另一个标签页稍后把已读更新再次通知。
  const remember = () => {
    if (!update.revision) return;
    seen.set(key, update.revision);
    if (seen.size > 1000) seen.delete(seen.keys().next().value);
  };
  const tag = `card:${key}`;
  if (!fromPush) {
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
    if (viewing) {
      remember();
      return;
    }
    const existing = await self.registration.getNotifications({ tag });
    if (
      update.revision &&
      existing.some(
        (notification) => notification.data?.revision === update.revision,
      )
    )
      return;
  }
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
  if (event.data?.type === "CYCLE_CHECK_PENDING_NAVIGATION") {
    const targetURL = consumePendingNavigation();
    if (event.ports && event.ports[0]) {
      event.ports[0].postMessage({
        type: "CYCLE_PENDING_NAVIGATION",
        url: targetURL,
      });
    } else if (event.source?.postMessage) {
      event.source.postMessage({
        type: "CYCLE_PENDING_NAVIGATION",
        url: targetURL,
      });
    }
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
  event.waitUntil(enqueue(update, { fromPush: true }));
});

self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  event.waitUntil(
    (async () => {
      const rawTarget = event.notification.data?.url;
      const url = resolveTargetURL(rawTarget, false);
      if (!url || url.origin !== self.location.origin) return;

      // 记录待消费导航意图，兜底 iOS PWA 冷启动或路由丢失
      setPendingNavigation(url.href);

      const windows = await self.clients.matchAll({
        type: "window",
        includeUncontrolled: true,
      });

      // 1. 若已有完全匹配目标路径的窗口，直接聚焦
      const exactMatch = windows.find(
        (client) => new URL(client.url).pathname === url.pathname,
      );
      if (exactMatch) {
        consumePendingNavigation();
        return exactMatch.focus();
      }

      // 2. 若存在同源窗口（但停留在其他路径），优先聚焦并通过 client.navigate 或 postMessage 通知前端 SPA 切路由
      const anyWindow = windows.find((client) => {
        try {
          return new URL(client.url).origin === self.location.origin;
        } catch {
          return false;
        }
      });
      if (anyWindow) {
        consumePendingNavigation();
        await anyWindow.focus();
        if ("navigate" in anyWindow && typeof anyWindow.navigate === "function") {
          try {
            await anyWindow.navigate(url.href);
            return;
          } catch {
            // navigate 失败时回退 postMessage
          }
        }
        anyWindow.postMessage({
          type: "CYCLE_NAVIGATE",
          url: url.href,
        });
        return;
      }

      // 3. 没有任何现存窗口（冷启动）：尝试通过 openWindow 打开
      // 无论 openWindow 是否被系统重定向至 start_url，页面启动后都会通过握手消费 pendingNavigation
      if (self.clients.openWindow) {
        await self.clients.openWindow(url.href);
      }
    })(),
  );
});
