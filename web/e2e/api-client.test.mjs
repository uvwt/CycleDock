import test from "node:test";
import assert from "node:assert/strict";
import { api, APIError } from "../src/shared/api/client.ts";

test("请求一直没有响应时结束等待，并提示检查操作结果", async (t) => {
  const controller = new AbortController();
  t.mock.method(AbortSignal, "timeout", (milliseconds) => {
    assert.equal(milliseconds, 15_000);
    return controller.signal;
  });
  t.mock.method(globalThis, "fetch", async (_url, { signal }) => {
    const pending = new Promise((_resolve, reject) => {
      signal.addEventListener("abort", () => reject(signal.reason));
    });
    controller.abort();
    return pending;
  });
  await assert.rejects(api.me(), (error) => {
    assert.ok(error instanceof APIError);
    assert.equal(error.code, "request_timeout");
    assert.match(error.message, /检查操作结果/);
    return true;
  });
});

test("响应头已返回但正文卡住时，同样结束等待", async (t) => {
  const controller = new AbortController();
  t.mock.method(AbortSignal, "timeout", () => controller.signal);
  t.mock.method(globalThis, "fetch", async () => ({
    ok: true,
    status: 200,
    json: () => {
      controller.abort();
      return Promise.reject(controller.signal.reason);
    },
  }));
  await assert.rejects(api.me(), (error) => error.code === "request_timeout");
});

test("服务端返回错误时保留原始错误码与提示", async (t) => {
  t.mock.method(
    globalThis,
    "fetch",
    async () =>
      new Response(
        JSON.stringify({
          error: { code: "unauthorized", message: "请重新登录" },
        }),
        { status: 401 },
      ),
  );
  await assert.rejects(api.me(), (error) => {
    assert.equal(error.status, 401);
    assert.equal(error.code, "unauthorized");
    assert.equal(error.message, "请重新登录");
    return true;
  });
});

test("连接测试使用服务端执行预算，不被普通请求超时截断", async (t) => {
  const budgets = [];
  t.mock.method(AbortSignal, "timeout", (milliseconds) => {
    budgets.push(milliseconds);
    return new AbortController().signal;
  });
  t.mock.method(globalThis, "fetch", async () => new Response("{}"));
  await api.testProvider({});
  await api.testMCP({ timeout_ms: 60_000 });
  assert.deepEqual(budgets, [125_000, 65_000]);
});
