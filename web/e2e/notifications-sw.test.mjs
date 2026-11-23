import { readFile } from "node:fs/promises";
import vm from "node:vm";
import test from "node:test";
import assert from "node:assert/strict";
import {
  PENDING_NAVIGATION_CACHE_KEY,
  PENDING_NAVIGATION_CACHE_NAME,
  PENDING_NAVIGATION_TTL_MS,
  consumePendingNavigationFromCache,
  createPendingNavigationConsumer,
} from "../src/shared/realtime/pendingNavigation.ts";

const ORIGIN = "https://cycledock.test";
const swSource = await readFile(
  new URL("../public/notifications-sw.js", import.meta.url),
  "utf8",
);

// 轻量内存 CacheStorage，用于同时模拟 Service Worker 与前端窗口共享的同源 Cache Storage。
function createMockCacheStorage() {
  const buckets = new Map();
  return {
    buckets,
    async open(name) {
      let bucket = buckets.get(name);
      if (!bucket) {
        bucket = new Map();
        buckets.set(name, bucket);
      }
      return {
        async put(key, response) {
          const text = await response.text();
          bucket.set(String(key), text);
        },
        async match(key) {
          const text = bucket.get(String(key));
          if (text === undefined) return undefined;
          return {
            async json() {
              return JSON.parse(text);
            },
            async text() {
              return text;
            },
          };
        },
        async delete(key) {
          return bucket.delete(String(key));
        },
      };
    },
    async readRaw(name = PENDING_NAVIGATION_CACHE_NAME) {
      const bucket = buckets.get(name);
      if (!bucket) return null;
      const key = new URL(PENDING_NAVIGATION_CACHE_KEY, ORIGIN).href;
      const text = bucket.get(key);
      return text === undefined ? null : JSON.parse(text);
    },
  };
}

function createFakeDate(getNow) {
  return class FakeDate extends Date {
    static now() {
      return getNow();
    }
  };
}

function bootWorker({ caches, getNow = () => Date.now() }) {
  const handlers = new Map();
  const shown = [];
  const opened = [];
  const windows = [];
  let idSeq = 0;

  const self = {
    location: { origin: ORIGIN },
    caches,
    addEventListener: (name, handler) => handlers.set(name, handler),
    skipWaiting: async () => {},
    clients: {
      claim: async () => {},
      matchAll: async () => windows,
      openWindow: async (url) => {
        opened.push(url);
      },
    },
    registration: {
      getNotifications: async ({ tag }) =>
        shown.filter((item) => item.tag === tag),
      showNotification: async (title, options) => {
        shown.push({ title, ...options });
      },
    },
  };

  vm.runInNewContext(swSource, {
    self,
    caches,
    URL,
    Response,
    Date: createFakeDate(getNow),
    crypto: {
      randomUUID: () => `nav-uuid-${++idSeq}`,
    },
    console: { error() {} },
  });

  return {
    shown,
    opened,
    windows,
    async click(url) {
      let work;
      handlers.get("notificationclick")({
        notification: {
          close() {},
          data: { url },
        },
        waitUntil: (promise) => {
          work = promise;
        },
      });
      await work;
    },
    async postMessage(data, port) {
      let work;
      handlers.get("message")({
        data,
        ports: port ? [port] : [],
        waitUntil: (promise) => {
          work = promise;
        },
      });
      await work;
    },
    asClientWorker() {
      return {
        postMessage: (data, transfer) => {
          const port = transfer?.[0];
          void this.postMessage(data, port);
        },
      };
    },
  };
}

test("持久化写入、读取与超过 60s TTL 后自动过期清理", async () => {
  let now = 1_700_000_000_000;
  const caches = createMockCacheStorage();
  const sw = bootWorker({ caches, getNow: () => now });

  await sw.click("/cards/project-1/card-1");
  const persisted = await caches.readRaw();
  assert.ok(persisted);
  assert.equal(persisted.url, `${ORIGIN}/cards/project-1/card-1`);
  assert.equal(persisted.id, "nav-uuid-1");
  assert.equal(persisted.timestamp, now);

  // 未到 60s（59s）仍应被视为有效
  now += PENDING_NAVIGATION_TTL_MS - 1_000;
  const responsesFresh = [];
  await sw.postMessage(
    { type: "CYCLE_CHECK_PENDING_NAVIGATION" },
    { postMessage: (msg) => responsesFresh.push(msg) },
  );
  assert.equal(responsesFresh.length, 1);
  assert.equal(responsesFresh[0].url, `${ORIGIN}/cards/project-1/card-1`);
  assert.equal(responsesFresh[0].id, "nav-uuid-1");
  assert.equal(await caches.readRaw(), null);

  // 再次写入后超过 60s：读取返回 null 并自动清理过期缓存
  await sw.click("/plans/project-1/plan-2");
  assert.ok(await caches.readRaw());
  now += PENDING_NAVIGATION_TTL_MS + 1;
  const responsesExpired = [];
  await sw.postMessage(
    { type: "CYCLE_CHECK_PENDING_NAVIGATION" },
    { postMessage: (msg) => responsesExpired.push(msg) },
  );
  assert.equal(responsesExpired[0].url, null);
  assert.equal(await caches.readRaw(), null);
});

test("Service Worker 被 iOS 回收重启后仍可从持久化缓存恢复待跳转目标", async () => {
  const now = 1_700_000_100_000;
  const sharedCaches = createMockCacheStorage();

  // 第一个 SW 实例处理通知点击后即被系统回收
  const swBeforeRestart = bootWorker({ caches: sharedCaches, getNow: () => now });
  await swBeforeRestart.click("/chat/project-1/main-discussion");
  assert.deepEqual(swBeforeRestart.opened, [
    `${ORIGIN}/chat/project-1/main-discussion`,
  ]);

  // 全新的 SW 实例（内存完全清空），共享同一 Cache Storage
  const swAfterRestart = bootWorker({ caches: sharedCaches, getNow: () => now + 5_000 });
  const responses = [];
  await swAfterRestart.postMessage(
    { type: "CYCLE_CHECK_PENDING_NAVIGATION" },
    { postMessage: (msg) => responses.push(msg) },
  );
  assert.equal(responses.length, 1);
  assert.equal(responses[0].type, "CYCLE_PENDING_NAVIGATION");
  assert.equal(responses[0].url, `${ORIGIN}/chat/project-1/main-discussion`);
  assert.ok(responses[0].id);
});

test("notificationclick 命中已有窗口时优先 postMessage 且不在 SW 侧提前清空持久化记录", async () => {
  const now = 1_700_000_200_000;
  const caches = createMockCacheStorage();
  const sw = bootWorker({ caches, getNow: () => now });

  const calls = [];
  const posted = [];
  let navigateCalled = false;
  sw.windows.push({
    url: `${ORIGIN}/cards/project-1/card-a`,
    focus: async () => {
      calls.push("focus");
    },
    navigate: async () => {
      navigateCalled = true;
    },
    postMessage: (msg) => {
      calls.push("postMessage");
      posted.push(msg);
    },
  });

  await sw.click("/cards/project-1/card-b");

  assert.deepEqual(calls, ["focus", "postMessage"]);
  assert.equal(navigateCalled, false);
  assert.equal(sw.opened.length, 0);
  assert.equal(posted.length, 1);
  assert.equal(posted[0].type, "CYCLE_NAVIGATE");
  assert.equal(posted[0].url, `${ORIGIN}/cards/project-1/card-b`);
  assert.ok(posted[0].id);

  // SW 侧尚未清空持久化记录，防止 iOS 挂起窗口丢失 postMessage
  const stillPersisted = await caches.readRaw();
  assert.ok(stillPersisted);
  assert.equal(stillPersisted.id, posted[0].id);
  assert.equal(stillPersisted.url, `${ORIGIN}/cards/project-1/card-b`);
});

test("冷启动回落 '/' 时前端通过 Cache Storage 直读优先消费深链并以 replace 跳转", async () => {
  const now = 1_700_000_300_000;
  const caches = createMockCacheStorage();
  const sw = bootWorker({ caches, getNow: () => now });

  // SW 处理通知点击落盘（模拟 iOS 冷启动最终落到 start_url: "/" 且 SW 尚未接管 controller）
  await sw.click("/cards/project-9/card-42");

  let currentPath = "/";
  const navigated = [];
  const consumer = createPendingNavigationConsumer({
    origin: ORIGIN,
    caches,
    // 模拟冷启动瞬间尚未获得 controller / registration，验证 Cache Storage 直读兜底
    serviceWorker: {
      controller: null,
      getRegistration: async () => undefined,
    },
    now: () => now + 2_000,
    getCurrentPath: () => currentPath,
    onNavigate: (targetPath, opts) => {
      navigated.push({ targetPath, replace: opts.replace });
      currentPath = targetPath;
    },
  });

  const handled = await consumer.checkPending({ replace: currentPath === "/" });
  assert.equal(handled, true);
  assert.deepEqual(navigated, [
    { targetPath: "/cards/project-9/card-42", replace: true },
  ]);
  // 消费后立即删除缓存记录
  assert.equal(await caches.readRaw(), null);
  assert.equal(
    await consumePendingNavigationFromCache({ caches, origin: ORIGIN, now }),
    null,
  );
});

test("visibilitychange 从后台唤醒时消费持久化记录，并按 id 去重避免重复跳转", async () => {
  const now = 1_700_000_400_000;
  const caches = createMockCacheStorage();
  const sw = bootWorker({ caches, getNow: () => now });

  // 场景 A：页面在 card-a 处于后台，SW 投递 postMessage 被 iOS 挂起吞掉，
  // 切回前台触发 visibilitychange（visible）后自动从持久化缓存跳转到 card-b。
  sw.windows.push({
    url: `${ORIGIN}/cards/project-1/card-a`,
    focus: async () => {},
    postMessage: () => {
      // 模拟后台冻结时 postMessage 未送达前端
    },
  });
  await sw.click("/cards/project-1/card-b");
  assert.ok(await caches.readRaw());

  let currentPath = "/cards/project-1/card-a";
  const navigated = [];
  const consumer = createPendingNavigationConsumer({
    origin: ORIGIN,
    caches,
    serviceWorker: {
      controller: sw.asClientWorker(),
    },
    now: () => now + 1_000,
    getCurrentPath: () => currentPath,
    onNavigate: (targetPath, opts) => {
      navigated.push({ targetPath, replace: opts.replace });
      currentPath = targetPath;
    },
  });

  // 模拟 visibilitychange 切回 visible 与随后的 pageshow/focus 连续触发
  const listeners = new Map();
  let visibilityState = "hidden";
  const onVisibilityChange = () => {
    if (visibilityState !== "visible") return;
    void consumer.checkPending({ replace: currentPath === "/" });
  };
  listeners.set("visibilitychange", onVisibilityChange);

  visibilityState = "visible";
  listeners.get("visibilitychange")();
  // 等待微任务与异步读取完成
  await consumer.checkPending({ replace: currentPath === "/" });
  assert.deepEqual(navigated, [
    { targetPath: "/cards/project-1/card-b", replace: false },
  ]);
  assert.equal(await caches.readRaw(), null);

  // 场景 B：前台收到 CYCLE_NAVIGATE 后立即清除持久化记录并记录 id，
  // 紧接着 visibilitychange 再次触发也不会重复导航，即使历史同 id 消息迟到也会被去重。
  const postedMessages = [];
  sw.windows[0] = {
    url: `${ORIGIN}/cards/project-1/card-b`,
    focus: async () => {},
    postMessage: (msg) => postedMessages.push(msg),
  };
  await sw.click("/plans/project-1/plan-99");
  assert.equal(postedMessages.length, 1);

  const firstApply = consumer.handleWorkerMessage(postedMessages[0], {
    replace: false,
  });
  assert.equal(firstApply, true);
  // 同一个 id 的迟到消息或重复触发会被严格去重
  const duplicateApply = consumer.handleWorkerMessage(postedMessages[0], {
    replace: false,
  });
  assert.equal(duplicateApply, false);

  await consumer.checkPending({ replace: false });
  assert.deepEqual(navigated, [
    { targetPath: "/cards/project-1/card-b", replace: false },
    { targetPath: "/plans/project-1/plan-99", replace: false },
  ]);
  assert.equal(await caches.readRaw(), null);
});

test("旧通知清理不会误删用户新点击的通知意图", async () => {
  const now = 1_700_000_500_000;
  const caches = createMockCacheStorage();
  const sw = bootWorker({ caches, getNow: () => now });

  await sw.click("/cards/project-1/card-old");
  const oldRecord = await caches.readRaw();
  await sw.click("/cards/project-1/card-new");
  const newRecord = await caches.readRaw();
  assert.notEqual(oldRecord.id, newRecord.id);

  // 清理旧 id 时，新记录必须保留
  await sw.postMessage({
    type: "CYCLE_CLEAR_PENDING_NAVIGATION",
    id: oldRecord.id,
  });
  const remaining = await caches.readRaw();
  assert.ok(remaining);
  assert.equal(remaining.id, newRecord.id);
  assert.equal(remaining.url, `${ORIGIN}/cards/project-1/card-new`);
});
