---
name: cycledock
description: Use when working through CycleDock MCP to inspect Projects/Cards, claim queued work, record durable progress/findings/results/check-ins, create explicit follow-ups, confirm completed Normal cards, or coordinate recurring Ongoing work. Do not use it to store raw model/tool traces or bypass Human Review/Done.
---

# CycleDock

CycleDock is shared durable work state for humans and AI agents.

The core model is:

`Workspace → Project → Card → Event / Trigger → AgentRun`

A Project is the board. There is no separate Board object.

## Operating model

- **Card retains knowledge; Run retains execution state.**
- A **Trigger** is one immutable work direction and is also the queue item.
- A Trigger with no Run is pending work.
- Claiming work creates a Run. At most one current Run may exist for a Card.
- Durable Card context includes comments, findings, results, check-ins, meaningful progress and status changes.
- Do not put chain-of-thought, raw tool arguments/results, temporary commands or noisy heartbeat logs into CycleDock.

## Start every work cycle by reading reality

1. Call `list_projects`.
2. Call `list_cards`.
3. Before acting on a Card, call `get_card` and read its current state, recent Timeline, pending queue and current Run.
4. Never infer that a Card is still in the state you remember from an earlier turn.

## Existing queued work

When a Card already has a pending Trigger, use `claim_work`.

- Claim is atomic.
- If another Source already owns the current Run, do not work around it.
- If your own previous claim response was lost, retrying `claim_work` returns your existing current Run.
- Do not create a duplicate follow-up merely because you are unsure whether a claim succeeded.

## Creating a new work direction

Use `create_followup` only when a **new unit of work** should enter the queue.

Use `add_comment` when you only need to append durable context and **do not** want to create new work.

External MCP deliberately has no interrupt tool. Do not simulate an interrupt by creating extra Triggers or editing Run state. Human Web owns the “interrupt now” decision.

## While a Run is active

- Use `heartbeat_run` only to extend the lease. It does not write Timeline.
- Use `report_progress` for the current stage and a concise meaningful progress summary.
- Set `event_content` only when that progress is itself a durable fact worth keeping.
- Use `add_finding` for discoveries future Runs need to know.
- On an Ongoing Card, use `check_in` for the latest healthy / warning / critical fact.
- Reuse the same `idempotency_key` when retrying the **same logical write**. Use a new key for a new logical action.

## Finishing a Run

Use `finish_run`:

- `completed`: include the durable Result and an idempotency key.
- `failed`: include a concise failure summary and an idempotency key.
- `waiting_human`: include the current stage and exactly what human input is needed, then stop work on that Run.

When a human replies to a waiting Run, CycleDock creates a new Trigger / Run and supersedes the old waiting Run. Never continue writing to the superseded Run.

## Review and completion

For a Normal Card:

`Discussion → Todo → Working → Review → Done`

- Agents may move among Discussion, Todo and Working with `move_card`.
- Agents must use `confirm_card` to hand verified Working work to Review.
- Confirmation thresholds are enforced by CycleDock.
- **Only a human can move Review → Done.**
- Do not report a Card as Done merely because a Run completed or because it reached Review.

For Ongoing Cards:

`Active / Paused / Archived`

Health is derived from the latest Check-in; it is not a separately writable field.

## Recurring work

CycleDock can schedule Ongoing Cards daily, weekly or monthly. A schedule creates a `scheduled_followup` Trigger; it does not itself execute a model.

Before creating an external client-side recurring automation, inspect the Card and its pending queue so you do not duplicate an existing CycleDock schedule or pending Trigger.

## Tool reference

Read [references/tools.md](references/tools.md) for the exact purpose and boundary of every current MCP tool.
