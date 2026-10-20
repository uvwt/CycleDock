# Ongoing Schedule

## 1. 目标

Ongoing Card 可以原生配置轻量周期计划。

Schedule 的唯一职责：

> 到时间后创建一个新的 scheduled_followup Trigger。

Schedule 本身不执行 Agent，也不拥有另一套 Job Runtime。

## 2. 第一版周期

第一版只支持：

- 每天固定时间。
- 每周固定星期 + 时间。
- 每月固定日期 + 时间。

所有计划必须有明确 timezone，并使用 IANA 名称，例如 `Asia/Shanghai`、`America/Los_Angeles`。

UI 不暴露 Cron 表达式。

以后真实需求出现后再考虑：

- 工作日。
- 每 N 天。
- 月末。
- 自定义 Cron。

## 3. 示例

```text
Card:
NexusDock 服务器健康巡检

Kind:
Ongoing

State:
Active

Schedule:
Daily 09:00 Asia/Shanghai
```

到时间：

```text
CardSchedule
→ scheduled_followup Trigger
→ Queue
→ AgentRun
→ Finding / Result / Check-in
```

## 4. 与当前 Run 的关系

定时 Trigger 永远默认 enqueue。

如果 09:00 到点时当前 Card 已有 Run：

```text
08:55 Run A running
09:00 Schedule fires
      ↓
      Trigger B pending
```

不能自动 supersede Run A。

Run A 完成后，由 Dispatcher 正常 Claim Trigger B。

## 5. Paused / Archived

只有 Active Ongoing Card 允许周期触发。

- Active：Schedule 正常推进。
- Paused：不生成新 scheduled Trigger。
- Archived：不生成新 scheduled Trigger。

重新 Active 时重新计算下一次未来触发时间，不补跑暂停期间的历史周期。

## 6. 错过的周期

第一版固定：

`missed_run_policy = skip`

例如系统离线三天，不补跑三次历史巡检。

恢复后：

- 计算最近合理的未来触发时间。
- 如果产品实现选择“恢复后补一次当前检查”，也只能生成一条当前 Trigger，不能按历史每个周期补跑；该行为在真正实现前需要明确测试和文档。

默认推荐直接 skip 到下一次未来周期。

## 7. 去重

Scheduler 可能因为进程重启或并发扫描重复处理同一计划。

Trigger 必须携带：

- schedule_id
- scheduled_for

并由数据库唯一约束保证同一 schedule occurrence 只能创建一个 Trigger。

不能只依赖“扫描前先查询有没有”。

### DST 语义

Schedule 保持“本地 wall-clock 时间”而不是固定 UTC offset：

- Spring-forward 导致目标本地时间不存在时，跳过该日/周/月 occurrence。
- Fall-back 导致同一本地时间出现两次时，只使用第一次 occurrence。
- 因此一个 calendar occurrence 最多生成一条 Trigger，不会因为回拨重复执行。

例如 `America/Los_Angeles`：

- `Daily 02:30` 在 2026-03-08 不存在，因此下一次是 2026-03-09 02:30。
- `Daily 01:30` 在 2026-11-01 出现两次，只触发第一次 01:30；当天第二次 01:30 不再次执行。

## 8. Scheduler Loop

第一版可由 Go 服务中的轻量 loop 实现：

1. 查询 `next_trigger_at <= now()` 且 enabled 的 Schedule。
2. PostgreSQL transaction 中锁定 Schedule。
3. 验证 Card 仍为 Active Ongoing。
4. 创建 scheduled_followup Trigger。
5. 计算下一次 next_trigger_at。
6. 更新 last_triggered_at / next_trigger_at。
7. commit。
8. 通知 Dispatcher。

不要在 Scheduler 里直接调用模型。

## 9. Check-in

Ongoing Run 完成时通常输出 Check-in：

```text
healthy
warning
critical
```

Card 当前 Health = 最新 Check-in。

例如：

```text
Healthy
最近检查：今天 09:04
下次检查：明天 09:00
```

不单独维护可写的 card.health。

## 10. 外部 Scheduler

CycleDock 原生 Schedule 不排斥外部 Scheduler。

ChatGPT Automations、AgentDock、Cron、GitHub Actions 等外部系统仍可以通过 MCP 创建/Claim 工作。

无论内部还是外部调度，最终都必须进入同一模型：

```text
Trigger → Claim → Run
```
