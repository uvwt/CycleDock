import { useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useNavigate, useSearchParams } from "react-router-dom";
import {
  DndContext,
  KeyboardSensor,
  MouseSensor,
  TouchSensor,
  useDraggable,
  useDroppable,
  useSensor,
  useSensors,
  type DragEndEvent,
} from "@dnd-kit/core";
import { CSS } from "@dnd-kit/utilities";
import { ListPlus } from "lucide-react";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import { api } from "../../shared/api/client";
import { keys, queryClient } from "../../app/query";
import {
  Button,
  ErrorView,
  Field,
  Modal,
  Spinner,
  TextArea,
  TextInput,
} from "../../shared/ui/components";
import { CardStatusDot, getCardVisualStatus } from "../../shared/ui/CardStatusDot";
import { stripMarkdownSummary } from "../../shared/lib/markdown";
import type { Card, TodoState } from "../../shared/api/types";
import { useProject } from "../../app/project-context";

const states: TodoState[] = ["todo", "working", "review", "done"];
const labels: Record<TodoState, string> = {
  todo: "待办",
  working: "进行中",
  review: "待审核",
  done: "已完成",
};


const laneAccentMap: Record<TodoState, { badge: string }> = {
  todo: {
    badge: "border-slate-200 bg-slate-100/80 text-slate-600 dark:border-slate-800 dark:bg-slate-900/60 dark:text-slate-400",
  },
  working: {
    badge: "border-sky-200 bg-sky-50 text-sky-700 dark:border-sky-900/60 dark:bg-sky-950/40 dark:text-sky-300",
  },
  review: {
    badge: "border-amber-200 bg-amber-50 text-amber-700 dark:border-amber-900/60 dark:bg-amber-950/40 dark:text-amber-300",
  },
  done: {
    badge: "border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-900/60 dark:bg-emerald-950/40 dark:text-emerald-300",
  },
};

function formatRelativeTime(dateStr?: string) {
  if (!dateStr) return "";
  const date = new Date(dateStr);
  const now = new Date();
  const diffMs = now.getTime() - date.getTime();
  if (Number.isNaN(diffMs)) return "";
  const diffMins = Math.floor(diffMs / (1000 * 60));
  const diffHours = Math.floor(diffMs / (1000 * 60 * 60));
  const diffDays = Math.floor(diffMs / (1000 * 60 * 60 * 24));
  if (diffMins < 5) return "刚刚";
  if (diffMins < 60) return `${diffMins}m`;
  if (diffHours < 24) return `${diffHours}h`;
  if (diffDays === 1) return "昨天";
  if (diffDays < 7) return `${diffDays}d`;
  return `${date.getMonth() + 1}/${date.getDate()}`;
}
function Lane({
  state,
  cards,
  onOpen,
}: {
  state: TodoState;
  cards: Card[];
  onOpen: (card: Card) => void;
}) {
  const { t } = useTranslation();
  const { setNodeRef, isOver } = useDroppable({ id: state });
  const accent = laneAccentMap[state];

  return (
    <section
      ref={setNodeRef}
      className={
        "flex min-h-0 flex-col rounded-2xl border p-3.5 transition-colors " +
        (isOver
          ? "border-sky-300 bg-sky-50/50 dark:bg-sky-950/20"
          : "border-line bg-muted/40")
      }
    >
      <header className="mb-3 flex shrink-0 items-center justify-between px-1">
        <h2 className="text-sm font-semibold tracking-tight text-foreground">
          {t(labels[state])}
        </h2>
        <span
          className={
            "inline-flex h-5 min-w-5 items-center justify-center rounded-full border px-1.5 text-xs font-medium tabular-nums " +
            accent.badge
          }
        >
          {cards.length}
        </span>
      </header>
      <div className="flex min-h-0 flex-1 flex-col space-y-2.5 overflow-y-auto pr-1">
        {cards.map((c) => (
          <TaskCard key={c.id} card={c} onOpen={onOpen} />
        ))}
        {!cards.length && (
          <div className="grid flex-1 place-items-center rounded-xl border border-dashed border-line/70 bg-surface/30 p-6 text-center">
            <p className="text-xs text-faint">{t("没有任务")}</p>
          </div>
        )}
      </div>
    </section>
  );
}

function TaskCard({ card, onOpen }: { card: Card; onOpen: (c: Card) => void }) {
  const { attributes, listeners, setNodeRef, transform, isDragging } =
    useDraggable({ id: card.id });
  const visualStatus = getCardVisualStatus(card);
  const updatedTime = formatRelativeTime(card.updated_at);
  const summary = stripMarkdownSummary(card.description);

  return (
    <button
      ref={setNodeRef}
      style={{ transform: CSS.Translate.toString(transform) }}
      {...attributes}
      {...listeners}
      onClick={() => onOpen(card)}
      className={
        "group relative w-full rounded-xl border border-line bg-surface p-3 text-left shadow-xs transition-all duration-150 hover:-translate-y-0.5 hover:border-sky-300 hover:shadow-md dark:hover:border-sky-700/80 " +
        (isDragging ? "relative z-20 opacity-60 shadow-lg" : "")
      }
    >
      <div className="flex items-start gap-2.5">
        <div className="mt-1 shrink-0">
          <CardStatusDot status={visualStatus} />
        </div>
        <div className="min-w-0 flex-1">
          <div className="flex items-start justify-between gap-2">
            <h3 className="min-w-0 flex-1 line-clamp-2 text-sm font-medium leading-snug text-foreground group-hover:text-primary">
              {card.title}
            </h3>
            {updatedTime && (
              <span className="shrink-0 text-[11px] font-normal tabular-nums text-faint/70">
                {updatedTime}
              </span>
            )}
          </div>
          {summary && (
            <p className="mt-1 line-clamp-2 text-xs leading-relaxed text-muted-foreground/80">
              {summary}
            </p>
          )}
        </div>
      </div>
    </button>
  );
}
export default function BoardPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();
  const [creating, setCreating] = useState(false);
  const [creatingTodo, setCreatingTodo] = useState(false);
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [todoTitle, setTodoTitle] = useState("");
  const [todoDescription, setTodoDescription] = useState("");
  const {
    projectID,
    isPending: projectsPending,
    error: projectsError,
    refetch: refetchProjects,
    setProjectID,
  } = useProject();

  const creatingFromURL = searchParams.get("new") === "project";

  function setCreateProjectOpen(open: boolean) {
    setCreating(open);
    if (open || !creatingFromURL) return;

    const next = new URLSearchParams(searchParams);
    next.delete("new");
    setSearchParams(next, { replace: true });
  }
  const cards = useQuery({
    queryKey: keys.cards(projectID || ""),
    queryFn: () => api.cards(projectID!),
    enabled: !!projectID,
  });
  const create = useMutation({
    mutationFn: () =>
      api.createProject({
        name: name.trim(),
        description: description.trim(),
        instructions: "",
      }),
    onSuccess: async (project) => {
      setCreating(false);
      setName("");
      setDescription("");
      await queryClient.invalidateQueries({ queryKey: keys.projects });
      setProjectID(project.id);
      navigate("/board/" + project.id);
    },
    onError: (e) => toast.error(String(e)),
  });
  const createTodo = useMutation({
    mutationFn: () =>
      api.createTodo(projectID!, todoTitle.trim(), todoDescription.trim()),
    onSuccess: async (card) => {
      setCreatingTodo(false);
      setTodoTitle("");
      setTodoDescription("");
      await queryClient.invalidateQueries({
        queryKey: keys.cards(projectID || ""),
      });
      navigate(`/cards/${projectID}/${card.id}`);
    },
    onError: (e) => toast.error(String(e)),
  });
  const move = useMutation({
    mutationFn: ({ id, state }: { id: string; state: TodoState }) =>
      api.moveTodo(id, state),
    onSuccess: () =>
      void queryClient.invalidateQueries({
        queryKey: keys.cards(projectID || ""),
      }),
    onError: (e) => toast.error(e instanceof Error ? e.message : t("移动失败")),
  });
  const sensors = useSensors(
    useSensor(MouseSensor, { activationConstraint: { distance: 8 } }),
    useSensor(TouchSensor, {
      activationConstraint: {
        delay: 250,
        tolerance: 5,
      },
    }),
    useSensor(KeyboardSensor),
  );
  function dragEnd(e: DragEndEvent) {
    const state = e.over?.id as TodoState | undefined;
    const card = cards.data?.find((c) => c.id === e.active.id);
    if (
      !card ||
      !state ||
      !states.includes(state) ||
      card.kind !== "todo" ||
      card.todo_state === state
    )
      return;
    if (state === "done" && card.todo_state !== "review") return;
    move.mutate({ id: card.id, state });
  }
  function open(card: Card) {
    navigate(
      (card.kind === "discussion" ? "/chat/" : "/cards/") +
        projectID +
        "/" +
        card.id,
    );
  }
  return (
    <main className="mx-auto flex h-[calc(100dvh-3.5rem)] max-w-[1600px] flex-col overflow-hidden px-5 py-6 md:h-dvh md:px-8 md:py-8">
      <header className="mb-6 flex shrink-0 flex-wrap items-center justify-between gap-4">
        <div>
          <h1 className="text-3xl font-semibold tracking-tight text-heading">
            {t("工作")}
          </h1>
          <p className="mt-1 text-sm text-muted-foreground">
            {t("查看任务执行状态，跟踪待办进度。")}
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          {projectID && (
            <Button variant="primary" onClick={() => setCreatingTodo(true)}>
              <ListPlus className="size-4" />
              {t("新建待办")}
            </Button>
          )}
        </div>
      </header>
      {projectsPending && <Spinner />}
      {Boolean(projectsError) && (
        <ErrorView error={projectsError} onRetry={refetchProjects} />
      )}
      {projectID && (
        <div className="flex min-h-0 flex-1 flex-col">
          {cards.isPending ? (
            <Spinner />
          ) : cards.error ? (
            <ErrorView error={cards.error} />
          ) : (
            <DndContext sensors={sensors} onDragEnd={dragEnd}>
              <div className="grid min-h-0 flex-1 auto-cols-[minmax(280px,86vw)] grid-flow-col gap-3.5 overflow-x-auto pb-1 lg:grid-flow-row lg:grid-cols-4 lg:overflow-visible">
                {states.map((state) => (
                  <Lane
                    key={state}
                    state={state}
                    cards={(cards.data || []).filter(
                      (c) => c.kind === "todo" && c.todo_state === state,
                    )}
                    onOpen={open}
                  />
                ))}
              </div>
            </DndContext>
          )}
        </div>
      )}
      <Modal
        open={creating || creatingFromURL}
        onOpenChange={setCreateProjectOpen}
        title={t("新建项目")}
        description={t("创建一个新的项目看板来组织任务与 Agent 会话。")}
      >
        <form
          onSubmit={(e) => {
            e.preventDefault();
            create.mutate();
          }}
          className="space-y-4"
        >
          <Field label={t("项目名称")}>
            <TextInput
              autoFocus
              required
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder={t("输入项目名称")}
            />
          </Field>
          <Field label={t("项目描述")}>
            <TextArea
              rows={3}
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder={t("描述该项目的工作范围或目标")}
            />
          </Field>
          <div className="flex justify-end gap-2 pt-2">
            <Button
              variant="secondary"
              type="button"
              onClick={() => setCreateProjectOpen(false)}
            >
              {t("取消")}
            </Button>
            <Button
              variant="primary"
              type="submit"
              disabled={!name.trim() || create.isPending}
            >
              {t("创建项目")}
            </Button>
          </div>
        </form>
      </Modal>
      <Modal
        open={creatingTodo}
        onOpenChange={setCreatingTodo}
        title={t("新建待办")}
        description={t("添加一个待办任务到当前项目看板中。")}
      >
        <form
          onSubmit={(e) => {
            e.preventDefault();
            createTodo.mutate();
          }}
          className="space-y-4"
        >
          <Field label={t("待办标题")}>
            <TextInput
              autoFocus
              required
              value={todoTitle}
              onChange={(e) => setTodoTitle(e.target.value)}
              placeholder={t("要完成什么？")}
            />
          </Field>
          <Field label={t("待办描述")}>
            <TextArea
              rows={4}
              value={todoDescription}
              onChange={(e) => setTodoDescription(e.target.value)}
              placeholder={t("补充目标、范围或完成条件")}
            />
          </Field>
          <div className="flex justify-end gap-2 pt-2">
            <Button
              variant="secondary"
              type="button"
              onClick={() => setCreatingTodo(false)}
            >
              {t("取消")}
            </Button>
            <Button
              variant="primary"
              type="submit"
              disabled={!todoTitle.trim() || createTodo.isPending}
            >
              {t("创建待办")}
            </Button>
          </div>
        </form>
      </Modal>
    </main>
  );
}
