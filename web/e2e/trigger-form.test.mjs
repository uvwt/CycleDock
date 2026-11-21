import test from "node:test";
import assert from "node:assert/strict";
import {
  emptyTriggerFormValues,
  formatRemainingCountdown,
  getCardRoute,
  hasAdvancedTriggerConfig,
  parseCommaList,
  parseJSONMatchesText,
  toCreateTriggerInput,
  toUpdateTriggerInput,
  triggerFormSchema,
  triggerToFormValues,
} from "../src/features/settings/trigger-form.ts";

test("触发器表单校验与逗号列表/JSON 匹配解析", () => {
  assert.deepEqual(parseCommaList(" workflow_run, pull_request ，WORKFLOW_RUN \n push "), [
    "workflow_run",
    "pull_request",
    "push",
  ]);

  const parsed = parseJSONMatchesText("event.status=failed\n\nenv = prod");
  assert.equal(parsed.error, undefined);
  assert.deepEqual(parsed.value, { "event.status": "failed", env: "prod" });

  const invalid = parseJSONMatchesText("missing_equals_sign");
  assert.match(invalid.error || "", /格式无效/);
});

test("新建与编辑触发器按路由模式只发送对应目标字段", () => {
  const base = emptyTriggerFormValues("proj-1");

  const watcherValues = triggerFormSchema.parse({
    ...base,
    name: "  GitHub CI Wakeup  ",
    provider: "github",
    routing_mode: "match_watchers",
    events_text: "workflow_run",
    conclusions_text: "success, failure",
    cooldown_seconds: 30,
  });
  const createWatcher = toCreateTriggerInput(watcherValues);
  assert.equal(createWatcher.name, "GitHub CI Wakeup");
  assert.equal(createWatcher.auth_mode, "github_hmac_sha256");
  assert.equal(createWatcher.target_card_id, undefined);
  assert.equal(createWatcher.target_project_id, undefined);
  assert.deepEqual(createWatcher.filter_config, {
    events: ["workflow_run"],
    conclusions: ["success", "failure"],
  });

  const fixedValues = triggerFormSchema.parse({
    ...base,
    name: "Alert card",
    provider: "generic",
    auth_mode: "bearer",
    routing_mode: "fixed_card",
    target_card_id: "card-99",
    json_matches_text: "severity=critical",
    match_key_path: "meta.key",
  });
  const createFixed = toCreateTriggerInput(fixedValues);
  assert.equal(createFixed.auth_mode, "bearer");
  assert.equal(createFixed.target_card_id, "card-99");
  assert.equal(createFixed.target_project_id, undefined);
  assert.deepEqual(createFixed.filter_config, {
    json_matches: { severity: "critical" },
    match_key_path: "meta.key",
  });

  const updateTodo = toUpdateTriggerInput(
    triggerFormSchema.parse({
      ...base,
      name: "Auto Todo",
      routing_mode: "create_todo",
      target_project_id: "proj-2",
    }),
  );
  assert.equal(updateTodo.target_project_id, "proj-2");
  assert.equal(updateTodo.target_card_id, "");
});

test("触发器表单拒绝缺少必填目标、短密钥或无效冷却时间", () => {
  const base = emptyTriggerFormValues("");
  for (const patch of [
    { name: "   " },
    { name: "x", routing_mode: "fixed_card", target_card_id: "" },
    { name: "x", routing_mode: "create_todo", target_project_id: "" },
    { name: "x", secret: "too-short" },
    { name: "x", cooldown_seconds: -1 },
    { name: "x", cooldown_seconds: 86401 },
    { name: "x", json_matches_text: "badline" },
  ]) {
    assert.equal(
      triggerFormSchema.safeParse({ ...base, ...patch }).success,
      false,
    );
  }
});

test("触发器回填表单、卡片跳转路由与倒计时格式化", () => {
  const values = triggerToFormValues(
    {
      id: "t-1",
      name: "CI Hook",
      provider: "circleci",
      auth_mode: "circleci_hmac_sha256",
      signature_header: "circleci-signature",
      secret_hint: "abcd...ef12",
      webhook_path: "/api/v1/webhooks/t-1",
      enabled: true,
      routing_mode: "fixed_card",
      target_card_id: "card-1",
      target_card_project_id: "proj-1",
      dispatch_mode: "interrupt",
      filter_config: { branches: ["main"], json_matches: { a: "1" } },
      cooldown_seconds: 15,
      created_at: "2026-11-21T00:00:00Z",
      updated_at: "2026-11-21T00:00:00Z",
    },
    "fallback-proj",
  );
  assert.equal(values.target_project_id, "proj-1");
  assert.equal(values.branches_text, "main");
  assert.equal(values.json_matches_text, "a=1");

  assert.equal(getCardRoute("p1", "c1", "discussion"), "/chat/p1/c1");
  assert.equal(getCardRoute("p1", "c1", "ongoing"), "/plans/p1/c1");
  assert.equal(getCardRoute("p1", "c1", "todo"), "/cards/p1/c1");

  const now = Date.parse("2026-11-21T10:00:00Z");
  assert.equal(
    formatRemainingCountdown("2026-11-21T10:25:00Z", now, false),
    "25 分钟后",
  );
  assert.equal(
    formatRemainingCountdown("2026-11-21T11:30:00Z", now, true),
    "in 1h 30m",
  );
  assert.equal(
    formatRemainingCountdown("2026-11-21T09:59:00Z", now, false),
    "即将触发",
  );
});

test("高级选项折叠判断：极简默认触发器默认折叠，含自定义配置时自动展开", () => {
  const minimalTrigger = {
    id: "t-min",
    name: "GitHub Webhook",
    provider: "github",
    auth_mode: "github_hmac_sha256",
    signature_header: "X-Hub-Signature-256",
    secret_hint: "abcd...ef12",
    webhook_path: "/api/v1/triggers/t-min/webhook",
    enabled: true,
    routing_mode: "match_watchers",
    dispatch_mode: "enqueue",
    filter_config: {},
    cooldown_seconds: 0,
    created_at: "2026-11-21T00:00:00Z",
    updated_at: "2026-11-21T00:00:00Z",
  };
  assert.equal(hasAdvancedTriggerConfig(minimalTrigger), false);

  assert.equal(
    hasAdvancedTriggerConfig({
      ...minimalTrigger,
      routing_mode: "create_todo",
    }),
    true,
  );
  assert.equal(
    hasAdvancedTriggerConfig({
      ...minimalTrigger,
      dispatch_mode: "interrupt",
    }),
    true,
  );
  assert.equal(
    hasAdvancedTriggerConfig({
      ...minimalTrigger,
      cooldown_seconds: 60,
    }),
    true,
  );
  assert.equal(
    hasAdvancedTriggerConfig({
      ...minimalTrigger,
      filter_config: { repositories: ["uvwt/CycleDock"] },
    }),
    true,
  );
  assert.equal(
    hasAdvancedTriggerConfig({
      ...minimalTrigger,
      provider: "generic",
      auth_mode: "bearer",
    }),
    true,
  );
});
