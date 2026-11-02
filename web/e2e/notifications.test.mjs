import { readFile } from "node:fs/promises";
import vm from "node:vm";
import test from "node:test";
import assert from "node:assert/strict";
import { cardNotificationBody } from "../src/shared/realtime/cardUpdates.ts";

const source = await readFile(
  new URL("../public/notifications-sw.js", import.meta.url),
  "utf8",
);
function worker() {
  const handlers = new Map();
  const shown = [];
  const opened = [];
  const windows = [];
  let failure = false;
  const self = {
    location: { origin: "https://cycledock.test" },
    addEventListener: (name, handler) => handlers.set(name, handler),
    clients: {
      matchAll: async () => windows,
      openWindow: async (url) => opened.push(url),
    },
    registration: {
      getNotifications: async ({ tag }) =>
        shown.filter((item) => item.tag === tag),
      showNotification: async (title, options) => {
        if (failure) throw new Error("OS rejected delivery");
        shown.push({ title, ...options });
      },
    },
  };
  vm.runInNewContext(source, { self, URL, console: { error() {} }, Date });
  const update = {
    type: "card.updated",
    workspaceID: "workspace",
    cardID: "card",
    revision: "1",
    title: "Updated",
    body: "Click to view",
    url: "/cards/project/card",
  };
  return {
    shown,
    opened,
    windows,
    update,
    fail: (value) => {
      failure = value;
    },
    async view(data, id = "tab") {
      handlers.get("message")({
        data: { type: "card.viewing", ...data },
        source: { id },
        waitUntil() {},
      });
    },
    async send(data = update) {
      let work;
      handlers.get("message")({
        data,
        waitUntil: (promise) => {
          work = promise;
        },
      });
      await work;
    },
    async push(data) {
      let work;
      handlers.get("push")({
        data: data ? { json: () => data } : undefined,
        waitUntil: (promise) => {
          work = promise;
        },
      });
      await work;
    },
    async click(notification) {
      let work;
      handlers.get("notificationclick")({
        notification,
        waitUntil: (promise) => {
          work = promise;
        },
      });
      await work;
    },
    async sendPendingCheck(port) {
      handlers.get("message")({
        data: { type: "CYCLE_CHECK_PENDING_NAVIGATION" },
        ports: port ? [port] : [],
        waitUntil() {},
      });
    },
  };
}

test("正在看当前卡片时不通知，可见但没有系统焦点也一样", async () => {
  for (const url of [
    "https://cycledock.test/chat/project/card",
    "https://cycledock.test/cards/project/card",
    "https://cycledock.test/plans/project/card",
  ]) {
    const w = worker();
    w.windows.push({
      url,
      focused: false,
      visibilityState: "visible",
    });
    await w.send();
    assert.equal(w.shown.length, 0, url);
    w.windows.length = 0;
    await w.send();
    assert.equal(w.shown.length, 0, url);
  }
});

test("页面上报正在查看时，Worker 把窗口看成隐藏也不通知", async () => {
  const w = worker();
  w.windows.push({
    id: "tab",
    url: "https://cycledock.test/chat/project/card",
    focused: false,
    visibilityState: "hidden",
  });
  await w.view({ cardID: "card", visible: true });
  await w.send();
  assert.equal(w.shown.length, 0);
  await w.send({ ...w.update, revision: "2", viewing: true });
  assert.equal(w.shown.length, 0);
});

test("Web Push 唤醒时始终展示可见通知，避免 iOS WebKit 因残留查看态吞通知或收回权限", async () => {
  const closed = worker();
  await closed.push(closed.update);
  assert.equal(closed.shown.length, 1);

  // iOS 锁屏或切主屏幕瞬间，PWA 窗口仍存在且 viewingByClient 可能残留 visible:true；
  // push 事件必须无条件 showNotification，同时记录 revision 防止回前台后 SSE 重复弹窗。
  const suspended = worker();
  suspended.windows.push({
    id: "ios-pwa",
    url: "https://cycledock.test/chat/project/card",
    focused: false,
    visibilityState: "hidden",
  });
  await suspended.view({ cardID: "card", visible: true }, "ios-pwa");
  await suspended.push(suspended.update);
  assert.equal(suspended.shown.length, 1);
  await suspended.view({ cardID: "", visible: false }, "ios-pwa");
  await suspended.send(suspended.update);
  assert.equal(suspended.shown.length, 1);

  // push 载荷地址异常时回退到同源根路径并照常展示通知。
  const invalidURL = worker();
  await invalidURL.push({ ...invalidURL.update, url: "https://external.test/x" });
  assert.equal(invalidURL.shown.length, 1);
  assert.equal(invalidURL.shown[0].data.url, "https://cycledock.test/");
});

test("后台或查看其他卡片时通知，多标签页的同一更新只通知一次", async () => {
  for (const client of [
    {
      url: "https://cycledock.test/cards/project/card",
      focused: false,
      visibilityState: "hidden",
    },
    {
      url: "https://cycledock.test/cards/project/other",
      focused: true,
      visibilityState: "visible",
    },
  ]) {
    const w = worker();
    w.windows.push(client);
    await Promise.all([w.send(), w.send()]);
    assert.equal(w.shown.length, 1);
    await w.send({ ...w.update, revision: "2" });
    assert.equal(w.shown.length, 2);
    assert.equal(w.shown[0].tag, w.shown[1].tag);
  }
});

test("Worker 重启后也不会重复已显示的通知", async () => {
  const w = worker();
  w.shown.push({ tag: "card:workspace:card", data: { revision: "1" } });
  await w.send();
  assert.equal(w.shown.length, 1);
});

test("通知点击打开对应卡片，已有页面则聚焦", async () => {
  const w = worker();
  await w.send();
  let closed = false;
  await w.click({
    ...w.shown[0],
    close: () => {
      closed = true;
    },
  });
  assert.equal(closed, true);
  assert.deepEqual(w.opened, ["https://cycledock.test/cards/project/card"]);
  let focused = false;
  w.windows.push({
    url: w.opened[0],
    focus: async () => {
      focused = true;
    },
  });
  await w.click({ ...w.shown[0], close() {} });
  assert.equal(focused, true);
  assert.equal(w.opened.length, 1);
});

test("通知点击已有同源其他页面时，聚焦并触发导航通知", async () => {
  const w = worker();
  await w.send();
  let focused = false;
  const messages = [];
  let navigatedTo = null;
  w.windows.push({
    url: "https://cycledock.test/chat",
    focus: async () => {
      focused = true;
    },
    navigate: async (url) => {
      navigatedTo = url;
    },
    postMessage: (msg) => {
      messages.push(msg);
    },
  });
  await w.click({ ...w.shown[0], close() {} });
  assert.equal(focused, true);
  assert.equal(navigatedTo, "https://cycledock.test/cards/project/card");
  assert.equal(w.opened.length, 0);

  // 若 client.navigate 失败，降级通过 postMessage 传递 CYCLE_NAVIGATE
  const w2 = worker();
  await w2.send();
  let focused2 = false;
  const messages2 = [];
  w2.windows.push({
    url: "https://cycledock.test/chat",
    focus: async () => {
      focused2 = true;
    },
    navigate: async () => {
      throw new Error("navigate not supported");
    },
    postMessage: (msg) => {
      messages2.push(msg);
    },
  });
  await w2.click({ ...w2.shown[0], close() {} });
  assert.equal(focused2, true);
  assert.equal(messages2.length, 1);
  assert.equal(messages2[0].type, "CYCLE_NAVIGATE");
  assert.equal(messages2[0].url, "https://cycledock.test/cards/project/card");
});

test("冷启动下通知点击暂存待消费意图，前端握手即可获取目标卡片并消费", async () => {
  const w = worker();
  await w.send();
  await w.click({ ...w.shown[0], close() {} });
  assert.deepEqual(w.opened, ["https://cycledock.test/cards/project/card"]);

  // 前端握手拉取未消费意图
  const responses = [];
  const fakePort = {
    postMessage: (msg) => responses.push(msg),
  };
  await w.sendPendingCheck(fakePort);
  assert.equal(responses.length, 1);
  assert.equal(responses[0].type, "CYCLE_PENDING_NAVIGATION");
  assert.equal(responses[0].url, "https://cycledock.test/cards/project/card");

  // 第二次拉取应已清空（消费过）
  responses.length = 0;
  await w.sendPendingCheck(fakePort);
  assert.equal(responses.length, 1);
  assert.equal(responses[0].type, "CYCLE_PENDING_NAVIGATION");
  assert.equal(responses[0].url, null);
});

test("拒绝外部地址", async () => {
  const w = worker();
  await w.send({ ...w.update, url: "https://external.test/card" });
  assert.equal(w.shown.length, 0);
});

test("通知投递失败后允许重试", async () => {
  const w = worker();
  w.fail(true);
  await assert.rejects(w.send());
  w.fail(false);
  await w.send();
  assert.equal(w.shown.length, 1);
});

test("只有新消息才生成通知正文", async () => {
  const w = worker();
  const previous = {
    id: "card",
    revision: "running",
    event_sequence: "1",
    attempt_status: "running",
    message_preview: "你好",
    title: "",
  };
  const answered = {
    ...previous,
    revision: "answered",
    event_sequence: "2",
    attempt_status: "completed",
    message_preview: "你好！我可以帮你处理项目任务。",
  };
  const titled = { ...answered, revision: "titled", title: "项目任务讨论" };
  const pinned = { ...titled, revision: "pinned" };
  const failed = { ...pinned, revision: "failed", attempt_status: "failed" };
  assert.equal(cardNotificationBody(answered, previous), answered.message_preview);
  assert.equal(cardNotificationBody(titled, answered), "");
  assert.equal(cardNotificationBody(pinned, titled), "");
  assert.equal(cardNotificationBody(failed, pinned), "");
  await w.send({
    ...w.update,
    revision: answered.revision,
    body: cardNotificationBody(answered, previous),
  });
  assert.equal(w.shown.length, 1);
  assert.equal(w.shown[0].body, answered.message_preview);
});
