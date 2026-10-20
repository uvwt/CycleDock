import * as Dialog from '@radix-ui/react-dialog'
import { useMutation, useQuery } from '@tanstack/react-query'
import { formatDistanceToNow } from 'date-fns'
import {
  Activity,
  CalendarClock,
  CircleAlert,
  Clock3,
  CornerDownRight,
  ListTodo,
  LoaderCircle,
  MessageSquareText,
  Pause,
  Play,
  Save,
  Send,
  Trash2,
  X,
  Zap,
} from 'lucide-react'
import { useState } from 'react'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { toast } from 'sonner'
import {
  api,
  type CardDetail,
  type NormalColumn,
  type OngoingState,
  type Schedule,
  type ScheduleInput,
} from '../api'
import { queryClient, queryKeys } from '../query'

const normalColumns: Array<{ id: NormalColumn; label: string }> = [
  { id: 'discussion', label: 'Discussion' },
  { id: 'todo', label: 'Todo' },
  { id: 'working', label: 'Working' },
  { id: 'review', label: 'Review' },
  { id: 'done', label: 'Done' },
]

export function CardDetailDialog({
  projectID,
  cardID,
  onClose,
}: {
  projectID: string
  cardID: string
  onClose: () => void
}) {
  const detail = useQuery({
    queryKey: queryKeys.card(cardID),
    queryFn: () => api.card(cardID),
    refetchInterval: 15_000,
  })

  async function refresh() {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: queryKeys.card(cardID) }),
      queryClient.invalidateQueries({ queryKey: queryKeys.cards(projectID) }),
    ])
  }

  return (
    <Dialog.Root open onOpenChange={(open) => !open && onClose()}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-40 bg-slate-950/80 backdrop-blur-sm" />
        <Dialog.Content className="fixed inset-x-3 bottom-3 top-3 z-50 mx-auto flex max-w-6xl flex-col overflow-hidden rounded-3xl border border-slate-800 bg-slate-950 text-slate-100 shadow-2xl outline-none md:inset-x-8">
          {detail.isPending ? (
            <div className="grid flex-1 place-items-center">
              <LoaderCircle className="size-6 animate-spin text-cyan-300" />
            </div>
          ) : detail.isError || !detail.data ? (
            <div className="grid flex-1 place-items-center px-8 text-center">
              <div>
                <CircleAlert className="mx-auto size-8 text-rose-300" />
                <p className="mt-4 text-sm text-rose-200">
                  {detail.error instanceof Error ? detail.error.message : 'Could not load this card.'}
                </p>
              </div>
            </div>
          ) : (
            <CardDetailContent key={detail.data.card.id} detail={detail.data} refresh={refresh} />
          )}
          <Dialog.Close className="absolute right-5 top-5 grid size-9 place-items-center rounded-xl border border-slate-800 bg-slate-900 text-slate-400 transition hover:text-white">
            <X className="size-4" />
            <span className="sr-only">Close</span>
          </Dialog.Close>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  )
}

function CardDetailContent({
  detail,
  refresh,
}: {
  detail: CardDetail
  refresh: () => Promise<void>
}) {
  const card = detail.card
  const [title, setTitle] = useState(card.title)
  const [description, setDescription] = useState(card.description)
  const [comment, setComment] = useState('')

  const edit = useMutation({
    mutationFn: () => api.updateCard(card.id, { title: title.trim(), description: description.trim() }),
    onSuccess: async () => {
      toast.success('Card updated')
      await refresh()
    },
    onError: showError,
  })

  const move = useMutation({
    mutationFn: (column: NormalColumn) => api.moveCard(card.id, column),
    onSuccess: refresh,
    onError: showError,
  })

  const ongoingState = useMutation({
    mutationFn: (state: OngoingState) => api.setOngoingState(card.id, state),
    onSuccess: refresh,
    onError: showError,
  })

  const submitDirection = useMutation({
    mutationFn: (mode: 'enqueue' | 'interrupt' | 'reply') =>
      mode === 'reply' ? api.reply(card.id, comment.trim()) : api.comment(card.id, comment.trim(), mode),
    onSuccess: async () => {
      setComment('')
      await refresh()
    },
    onError: showError,
  })

  const waitingHuman = detail.current_run?.status === 'waiting_human'
  const hasCurrentRun = Boolean(detail.current_run)

  return (
    <>
      <header className="border-b border-slate-800 px-6 py-5 pr-20">
        <div className="flex flex-wrap items-center gap-2 text-xs text-slate-500">
          <span className="uppercase tracking-[0.12em]">{card.kind}</span>
          <span>•</span>
          {card.kind === 'normal' ? (
            <span className="capitalize">{card.normal_column}</span>
          ) : (
            <>
              <span className="capitalize">{card.ongoing_state}</span>
              <span>•</span>
              <HealthPill health={detail.health} />
            </>
          )}
          {detail.current_run ? (
            <>
              <span>•</span>
              <span className="text-cyan-300">{detail.current_run.status.replace('_', ' ')}</span>
            </>
          ) : null}
        </div>
        <Dialog.Title className="mt-2 text-xl font-semibold tracking-tight">{card.title}</Dialog.Title>
        <Dialog.Description className="mt-1 text-sm text-slate-500">
          Durable card context and meaningful work history. Raw agent traces are intentionally not stored here.
        </Dialog.Description>
      </header>

      <div className="grid min-h-0 flex-1 lg:grid-cols-[minmax(0,1fr)_360px]">
        <div className="min-h-0 overflow-y-auto px-6 py-6">
          <section className="rounded-2xl border border-slate-800 bg-slate-900/50 p-4">
            <div className="mb-3 flex items-center justify-between">
              <h3 className="text-xs font-semibold uppercase tracking-[0.1em] text-slate-400">Card</h3>
              <button
                type="button"
                onClick={() => edit.mutate()}
                disabled={edit.isPending || !title.trim()}
                className="flex items-center gap-1.5 rounded-lg border border-slate-700 px-2.5 py-1.5 text-xs text-slate-300 transition hover:border-slate-600 disabled:opacity-50"
              >
                <Save className="size-3.5" /> Save
              </button>
            </div>
            <input
              value={title}
              onChange={(event) => setTitle(event.target.value)}
              className="w-full bg-transparent text-lg font-medium outline-none placeholder:text-slate-700"
              placeholder="Card title"
            />
            <textarea
              value={description}
              onChange={(event) => setDescription(event.target.value)}
              rows={3}
              className="mt-3 w-full resize-none rounded-xl border border-slate-800 bg-slate-950/60 px-3 py-2 text-sm leading-6 text-slate-300 outline-none focus:border-cyan-500/60"
              placeholder="Describe the durable task context…"
            />

            {card.kind === 'normal' ? (
              <div className="mt-4 flex flex-wrap gap-2">
                {normalColumns.map((column) => {
                  const disabled =
                    column.id === card.normal_column ||
                    (column.id === 'done' && card.normal_column !== 'review')
                  return (
                    <button
                      key={column.id}
                      type="button"
                      disabled={disabled || move.isPending}
                      onClick={() => move.mutate(column.id)}
                      className={`rounded-lg border px-2.5 py-1.5 text-xs transition ${
                        column.id === card.normal_column
                          ? 'border-cyan-500/40 bg-cyan-400/10 text-cyan-300'
                          : 'border-slate-800 text-slate-400 hover:border-slate-700 hover:text-slate-200 disabled:opacity-35'
                      }`}
                    >
                      {column.label}
                    </button>
                  )
                })}
              </div>
            ) : (
              <div className="mt-4 flex flex-wrap gap-2">
                {(['active', 'paused', 'archived'] as OngoingState[]).map((state) => (
                  <button
                    key={state}
                    type="button"
                    disabled={state === card.ongoing_state || ongoingState.isPending}
                    onClick={() => ongoingState.mutate(state)}
                    className={`flex items-center gap-1.5 rounded-lg border px-2.5 py-1.5 text-xs capitalize transition ${
                      state === card.ongoing_state
                        ? 'border-cyan-500/40 bg-cyan-400/10 text-cyan-300'
                        : 'border-slate-800 text-slate-400 hover:border-slate-700 hover:text-slate-200'
                    }`}
                  >
                    {state === 'active' ? <Play className="size-3" /> : state === 'paused' ? <Pause className="size-3" /> : null}
                    {state}
                  </button>
                ))}
              </div>
            )}
          </section>

          <section className="mt-5">
            <div className="mb-3 flex items-center gap-2">
              <MessageSquareText className="size-4 text-slate-500" />
              <h3 className="text-sm font-semibold">Timeline</h3>
            </div>
            {detail.timeline.length === 0 ? (
              <div className="rounded-2xl border border-dashed border-slate-800 px-4 py-8 text-center text-sm text-slate-600">
                No meaningful events yet.
              </div>
            ) : (
              <div className="space-y-3">
                {detail.timeline.map((event) => (
                  <article key={event.id} className="rounded-2xl border border-slate-800 bg-slate-900/40 p-4">
                    <div className="flex items-center justify-between gap-3 text-[11px] text-slate-500">
                      <div className="flex items-center gap-2">
                        <span className="font-medium text-slate-400">{event.actor_name}</span>
                        <span>•</span>
                        <span className="uppercase tracking-[0.08em]">{event.type.replace('_', ' ')}</span>
                        {event.checkin_status ? <HealthPill health={event.checkin_status} /> : null}
                      </div>
                      <time title={new Date(event.created_at).toLocaleString()}>
                        {formatDistanceToNow(new Date(event.created_at), { addSuffix: true })}
                      </time>
                    </div>
                    <div className="prose prose-invert prose-sm mt-3 max-w-none text-sm leading-6 text-slate-300 prose-p:my-1 prose-pre:bg-slate-950">
                      <ReactMarkdown remarkPlugins={[remarkGfm]}>{event.content}</ReactMarkdown>
                    </div>
                  </article>
                ))}
              </div>
            )}
          </section>
        </div>

        <aside className="min-h-0 overflow-y-auto border-t border-slate-800 bg-slate-900/35 px-5 py-5 lg:border-l lg:border-t-0">
          {card.kind === 'ongoing' ? (
            <section className="rounded-2xl border border-slate-800 bg-slate-950/50 p-4">
              <div className="flex items-center justify-between">
                <span className="text-xs font-semibold uppercase tracking-[0.08em] text-slate-400">Cycle health</span>
                <HealthPill health={detail.health} />
              </div>
              <p className="mt-3 text-xs text-slate-500">
                Last check-in:{' '}
                {detail.last_checkin
                  ? formatDistanceToNow(new Date(detail.last_checkin.created_at), { addSuffix: true })
                  : 'Never'}
              </p>
              <p className="mt-1 text-xs text-slate-500">
                Next run:{' '}
                {detail.schedules.filter((item) => item.enabled).length > 0
                  ? new Date(
                      [...detail.schedules]
                        .filter((item) => item.enabled)
                        .sort((a, b) => a.next_trigger_at.localeCompare(b.next_trigger_at))[0].next_trigger_at,
                    ).toLocaleString()
                  : 'No enabled schedule'}
              </p>
            </section>
          ) : null}

          {detail.current_run ? (
            <section className={`rounded-2xl border border-cyan-900/60 bg-cyan-950/20 p-4 ${card.kind === 'ongoing' ? 'mt-4' : ''}`}>
              <div className="flex items-center gap-2 text-xs font-semibold uppercase tracking-[0.08em] text-cyan-300">
                <Activity className="size-4" /> Current run
              </div>
              <p className="mt-3 text-sm font-medium capitalize text-slate-200">{detail.current_run.status.replace('_', ' ')}</p>
              {detail.current_run.current_stage ? <p className="mt-1 text-xs text-slate-400">{detail.current_run.current_stage}</p> : null}
              {detail.current_run.progress_summary ? (
                <p className="mt-3 rounded-xl bg-slate-950/60 px-3 py-2 text-xs leading-5 text-slate-400">
                  {detail.current_run.progress_summary}
                </p>
              ) : null}
            </section>
          ) : null}

          <section className="mt-4 rounded-2xl border border-slate-800 bg-slate-950/50 p-4">
            <div className="flex items-center gap-2 text-xs font-semibold uppercase tracking-[0.08em] text-slate-400">
              <Send className="size-3.5" />
              {waitingHuman ? 'Reply and continue' : hasCurrentRun ? 'New direction' : 'Add direction'}
            </div>
            <textarea
              value={comment}
              onChange={(event) => setComment(event.target.value)}
              rows={4}
              className="mt-3 w-full resize-none rounded-xl border border-slate-800 bg-slate-900 px-3 py-2 text-sm leading-6 text-slate-200 outline-none focus:border-cyan-500/60"
              placeholder={waitingHuman ? 'Reply with the information the agent needs…' : 'Add context or a new direction…'}
            />
            {waitingHuman ? (
              <button
                type="button"
                disabled={!comment.trim() || submitDirection.isPending}
                onClick={() => submitDirection.mutate('reply')}
                className="mt-3 flex h-9 w-full items-center justify-center gap-2 rounded-lg bg-cyan-400 text-xs font-semibold text-slate-950 disabled:opacity-40"
              >
                <CornerDownRight className="size-3.5" /> Reply and continue
              </button>
            ) : hasCurrentRun ? (
              <div className="mt-3 grid grid-cols-2 gap-2">
                <button
                  type="button"
                  disabled={!comment.trim() || submitDirection.isPending}
                  onClick={() => submitDirection.mutate('enqueue')}
                  className="h-9 rounded-lg border border-slate-700 text-xs font-medium text-slate-300 disabled:opacity-40"
                >
                  Add to queue
                </button>
                <button
                  type="button"
                  disabled={!comment.trim() || submitDirection.isPending}
                  onClick={() => submitDirection.mutate('interrupt')}
                  className="flex h-9 items-center justify-center gap-1.5 rounded-lg bg-amber-400 text-xs font-semibold text-slate-950 disabled:opacity-40"
                >
                  <Zap className="size-3.5" /> Interrupt now
                </button>
              </div>
            ) : (
              <button
                type="button"
                disabled={!comment.trim() || submitDirection.isPending}
                onClick={() => submitDirection.mutate('enqueue')}
                className="mt-3 flex h-9 w-full items-center justify-center gap-2 rounded-lg bg-cyan-400 text-xs font-semibold text-slate-950 disabled:opacity-40"
              >
                <Send className="size-3.5" /> Add direction
              </button>
            )}
          </section>

          <section className="mt-4 rounded-2xl border border-slate-800 bg-slate-950/50 p-4">
            <div className="mb-3 flex items-center justify-between">
              <div className="flex items-center gap-2 text-xs font-semibold uppercase tracking-[0.08em] text-slate-400">
                <ListTodo className="size-3.5" /> Queue
              </div>
              <span className="text-[11px] text-slate-600">{detail.queue.length}</span>
            </div>
            {detail.queue.length === 0 ? (
              <p className="text-xs leading-5 text-slate-600">No pending Trigger. A Trigger without a Run is the queue item.</p>
            ) : (
              <div className="space-y-2">
                {detail.queue.map((item) => (
                  <div key={item.id} className="rounded-xl border border-slate-800 px-3 py-2">
                    <p className="line-clamp-2 text-xs leading-5 text-slate-300">{item.content || item.cause.replace('_', ' ')}</p>
                    <p className="mt-1 text-[10px] text-slate-600">#{item.sequence} · {item.dispatch_mode}</p>
                  </div>
                ))}
              </div>
            )}
          </section>

          {card.kind === 'ongoing' ? (
            <SchedulePanel cardID={card.id} schedules={detail.schedules} refresh={refresh} />
          ) : null}
        </aside>
      </div>
    </>
  )
}

function SchedulePanel({
  cardID,
  schedules,
  refresh,
}: {
  cardID: string
  schedules: Schedule[]
  refresh: () => Promise<void>
}) {
  const timezone = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
  const [frequency, setFrequency] = useState<ScheduleInput['frequency']>('daily')
  const [timeValue, setTimeValue] = useState('09:00')
  const [dayValue, setDayValue] = useState(1)

  const create = useMutation({
    mutationFn: () => api.createSchedule(cardID, scheduleInput(frequency, timeValue, dayValue, timezone)),
    onSuccess: async () => {
      toast.success('Schedule added')
      await refresh()
    },
    onError: showError,
  })
  const update = useMutation({
    mutationFn: (schedule: Schedule) =>
      api.updateSchedule(cardID, schedule.id, {
        enabled: !schedule.enabled,
        frequency: schedule.frequency,
        day_of_week: schedule.day_of_week,
        day_of_month: schedule.day_of_month,
        hour: schedule.hour,
        minute: schedule.minute,
        timezone: schedule.timezone,
      }),
    onSuccess: refresh,
    onError: showError,
  })
  const remove = useMutation({
    mutationFn: (scheduleID: string) => api.deleteSchedule(cardID, scheduleID),
    onSuccess: refresh,
    onError: showError,
  })

  return (
    <section className="mt-4 rounded-2xl border border-slate-800 bg-slate-950/50 p-4">
      <div className="flex items-center gap-2 text-xs font-semibold uppercase tracking-[0.08em] text-slate-400">
        <CalendarClock className="size-3.5" /> Schedule
      </div>

      <div className="mt-3 space-y-2">
        {schedules.map((schedule) => (
          <div key={schedule.id} className="rounded-xl border border-slate-800 px-3 py-2.5">
            <div className="flex items-start justify-between gap-2">
              <div>
                <p className="text-xs font-medium capitalize text-slate-300">{schedule.frequency}</p>
                <p className="mt-1 text-[10px] text-slate-600">
                  {String(schedule.hour).padStart(2, '0')}:{String(schedule.minute).padStart(2, '0')} · {schedule.timezone}
                </p>
                <p className="mt-1 flex items-center gap-1 text-[10px] text-slate-500">
                  <Clock3 className="size-3" /> Next {new Date(schedule.next_trigger_at).toLocaleString()}
                </p>
              </div>
              <div className="flex gap-1">
                <button
                  type="button"
                  onClick={() => update.mutate(schedule)}
                  className="grid size-7 place-items-center rounded-lg border border-slate-800 text-slate-500 hover:text-slate-200"
                  title={schedule.enabled ? 'Pause schedule' : 'Enable schedule'}
                >
                  {schedule.enabled ? <Pause className="size-3" /> : <Play className="size-3" />}
                </button>
                <button
                  type="button"
                  onClick={() => remove.mutate(schedule.id)}
                  className="grid size-7 place-items-center rounded-lg border border-slate-800 text-slate-500 hover:text-rose-300"
                  title="Delete schedule"
                >
                  <Trash2 className="size-3" />
                </button>
              </div>
            </div>
          </div>
        ))}
      </div>

      <div className="mt-3 grid grid-cols-2 gap-2">
        <select
          value={frequency}
          onChange={(event) => setFrequency(event.target.value as ScheduleInput['frequency'])}
          className="h-9 rounded-lg border border-slate-800 bg-slate-900 px-2 text-xs text-slate-300 outline-none"
        >
          <option value="daily">Daily</option>
          <option value="weekly">Weekly</option>
          <option value="monthly">Monthly</option>
        </select>
        <input
          type="time"
          value={timeValue}
          onChange={(event) => setTimeValue(event.target.value)}
          className="h-9 rounded-lg border border-slate-800 bg-slate-900 px-2 text-xs text-slate-300 outline-none"
        />
        {frequency !== 'daily' ? (
          <select
            value={dayValue}
            onChange={(event) => setDayValue(Number(event.target.value))}
            className="col-span-2 h-9 rounded-lg border border-slate-800 bg-slate-900 px-2 text-xs text-slate-300 outline-none"
          >
            {frequency === 'weekly'
              ? ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'].map((day, index) => (
                  <option key={day} value={index}>{day}</option>
                ))
              : Array.from({ length: 28 }, (_, index) => index + 1).map((day) => (
                  <option key={day} value={day}>Day {day}</option>
                ))}
          </select>
        ) : null}
      </div>
      <button
        type="button"
        onClick={() => create.mutate()}
        disabled={create.isPending}
        className="mt-2 h-9 w-full rounded-lg border border-slate-700 text-xs font-medium text-slate-300 transition hover:border-slate-600 disabled:opacity-50"
      >
        Add schedule
      </button>
      <p className="mt-2 text-[10px] leading-4 text-slate-600">Timezone: {timezone}. Missed occurrences are skipped.</p>
    </section>
  )
}

function scheduleInput(
  frequency: ScheduleInput['frequency'],
  timeValue: string,
  dayValue: number,
  timezone: string,
): ScheduleInput {
  const [hour, minute] = timeValue.split(':').map(Number)
  return {
    frequency,
    hour,
    minute,
    timezone,
    ...(frequency === 'weekly' ? { day_of_week: dayValue } : {}),
    ...(frequency === 'monthly' ? { day_of_month: dayValue } : {}),
  }
}

function HealthPill({ health }: { health: string }) {
  const className =
    health === 'healthy'
      ? 'bg-emerald-400/10 text-emerald-300'
      : health === 'warning'
        ? 'bg-amber-400/10 text-amber-300'
        : health === 'critical'
          ? 'bg-rose-400/10 text-rose-300'
          : 'bg-slate-800 text-slate-500'
  return <span className={`rounded-full px-2 py-0.5 text-[10px] font-medium capitalize ${className}`}>{health}</span>
}

function showError(error: unknown) {
  toast.error(error instanceof Error ? error.message : 'Action failed')
}
