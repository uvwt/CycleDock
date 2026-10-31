---
name: cycledock
description: Use when working through CycleDock MCP to inspect Projects/Cards, start Discussions, generate Todo/Ongoing work, create or claim Turns, operate Attempts, record durable progress/findings/results/check-ins, confirm completed Todo cards, or coordinate recurring Ongoing work. Do not bypass Human Review/Done or store raw reasoning/tool noise as product facts.
---

# CycleDock

CycleDock is shared durable work state for humans and AI agents.

The core model is:

`Workspace → Project → Card → Turn → Attempt`

A Card is the long-lived business container and persistent Agent Session boundary. A Turn is one new work direction. An Attempt is one real execution of a Turn. A Project is the board.

## Start every work cycle by reading reality

1. Call `list_projects`.
2. Call `list_cards`.
3. Before acting on a Card, call `get_card` and read its state, Timeline, `pending_turns`, `current_attempt` and `attempt_history`.
4. Never assume a remembered state is still current.

## Existing queued work

When a Card has a pending Turn, use `claim_attempt`.

- Claim is Card-scoped and atomic.
- If another Source owns the current Attempt, do not work around it.
- If your own claim response was lost, retry `claim_attempt`; CycleDock returns your existing current Attempt.
- Do not create a duplicate Turn merely because you are unsure whether a claim succeeded.

## Starting and generating work

New work starts with `create_discussion`.

When a Discussion is concrete enough, use `generate_work`:

- finite work with a completion point → `todo`
- long-term, periodic, recurring, or continuously monitored work → `ongoing`

A Discussion may generate multiple work Cards. Never call `generate_work` from Todo or Ongoing.

Use `create_turn` when a new direction should enter the queue of the same existing Card. Use `add_comment` only for durable context that should not create work.

External MCP deliberately has no interrupt tool. Human Web owns “interrupt now”.

## While an Attempt is active

- Use `heartbeat_attempt` only to extend the lease.
- Use `reply_message` for every user-visible bubble. One invocation creates one message, and you may invoke it repeatedly.
- Set `end_turn=false` to continue working; use `end_turn=true` for the last text/attachment bubble, or with `type=choice` and `widget` to wait for a human choice.
- On Ongoing Cards, use `check_in` for healthy / warning / critical.
- Every execution write must use the current `attempt_id`; foreign or stale Attempts reject writes.
- Reuse the same `idempotency_key` when retrying the same logical Event write.

## Finishing an Attempt

Normally finish with `reply_message(end_turn=true)`, which completes the Attempt after publishing the final bubble. If human input is needed, use `type=choice` and `widget.question / widget.options` instead; the Attempt enters `waiting_human` automatically.

Use `finish_attempt` for exceptional lifecycle handling:

- `completed`: status-only completion without a message; publish user-visible content through `reply_message`, never through a result field.
- `failed`: include a concise failure summary.
- `waiting_human`: include stage and exactly what human input is required, then stop.

Lifecycle retries are safe: repeating the same terminal value or waiting_human state for the same owned Attempt is idempotent.

When a human replies to waiting_human, CycleDock supersedes the old Attempt and creates a new Human Turn in the same Card Session. Claim the new Turn with `claim_attempt`; never keep writing to the superseded Attempt.

## Review and completion

Todo lifecycle:

`Todo → Working → Review → Done`

- Agents may move Todo ↔ Working with `move_card`.
- Use `confirm_card` to hand verified Working work to Review.
- Confirmation thresholds are enforced by CycleDock.
- Only a human can move Review → Done.
- Attempt completed does not mean Card Done.

Discussion lifecycle is `Active → Archived`.

Ongoing lifecycle is `Active / Paused / Archived`. Health comes from the latest Check-in.

## Recurring work

CycleDock Ongoing schedules create Schedule Turns; they do not directly execute a model.

Use list_schedules(card_id) to inspect existing times, then create_schedule(card_id, rule) to add one or update_schedule(card_id, schedule_id, rule, enabled) to change/disable it. Use delete_schedule only before any Turn history exists; otherwise disable it. A rule contains daily/weekly/monthly frequency, local hour/minute and IANA timezone. Weekly rules require day_of_week (Sunday=0); monthly rules require day_of_month (1-28). Updates supply the complete rule and an explicit enabled value. Create is not retry-idempotent: if the reply is lost, list schedules before deciding whether to retry.

Before adding external recurring automation, inspect the Card's `pending_turns` and `current_attempt` to avoid duplicate work. External agents claim Schedule Turns with `claim_attempt` like any other pending Turn.

## Tool reference

Read [references/tools.md](references/tools.md) for exact current MCP tool boundaries.
