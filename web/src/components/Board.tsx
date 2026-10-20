import { DndContext, PointerSensor, useDraggable, useDroppable, useSensor, useSensors, type DragEndEvent } from '@dnd-kit/core'
import { CSS } from '@dnd-kit/utilities'
import { Activity, Circle, Clock3 } from 'lucide-react'
import type { Card, NormalColumn } from '../api'

const columns: Array<{ id: NormalColumn; label: string }> = [
  { id: 'discussion', label: 'Discussion' },
  { id: 'todo', label: 'Todo' },
  { id: 'working', label: 'Working' },
  { id: 'review', label: 'Review' },
  { id: 'done', label: 'Done' },
]

export function Board({
  cards,
  onOpen,
  onMove,
}: {
  cards: Card[]
  onOpen: (card: Card) => void
  onMove: (cardID: string, column: NormalColumn) => void
}) {
  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 6 } }))
  const normalCards = cards.filter((card) => card.kind === 'normal')
  const ongoingCards = cards.filter((card) => card.kind === 'ongoing')

  function handleDragEnd(event: DragEndEvent) {
    const cardID = String(event.active.id)
    const column = event.over?.id as NormalColumn | undefined
    const card = normalCards.find((item) => item.id === cardID)
    if (card && column && columns.some((item) => item.id === column) && card.normal_column !== column) {
      onMove(cardID, column)
    }
  }

  return (
    <div className="space-y-8">
      <DndContext sensors={sensors} onDragEnd={handleDragEnd}>
        <div className="grid min-w-[980px] grid-cols-5 gap-3">
          {columns.map((column) => (
            <BoardColumn
              key={column.id}
              column={column}
              cards={normalCards.filter((card) => card.normal_column === column.id)}
              onOpen={onOpen}
            />
          ))}
        </div>
      </DndContext>

      <section>
        <div className="mb-3 flex items-center gap-2">
          <Activity className="size-4 text-cyan-300" />
          <h2 className="text-sm font-semibold text-slate-200">Ongoing cycles</h2>
          <span className="rounded-full bg-slate-800 px-2 py-0.5 text-[11px] text-slate-400">{ongoingCards.length}</span>
        </div>
        {ongoingCards.length === 0 ? (
          <div className="rounded-2xl border border-dashed border-slate-800 px-5 py-7 text-sm text-slate-500">
            No active ongoing cards yet.
          </div>
        ) : (
          <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
            {ongoingCards.map((card) => (
              <button
                key={card.id}
                onClick={() => onOpen(card)}
                className={`rounded-2xl border border-slate-800 bg-slate-900/70 p-4 text-left transition hover:border-slate-700 hover:bg-slate-900 ${
                  card.ongoing_state === 'archived' ? 'opacity-55' : ''
                }`}
              >
                <div className="flex items-start justify-between gap-3">
                  <div>
                    <p className="font-medium text-slate-100">{card.title}</p>
                    <p className="mt-1 line-clamp-2 text-xs leading-5 text-slate-500">{card.description || 'No description'}</p>
                  </div>
                  <HealthBadge health={card.health ?? 'unknown'} />
                </div>
                <div className="mt-4 flex items-center justify-between text-[11px] text-slate-500">
                  <span className="capitalize">{card.ongoing_state}</span>
                  {card.current_run_status ? <RunBadge status={card.current_run_status} /> : <span>Idle</span>}
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
  cards,
  onOpen,
}: {
  column: { id: NormalColumn; label: string }
  cards: Card[]
  onOpen: (card: Card) => void
}) {
  const { setNodeRef, isOver } = useDroppable({ id: column.id })
  return (
    <section
      ref={setNodeRef}
      className={`min-h-[420px] rounded-2xl border p-2 transition ${
        isOver ? 'border-cyan-400/70 bg-cyan-400/5' : 'border-slate-800 bg-slate-900/45'
      }`}
    >
      <header className="mb-2 flex items-center justify-between px-2 py-1.5">
        <div className="flex items-center gap-2">
          <Circle className="size-2.5 fill-current text-slate-500" />
          <h2 className="text-xs font-semibold uppercase tracking-[0.08em] text-slate-400">{column.label}</h2>
        </div>
        <span className="text-[11px] tabular-nums text-slate-600">{cards.length}</span>
      </header>
      <div className="space-y-2">
        {cards.map((card) => (
          <DraggableCard key={card.id} card={card} onOpen={onOpen} />
        ))}
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
      className={`w-full rounded-xl border border-slate-800 bg-slate-950/80 p-3 text-left shadow-sm transition hover:border-slate-700 ${
        isDragging ? 'z-20 opacity-60 shadow-xl' : ''
      }`}
    >
      <p className="text-sm font-medium leading-5 text-slate-100">{card.title}</p>
      {card.description ? <p className="mt-1.5 line-clamp-2 text-xs leading-5 text-slate-500">{card.description}</p> : null}
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
      ? 'border-emerald-800 bg-emerald-950/60 text-emerald-300'
      : health === 'warning'
        ? 'border-amber-800 bg-amber-950/60 text-amber-300'
        : health === 'critical'
          ? 'border-rose-800 bg-rose-950/60 text-rose-300'
          : 'border-slate-700 bg-slate-900 text-slate-500'
  return <span className={`rounded-full border px-2 py-0.5 text-[10px] font-medium capitalize ${className}`}>{health}</span>
}

function RunBadge({ status }: { status: string }) {
  return <span className="rounded-full bg-cyan-400/10 px-2 py-0.5 text-[10px] font-medium text-cyan-300">{status.replace('_', ' ')}</span>
}
