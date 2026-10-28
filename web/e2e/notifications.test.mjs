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
  vm.runInNewContext(source, { self, URL, console: { error() {} } });
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
  };
}

test("前台查看当前卡片时消费更新，不重复通知", async () => {
  const w = worker();
  w.windows.push({
    url: "https://cycledock.test/chat/project/card",
    focused: true,
    visibilityState: "visible",
  });
  await w.send();
  assert.equal(w.shown.length, 0);
  w.windows.length = 0;
  await w.send();
  assert.equal(w.shown.length, 0);
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

test("回答后生成标题时，通知保留模型回答正文", async () => {
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
  const translate = (key) => key;
  await w.send({
    ...w.update,
    revision: answered.revision,
    body: cardNotificationBody(answered, previous, translate),
  });
  await w.send({
    ...w.update,
    revision: titled.revision,
    title: titled.title,
    body: cardNotificationBody(titled, answered, translate),
  });
  assert.equal(w.shown.length, 2);
  assert.equal(w.shown[1].tag, w.shown[0].tag);
  assert.equal(w.shown[1].body, answered.message_preview);
  assert.equal(w.shown[1].title, titled.title);
});

test("状态变化仍提示失败，不把旧消息当作新回答", () => {
  const previous = {
    event_sequence: "1",
    attempt_status: "running",
    message_preview: "旧消息",
  };
  const failed = { ...previous, attempt_status: "failed" };
  assert.equal(
    cardNotificationBody(failed, previous, (key) => key),
    "执行失败",
  );
});
