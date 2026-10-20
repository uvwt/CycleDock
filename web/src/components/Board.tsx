import {
  DndContext,
  KeyboardSensor,
  PointerSensor,
  useDraggable,
  useDroppable,
  useSensor,
  useSensors,
  type DragEndEvent,
} from '@dnd-kit/core'
import { CSS } from '@dnd-kit/utilities'
import { Activity, Circle, Clock3 } from 'lucide-react'
import type { Card, NormalColumn } from '../api'
import { useI18n } from '../i18n-context'

const columns: NormalColumn[] = ['discussion', 'todo', 'working', 'review', 'done']

export function Board({
  cards,
  onOpen,
  onMove,
}: {
  cards: Card[]
  onOpen: (card: Card) => void
  onMove: (cardID: string, column: NormalColumn) => void
}) {
  const { t } = useI18n()
  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 6 } }),
    useSensor(KeyboardSensor),
  )
  const normalCards = cards.filter((card) => card.kind === 'normal')
  const ongoingCards = cards.filter((card) => card.kind === 'ongoing')

  function handleDragEnd(event: DragEndEvent) {
    const cardID = String(event.active.id)
    const column = event.over?.id as NormalColumn | undefined
    const card = normalCards.find((item) => item.id === cardID)
    const blockedDone = column === 'done' && card?.normal_column !== 'review'
    if (card && column && columns.includes(column) && card.normal_column !== column && !blockedDone) {
      onMove(cardID, column)
    }
  }

  return (
    <div className="space-y-8">
      <DndContext sensors={sensors} onDragEnd={handleDragEnd}>
        <div className="grid snap-x snap-mandatory auto-cols-[minmax(260px,85vw)] grid-flow-col gap-3 overflow-x-auto pb-2 lg:min-w-[980px] lg:snap-none lg:grid-flow-row lg:grid-cols-5 lg:overflow-visible lg:pb-0">
          {columns.map((column) => (
            <BoardColumn
              key={column}
              column={column}
              label={t(`board.${column}`)}
              emptyLabel={t('board.emptyColumn')}
              cards={normalCards.filter((card) => card.normal_column === column)}
              onOpen={onOpen}
            />
          ))}
        </div>
      </DndContext>

      <section>
        <div className="mb-3 flex items-center gap-2">
          <Activity className="size-4 text-sky-700" />
          <h2 className="text-sm font-semibold text-slate-800">{t('board.ongoing')}</h2>
          <span className="rounded-full bg-slate-100 px-2 py-0.5 text-[11px] text-slate-600">{ongoingCards.length}</span>
        </div>
        {ongoingCards.length === 0 ? (
          <div className="rounded-2xl border border-dashed border-slate-200 px-5 py-7 text-sm text-slate-600">
            {t('board.emptyOngoing')}
          </div>
        ) : (
          <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
            {ongoingCards.map((card) => (
              <button
                key={card.id}
                onClick={() => onOpen(card)}
                className={`rounded-2xl border border-slate-200 bg-white p-4 text-left transition hover:border-slate-300 hover:bg-white ${
                  card.ongoing_state === 'archived' ? 'opacity-55' : ''
                }`}
              >
                <div className="flex items-start justify-between gap-3">
                  <div>
                    <p className="font-medium text-slate-900">{card.title}</p>
                    <p className="mt-1 line-clamp-2 text-xs leading-5 text-slate-600">
                      {card.description || t('board.noDescription')}
                    </p>
                  </div>
                  <HealthBadge health={card.health ?? 'unknown'} />
                </div>
                <div className="mt-4 flex items-center justify-between text-[11px] text-slate-600">
                  <span className="capitalize">{card.ongoing_state}</span>
                  {card.current_run_status ? <RunBadge status={card.current_run_status} /> : <span>{t('board.idle')}</span>}
                </div>
              </button>
            ))}
          </div>
        )}
      </section>
    </div>
  )
}

function BoardColumn({
  column,
  label,
  emptyLabel,
  cards,
  onOpen,
}: {
  column: NormalColumn
  label: string
  emptyLabel: string
  cards: Card[]
  onOpen: (card: Card) => void
}) {
  const { setNodeRef, isOver } = useDroppable({ id: column })
  return (
    <section
      ref={setNodeRef}
      className={`min-h-[360px] snap-start rounded-2xl border p-2 transition lg:min-h-[420px] ${
        isOver ? 'border-sky-400 bg-sky-50' : 'border-slate-200 bg-slate-50'
      }`}
      aria-label={label}
    >
      <header className="mb-2 flex items-center justify-between px-2 py-1.5">
        <div className="flex items-center gap-2">
          <Circle className="size-2.5 fill-current text-slate-600" />
          <h2 className="text-xs font-semibold uppercase tracking-[0.08em] text-slate-600">{label}</h2>
        </div>
        <span className="text-[11px] tabular-nums text-slate-600">{cards.length}</span>
      </header>
      <div className="space-y-2">
        {cards.length === 0 ? (
          <p className="rounded-xl border border-dashed border-slate-200 px-3 py-6 text-center text-[11px] text-slate-600">
            {emptyLabel}
          </p>
        ) : (
          cards.map((card) => <DraggableCard key={card.id} card={card} onOpen={onOpen} />)
        )}
      </div>
    </section>
  )
}

function DraggableCard({ card, onOpen }: { card: Card; onOpen: (card: Card) => void }) {
  const { attributes, listeners, setNodeRef, transform, isDragging } = useDraggable({ id: card.id })
  return (
    <button
      ref={setNodeRef}
      style={{ transform: CSS.Translate.toString(transform) }}
      onClick={() => onOpen(card)}
      {...listeners}
      {...attributes}
      className={`w-full rounded-xl border border-slate-200 bg-white p-3 text-left shadow-sm transition hover:border-slate-300 ${
        isDragging ? 'z-20 opacity-60 shadow-xl' : ''
      }`}
    >
      <p className="text-sm font-medium leading-5 text-slate-900">{card.title}</p>
      {card.description ? <p className="mt-1.5 line-clamp-2 text-xs leading-5 text-slate-600">{card.description}</p> : null}
      <div className="mt-3 flex items-center justify-between">
        {card.current_run_status ? <RunBadge status={card.current_run_status} /> : <span />}
        <span className="flex items-center gap-1 text-[10px] text-slate-600">
          <Clock3 className="size-3" />
          {new Date(card.updated_at).toLocaleDateString()}
        </span>
      </div>
    </button>
  )
}

function HealthBadge({ health }: { health: string }) {
  const className =
    health === 'healthy'
      ? 'border-emerald-300 bg-emerald-50 text-emerald-700'
      : health === 'warning'
        ? 'border-amber-300 bg-amber-50 text-amber-700'
        : health === 'critical'
          ? 'border-rose-300 bg-rose-50 text-rose-700'
          : 'border-slate-300 bg-white text-slate-600'
  return <span className={`rounded-full border px-2 py-0.5 text-[10px] font-medium capitalize ${className}`}>{health}</span>
}

function RunBadge({ status }: { status: string }) {
  return <span className="rounded-full bg-sky-50 px-2 py-0.5 text-[10px] font-medium text-sky-700">{status.replace('_', ' ')}</span>
}
