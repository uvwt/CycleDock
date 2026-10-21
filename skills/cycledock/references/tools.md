# CycleDock MCP tool reference

## Read

### list_projects
Lists Projects in the authenticated Workspace. Project is the board.

### list_cards
Lists Cards with Project, workflow state, Ongoing health, current Run status and pending Trigger count.

### get_card
Returns one Card plus recent durable Timeline, pending Trigger queue and current Run. It intentionally excludes raw model/tool traces.

## Card writes

### create_discussion
Creates a new Discussion in a Project. A Discussion is the entry point for new work and does not start as a Todo. Requires a stable `idempotency_key`.

### generate_work
Creates one Todo or Ongoing Card from an active Discussion and records the source Discussion. `kind` is the agent's semantic classification: finite work is `todo`; long-term, periodic, recurring, or continuously monitored work is `ongoing`. Todo and Ongoing Cards cannot generate more Cards. Requires a stable `idempotency_key`.

### update_card
Updates title and/or description only.

### move_card
For external agents, this is only for Todo Cards. Valid target states are `todo` and `working`. Discussion is not a Todo state. Review requires `confirm_card`; Done requires a human.

### add_comment
Adds durable context only. It does **not** create a Trigger.

### create_followup
Creates a durable comment + new queued Trigger. It never interrupts current work and never silently claims the Trigger.

## Run lifecycle

### claim_work
Atomically claims and starts the oldest pending Trigger for a Card. Retry by the same Source returns its existing current Run.

### heartbeat_run
Extends the lease of a running Run owned by this Source. No Timeline Event is written.

### report_progress
Updates stage/progress summary and refreshes the lease. Optional `event_content` is for meaningful durable progress only; if supplied, `idempotency_key` is required.

### add_finding
Writes a durable Finding attached to the current Run. Requires `idempotency_key`.

### check_in
Writes healthy / warning / critical for an Ongoing Card through the current Run. Requires `idempotency_key`.

### finish_run
Outcomes:
- `completed`: durable Result + `idempotency_key`.
- `failed`: failure summary + `idempotency_key`.
- `waiting_human`: stage + summary; then stop and wait for human input.

A superseded or foreign-owned Run rejects further writes.

## Review

### confirm_card
Confirms a Working Todo Card. Requires `idempotency_key`. When the Card reaches its required confirmation count, CycleDock moves it to Review. Review → Done remains Human-only.
