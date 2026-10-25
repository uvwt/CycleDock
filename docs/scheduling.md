# Ongoing Schedule

## 1. 目标

Ongoing Card 可以原生配置轻量周期计划。

Schedule 的唯一职责：

> 到时间后在同一个 Card Session 中创建一个新的 Schedule Turn。

Schedule 本身不直接执行模型，也不拥有另一套 Job Runtime。

## 2. 第一版周期

第一版只支持：

- 每天固定时间。
- 每周固定星期 + 时间。
- 每月固定日期 + 时间。

所有计划必须有明确 timezone，并使用 IANA 名称，例如 `Asia/Shanghai`、`America/Los_Angeles`。

UI 不暴露 Cron 表达式。

## 3. 执行模型

例如：

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

到时间后：

```text
CardSchedule
→ Schedule Turn (enqueue)
→ pending_turns
→ Claim
→ Attempt
→ Finding / Result / Check-in
→ same Card Session
```

Schedule Turn 不提前进入 ADK Session。只有真正 Claim 后，当前 Turn 才成为这次 invocation 的输入。

## 4. 与当前 Attempt 的关系

定时 Turn 固定为 `enqueue`。

如果 09:00 到点时当前 Card 已有 running Attempt：

```text
08:55 Attempt A running
09:00 Schedule fires
      ↓
      Schedule Turn B pending
```

不能自动 supersede Attempt A。

Attempt A 结束后，Built-in Dispatcher 或 External Agent 再正常 Claim Turn B，形成新的 Attempt。

## 5. Paused / Archived

只有 Active Ongoing Card 允许周期触发：

- Active：Schedule 正常推进。
- Paused：不创建新 Schedule Turn。
- Archived：不创建新 Schedule Turn。

重新 Active 时重新计算下一次未来 occurrence，不补跑暂停期间的历史周期。

## 6. 错过的周期

第一版固定：

`missed_run_policy = skip`

例如系统离线三天，不补跑三次历史巡检。恢复后直接计算下一次合理的未来 occurrence。

如果未来产品决定“恢复后立即补一次当前检查”，也只能创建一条当前 Schedule Turn，不能按所有历史周期逐条补跑。

## 7. 去重

Scheduler 可能因为进程重启或并发扫描重复处理同一计划。

每个 Schedule Turn 携带：

- `schedule_id`
- `scheduled_for`

数据库唯一约束 `card_turns(schedule_id, scheduled_for)` 保证同一 occurrence 最多创建一个 Turn。

不能依赖“先查询有没有，再插入”的应用层检查。

### DST 语义

Schedule 保持“本地 wall-clock 时间”而不是固定 UTC offset：

- Spring-forward 导致目标本地时间不存在时，跳过该日/周/月 occurrence。
- Fall-back 导致同一本地时间出现两次时，只使用第一次 occurrence。
- 一个 calendar occurrence 最多生成一条 Turn，不会因为回拨重复执行。

例如 `America/Los_Angeles`：

- `Daily 02:30` 在 2026-03-08 不存在，因此下一次是 2026-03-09 02:30。
- `Daily 01:30` 在 2026-11-01 出现两次，只使用第一次 01:30。

## 8. Scheduler Loop

Go 服务中的轻量 loop：

1. 查询 `next_trigger_at <= now()` 且 enabled 的 Schedule。
2. PostgreSQL transaction 中锁定 Schedule。
3. 验证 Card 仍为 Active Ongoing。
4. 创建 Schedule Turn。
5. 计算下一次 `next_trigger_at`。
6. 更新 `last_triggered_at / next_trigger_at`。
7. commit。
8. 通知 Dispatcher。

Scheduler 不直接调用模型。

## 9. Check-in

Ongoing Attempt 完成时通常输出 Check-in：

```text
healthy
warning
critical
```

Card 当前 Health = 最新 Check-in，不单独维护可写的 `card.health`。

## 10. 删除 Schedule

已经产生 Turn 历史的 Schedule 不能硬删除，因为 Schedule Turn 是审计事实；应将 Schedule 设为 disabled。

尚无 Turn 历史的 Schedule 可以删除。

数据库使用 RESTRICT 保护 Schedule → Turn 历史关系，应用层也在删除前检查历史。

## 11. 外部 Scheduler / Agent

CycleDock 原生 Schedule 不排斥 ChatGPT Automations、AgentDock、Cron、GitHub Actions 等外部系统。

无论内部还是外部调度，真正执行都进入同一模型：

```text
Turn → Claim → Attempt → Card Session
```

External MCP Agent 使用 `get_card` 查看 `pending_turns/current_attempt`，再调用 `claim_attempt(card_id)`，不能建立第二套调度状态机。
