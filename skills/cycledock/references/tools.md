# CycleDock MCP tool reference

## Read

### list_projects
Lists Projects in the authenticated Workspace. Project is the board.

### list_cards
Lists Cards with Project state, Ongoing health, `current_attempt_status` and `pending_turn_count`.

### get_card
Returns one Card plus durable Timeline, `pending_turns`, `current_attempt` and `attempt_history`. Raw model/tool traces and chain-of-thought are not product Timeline data.

## Card writes

### create_discussion
Creates a new Discussion in a Project. Requires stable `idempotency_key`.

### generate_work
Creates one Todo or Ongoing Card from an active Discussion. `kind` is the agent's semantic classification. Todo/Ongoing cannot generate more Cards. Requires stable `idempotency_key`.

### update_card
Updates title and/or description only.

### move_card
For agents, Todo targets are `todo` and `working`. Review requires `confirm_card`; Done requires a human.

### add_comment
Adds durable context only. It does not create a Turn.

### create_turn
Creates a durable Comment Event + queued Turn on the same Card. It does not interrupt current work or silently claim. Requires stable `idempotency_key`.

## Attempt lifecycle

### claim_attempt
Card-scoped atomic claim. Starts the oldest pending Turn for that Card. Retry by the same OAuth Source reuses its current Attempt; another Source receives conflict.

### heartbeat_attempt
Extends the lease of a running Attempt owned by this Source. No Timeline Event is written.

### reply_message
Publishes one user-visible bubble for the owned Attempt. Required: `attempt_id`, `type` (`text`, `attachment`, `choice`), `idempotency_key`, and `end_turn`. Text requires `content`; attachment uses HTTPS `url` with optional `alt`; text/attachment may include `images` (HTTPS URLs); choice requires `widget.question` and 2–8 `widget.options` of `{value,label}`, plus `end_turn=true`. Every call is a distinct durable bubble. A terminal text or attachment ends the Attempt; terminal choice enters `waiting_human` and resumes after Human reply.

### check_in
Writes healthy / warning / critical for an Ongoing Card through the owned current Attempt. Requires `idempotency_key`.

### finish_attempt
Outcomes:

- `completed`: legacy completion when no final reply bubble was published.
- `failed`: failure summary.
- `waiting_human`: stage + summary, then stop and wait.

Repeating the same owned lifecycle transition is idempotent. Superseded, terminal, or foreign-owned Attempts reject further writes.

## Review

### confirm_card
Confirms a Working Todo Card. Requires `idempotency_key`. When required confirmations are reached, CycleDock moves it to Review. Review → Done remains Human-only.

## Removed legacy tools

The pre-release Trigger/Run protocol is not supported. Do not use:

- `create_followup`
- `claim_work`
- `heartbeat_run`
- `finish_run`
