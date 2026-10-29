import test from "node:test";
import assert from "node:assert/strict";
import { applyCardStreamEvent as apply } from "../src/shared/realtime/cardStreamState.ts";

const event = (data = {}) => ({ attempt_id: "current", ...data });
const started = () => apply(null, "attempt.started", event());

test("正文快照覆盖临时气泡，多调用交错仍按工具顺序展示", () => {
  let live = started();
  live = apply(live, "reply.preview", event({reply_id:"second",order:2,text:"第二条"}));
  live = apply(live, "reply.preview", event({reply_id:"first",order:1,text:"第"}));
  live = apply(live, "reply.preview", event({reply_id:"first",order:1,text:"第一条"}));
  live = apply(live, "reply.preview", event({reply_id:"first",order:1,text:"第一条"}));
  assert.deepEqual(live.replies.map((reply) => reply.text), ["第一条", "第二条"]);
});

test("消息提交绑定正式事件 ID，迟到参数不能重新打开气泡", () => {
  let live = apply(started(), "reply.preview", event({reply_id:"first",text:"正文"}));
  live = apply(live, "reply.committed", event({reply_id:"first",event_id:"persisted"}));
  const committed = live;
  assert.equal(live.replies[0].eventID, "persisted");
  live = apply(live, "reply.preview", event({reply_id:"first",text:"late"}));
  assert.equal(live, committed);
});

test("路由重试只清除未提交预览，保留已提交消息的对应关系", () => {
  let live = apply(started(), "reply.preview", event({reply_id:"saved",text:"已发送"}));
  live = apply(live, "reply.committed", event({reply_id:"saved",event_id:"persisted"}));
  live = apply(live, "reply.preview", event({reply_id:"draft",text:"未发送"}));
  live = apply(live, "reply.reset", event());
  assert.deepEqual(live.replies.map((reply) => reply.id), ["saved"]);
});

test("旧 Attempt 的迟到事件不影响当前回复，终止后不再接受参数", () => {
  let live = apply(started(), "reply.preview", event({reply_id:"draft",text:"正文"}));
  assert.equal(apply(live,"attempt.failed",event({attempt_id:"old"})), live);
  live = apply(live,"attempt.failed",event());
  assert.equal(live.replies[0].failed, true);
  assert.equal(live.finished, true);
  assert.equal(apply(live,"reply.preview",event({reply_id:"draft",text:"late"})), live);
});

test("重连快照恢复多个预览和已提交事件身份", () => {
  const live = apply(null,"attempt.snapshot",event({replies:[
    {id:"a",order:0,text:"已发送",event_id:"persisted"},
    {id:"b",order:1,text:"生成中"},
  ]}));
  assert.equal(live.replies.length,2);
  assert.equal(live.replies[0].eventID,"persisted");
});
