import test from "node:test";
import assert from "node:assert/strict";
import {
  scheduleFormSchema,
  scheduleFormInput,
} from "../src/features/cards/schedule-form.ts";
import { mcpFormSchema } from "../src/features/settings/mcp-form.ts";

const daily = {
  frequency: "daily",
  time: "09:05",
  timezone: " Asia/Shanghai ",
};
const mcp = {
  name: " MCP ",
  url: " https://example.com/mcp ",
  enabled: true,
  timeout_ms: 30000,
  bearer_token: "",
  clear_bearer_token: false,
};

test("日程提交规范化时区，且只发送当前频率需要的日期字段", () => {
  const value = scheduleFormSchema.parse({ ...daily, weekday: 2, monthday: 8 });
  assert.deepEqual(scheduleFormInput(value, false), {
    frequency: "daily",
    hour: 9,
    minute: 5,
    timezone: "Asia/Shanghai",
    enabled: false,
  });
  const weekly = scheduleFormSchema.parse({
    ...daily,
    frequency: "weekly",
    weekday: 0,
  });
  assert.equal(scheduleFormInput(weekly, true).day_of_week, 0);
  assert.equal("day_of_month" in scheduleFormInput(weekly, true), false);
});

test("日程拒绝无效时间、时区以及缺失或越界的周/月日期", () => {
  for (const change of [
    { time: "24:00" },
    { time: "" },
    { timezone: "Not/AZone" },
    { frequency: "weekly" },
    { frequency: "weekly", weekday: 7 },
    { frequency: "monthly" },
    { frequency: "monthly", monthday: 29 },
  ])
    assert.equal(
      scheduleFormSchema.safeParse({ ...daily, ...change }).success,
      false,
    );
});

test("MCP 表单规范化名称和 URL，Token 原样传递", () => {
  const value = mcpFormSchema.parse({ ...mcp, bearer_token: " unchanged " });
  assert.equal(value.name, "MCP");
  assert.equal(value.url, "https://example.com/mcp");
  assert.equal(value.bearer_token, " unchanged ");
});

test("MCP 保存与测试均拒绝无效地址、超时和 Token 冲突", () => {
  for (const change of [
    { name: " " },
    { url: "bad" },
    { url: "file:///tmp/mcp" },
    { timeout_ms: 999 },
    { timeout_ms: 300001 },
    { timeout_ms: NaN },
    { bearer_token: "new-token", clear_bearer_token: true },
  ])
    assert.equal(mcpFormSchema.safeParse({ ...mcp, ...change }).success, false);
  assert.equal(
    mcpFormSchema.safeParse({ ...mcp, clear_bearer_token: true }).success,
    true,
  );
});
