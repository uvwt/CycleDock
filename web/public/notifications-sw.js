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

// 持久化暂存用户点击通知但尚未被前端消费的跳转意图。
// 仅放内存会在 iOS 后台回收/重启 Service Worker 时丢失，导致冷启动或切回前台落回主页；
// 使用同源 Cache Storage（前端与 SW 共享）可在 SW 重启或尚未 controller 就绪时双通道恢复。
const PENDING_NAVIGATION_CACHE_NAME = "cycledock-push-nav-v1";
const PENDING_NAVIGATION_CACHE_KEY = "/__cycledock_pending_navigation__";
const PENDING_NAVIGATION_TTL_MS = 60_000;

// 仅在 Cache Storage 不可用的受限环境作为兜底；当 Cache Storage 可用时以持久缓存为唯一真源。
let pendingNavigation = null;

function pendingNavigationRequestURL() {
  return new URL(PENDING_NAVIGATION_CACHE_KEY, self.location.origin).href;
}

function getPendingNavigationCacheStorage() {
  if (self.caches && typeof self.caches.open === "function") {
    return self.caches;
  }
  if (typeof caches !== "undefined" && typeof caches.open === "function") {
    return caches;
  }
  return null;
}

function createPendingNavigationID() {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return crypto.randomUUID();
  }
  return `nav-${Date.now()}-${Math.random().toString(36).slice(2, 10)}`;
}

function isFreshPendingNavigation(record, now = Date.now()) {
  if (!record || typeof record !== "object") return false;
  if (typeof record.url !== "string" || !record.url) return false;
  if (typeof record.timestamp !== "number" || !Number.isFinite(record.timestamp)) {
    return false;
  }
  return now - record.timestamp < PENDING_NAVIGATION_TTL_MS;
}

async function savePendingNavigation(url) {
  const record = {
    id: createPendingNavigationID(),
    url,
    timestamp: Date.now(),
  };
  pendingNavigation = record;
  const cacheStorage = getPendingNavigationCacheStorage();
  if (cacheStorage && typeof Response !== "undefined") {
    try {
      const cache = await cacheStorage.open(PENDING_NAVIGATION_CACHE_NAME);
      await cache.put(
        pendingNavigationRequestURL(),
        new Response(JSON.stringify(record), {
          headers: { "Content-Type": "application/json" },
        }),
      );
    } catch (error) {
      console.error("Failed to persist pending navigation", error);
    }
  }
  return record;
}

async function readPendingNavigation() {
  const cacheStorage = getPendingNavigationCacheStorage();
  if (cacheStorage) {
    try {
      const cache = await cacheStorage.open(PENDING_NAVIGATION_CACHE_NAME);
      const requestURL = pendingNavigationRequestURL();
      const response = await cache.match(requestURL);
      if (!response) {
        // 前端可能已经直接从 Cache Storage 消费并删除，避免内存镜像把旧记录复活。
        pendingNavigation = null;
        return null;
      }
      let parsed = null;
      try {
        parsed = await response.json();
      } catch {
        parsed = null;
      }
      if (!isFreshPendingNavigation(parsed)) {
        await cache.delete(requestURL);
        pendingNavigation = null;
        return null;
      }
      pendingNavigation = parsed;
      return parsed;
    } catch (error) {
      console.error("Failed to read pending navigation", error);
    }
  }
  if (!pendingNavigation) return null;
  if (!isFreshPendingNavigation(pendingNavigation)) {
    pendingNavigation = null;
    return null;
  }
  return pendingNavigation;
}

async function clearPendingNavigation(expectedID) {
  if (!expectedID || pendingNavigation?.id === expectedID) {
    pendingNavigation = null;
  }
  const cacheStorage = getPendingNavigationCacheStorage();
  if (!cacheStorage) return;
  try {
    const cache = await cacheStorage.open(PENDING_NAVIGATION_CACHE_NAME);
    const requestURL = pendingNavigationRequestURL();
    if (!expectedID) {
      await cache.delete(requestURL);
      return;
    }
    const response = await cache.match(requestURL);
    if (!response) return;
    let parsed = null;
    try {
      parsed = await response.json();
    } catch {
      parsed = null;
    }
    // 只清理匹配 id 或已过期/损坏的记录，防止旧通知的清理误删用户刚点的新通知意图。
    if (!parsed || !parsed.id || parsed.id === expectedID || !isFreshPendingNavigation(parsed)) {
      await cache.delete(requestURL);
    }
  } catch (error) {
    console.error("Failed to clear pending navigation", error);
  }
}

async function consumePendingNavigation() {
  const record = await readPendingNavigation();
  if (!record) return null;
  await clearPendingNavigation(record.id);
  return record;
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
    const work = (async () => {
      const record = await consumePendingNavigation();
      const reply = {
        type: "CYCLE_PENDING_NAVIGATION",
        url: record ? record.url : null,
        id: record ? record.id : null,
        timestamp: record ? record.timestamp : null,
      };
      if (event.ports && event.ports[0]) {
        event.ports[0].postMessage(reply);
      } else if (event.source?.postMessage) {
        event.source.postMessage(reply);
      }
    })();
    if (typeof event.waitUntil === "function") {
      event.waitUntil(work);
    }
    return;
  }
  if (event.data?.type === "CYCLE_CLEAR_PENDING_NAVIGATION") {
    const work = clearPendingNavigation(event.data.id);
    if (typeof event.waitUntil === "function") {
      event.waitUntil(work);
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

      // 先持久化写入待跳转意图，确保 iOS 后台回收 SW 或冷启动回落 start_url 时仍可恢复。
      const record = await savePendingNavigation(url.href);

      const windows = await self.clients.matchAll({
        type: "window",
        includeUncontrolled: true,
      });

      // 1. 若已有完全停留在目标地址的同源窗口，直接聚焦并清理本次待跳转记录。
      const exactMatch = windows.find((client) => {
        try {
          const current = new URL(client.url);
          return (
            current.origin === self.location.origin &&
            current.pathname === url.pathname &&
            current.search === url.search &&
            current.hash === url.hash
          );
        } catch {
          return false;
        }
      });
      if (exactMatch) {
        try {
          await exactMatch.focus();
        } catch {
          // 忽略部分环境下 focus 抛错，仍清理同页重复意图。
        }
        await clearPendingNavigation(record.id);
        return;
      }

      // 2. 若存在其他同源窗口：不要在 SW 侧提前清空待跳转记录；
      // 先聚焦窗口，再优先通过 postMessage 通知前端 SPA 无刷新切路由，
      // 避免 iOS WebKit 中 client.navigate() 静默失效或回落主页。
      // 待前端真正收到并执行跳转后再清除持久化记录；即使消息因页面挂起未送达，
      // 前端切回前台时也会通过 visibilitychange / pageshow / focus 从持久化缓存兜底消费。
      const anyWindow = windows.find((client) => {
        try {
          return new URL(client.url).origin === self.location.origin;
        } catch {
          return false;
        }
      });
      if (anyWindow) {
        try {
          await anyWindow.focus();
        } catch {
          // 某些环境下 focus 可能失败，仍继续投递导航消息。
        }
        if (typeof anyWindow.postMessage === "function") {
          anyWindow.postMessage({
            type: "CYCLE_NAVIGATE",
            url: record.url,
            id: record.id,
            timestamp: record.timestamp,
          });
          return;
        }
        if ("navigate" in anyWindow && typeof anyWindow.navigate === "function") {
          try {
            await anyWindow.navigate(record.url);
          } catch {
            // navigate 失败时保留持久化记录，等前端恢复后兜底消费。
          }
        }
        return;
      }

      // 3. 没有任何现存窗口（冷启动）：持久化记录已落盘，再调用 openWindow。
      // 即使 iOS 将冷启动重定向至 manifest 的 start_url: "/"，前端启动后会立即消费持久化记录并跳转。
      if (self.clients.openWindow) {
        await self.clients.openWindow(record.url);
      }
    })(),
  );
});
