import { useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useNavigate, useParams } from "react-router-dom";
import {
  DndContext,
  KeyboardSensor,
  PointerSensor,
  useDraggable,
  useDroppable,
  useSensor,
  useSensors,
  type DragEndEvent,
} from "@dnd-kit/core";
import { CSS } from "@dnd-kit/utilities";
import {
  Activity,
  FolderPlus,
  ListPlus,
  MessageSquareText,
  Clock3,
  ChevronRight,
} from "lucide-react";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import { api } from "../../shared/api/client";
import { keys, queryClient } from "../../app/query";
import {
  Button,
  ErrorView,
  Field,
  Sheet,
  Spinner,
  TextArea,
  TextInput,
} from "../../shared/ui/components";
import type { Card, TodoState } from "../../shared/api/types";

const states: TodoState[] = ["todo", "working", "review", "done"];
const labels: Record<TodoState, string> = {
  todo: "待办",
  working: "进行中",
  review: "待审核",
  done: "已完成",
};

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
  return (
    <section
      ref={setNodeRef}
      className={`min-h-75 rounded-2xl border p-3 ${isOver ? "border-sky-300 bg-sky-50" : "border-slate-200 bg-slate-100/60"}`}
    >
      <header className="mb-4 flex items-center justify-between px-1">
        <h2 className="text-sm font-semibold text-slate-700">
          {t(labels[state])}
        </h2>
        <span className="rounded-full bg-white px-2 py-0.5 text-xs text-slate-500">
          {cards.length}
        </span>
      </header>
      <div className="space-y-2">
        {cards.map((c) => (
          <TaskCard key={c.id} card={c} onOpen={onOpen} />
        ))}
        {!cards.length && (
          <div className="rounded-xl border border-dashed border-slate-200 p-6 text-center text-xs text-slate-400">
            {t("没有任务")}
          </div>
        )}
      </div>
    </section>
  );
}
function TaskCard({ card, onOpen }: { card: Card; onOpen: (c: Card) => void }) {
  const { t } = useTranslation();
  const { attributes, listeners, setNodeRef, transform, isDragging } =
    useDraggable({ id: card.id });
  return (
    <button
      ref={setNodeRef}
      style={{ transform: CSS.Translate.toString(transform) }}
      {...attributes}
      {...listeners}
      onClick={() => onOpen(card)}
      className={`w-full rounded-xl border border-slate-200 bg-white p-4 text-left shadow-sm hover:border-sky-300 ${isDragging ? "relative z-20 opacity-60" : ""}`}
    >
      <h3 className="line-clamp-2 text-sm font-semibold">{card.title}</h3>
      <p className="mt-2 line-clamp-2 text-xs leading-5 text-slate-500">
        {card.description}
      </p>
      <div className="mt-4 flex items-center justify-between text-[11px] text-slate-400">
        <span>{card.current_run_status || t("未运行")}</span>
        <Clock3 className="size-3" />
      </div>
    </button>
  );
}
export default function BoardPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const params = useParams();
  const [picked, setPicked] = useState("");
  const [creating, setCreating] = useState(false);
  const [creatingTodo, setCreatingTodo] = useState(false);
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [todoTitle, setTodoTitle] = useState("");
  const [todoDescription, setTodoDescription] = useState("");
  const projects = useQuery({ queryKey: keys.projects, queryFn: api.projects });
  const projectID = projects.data?.some(
    (p) => p.id === (params.projectID || picked),
  )
    ? params.projectID || picked
    : projects.data?.[0]?.id;
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
    useSensor(PointerSensor, { activationConstraint: { distance: 8 } }),
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
    <main className="mx-auto max-w-[1600px] space-y-7 px-5 py-8 md:px-8">
      <header className="flex flex-wrap items-center justify-between gap-4">
        <div>
          <p className="text-xs font-semibold tracking-widest text-sky-700">
            PROJECT BOARD
          </p>
          <h1 className="mt-2 text-3xl font-semibold tracking-tight">
            {t("看板")}
          </h1>
          <p className="mt-1 text-sm text-slate-500">
            {t("查看执行状态，跟踪长期工作。")}
          </p>
        </div>
        <div className="flex flex-wrap gap-2">
          {projectID && (
            <Button variant="primary" onClick={() => setCreatingTodo(true)}>
              <ListPlus className="size-4" />
              {t("新建待办")}
            </Button>
          )}
          <Button onClick={() => setCreating(true)}>
            <FolderPlus className="size-4" />
            {t("新建项目")}
          </Button>
        </div>
      </header>
      {projects.isPending && <Spinner />}
      {projects.error && <ErrorView error={projects.error} />}
      {projectID && (
        <>
          <div className="flex items-center justify-between gap-3">
            <select
              aria-label={t("切换项目")}
              className="rounded-xl border border-slate-200 bg-white px-3 py-2 text-sm"
              value={projectID}
              onChange={(e) => {
                setPicked(e.target.value);
                navigate("/board/" + e.target.value);
              }}
            >
              {projects.data?.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.name}
                </option>
              ))}
            </select>
            <Button onClick={() => navigate("/chat")}>
              <MessageSquareText className="size-4" />
              {t("从讨论中生成工作")} <ChevronRight className="size-4" />
            </Button>
          </div>
          {cards.isPending ? (
            <Spinner />
          ) : cards.error ? (
            <ErrorView error={cards.error} />
          ) : (
            <>
              <DndContext sensors={sensors} onDragEnd={dragEnd}>
                <div className="grid auto-cols-[minmax(260px,86vw)] grid-flow-col gap-3 overflow-x-auto pb-3 lg:grid-flow-row lg:grid-cols-4 lg:overflow-visible">
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
              <section className="space-y-3">
                <h2 className="flex items-center gap-2 font-semibold">
                  <Activity className="size-5 text-sky-700" />
                  {t("持续事项")}{" "}
                  <span className="text-sm text-slate-400">
                    {cards.data?.filter((c) => c.kind === "ongoing").length}
                  </span>
                </h2>
                <div className="grid gap-3 md:grid-cols-2 lg:grid-cols-3">
                  {cards.data
                    ?.filter((c) => c.kind === "ongoing")
                    .map((c) => (
                      <button
                        key={c.id}
                        onClick={() => open(c)}
                        className="rounded-2xl border border-slate-200 bg-white p-5 text-left hover:border-sky-300"
                      >
                        <div className="flex justify-between gap-3">
                          <span className="font-semibold">{c.title}</span>
                          <span className="text-xs text-slate-400">
                            {c.health || "unknown"}
                          </span>
                        </div>
                        <p className="mt-2 line-clamp-2 text-sm text-slate-500">
                          {c.description}
                        </p>
                        <p className="mt-4 text-xs text-sky-700">
                          {c.ongoing_state}
                        </p>
                      </button>
                    ))}
                  {!cards.data?.some((c) => c.kind === "ongoing") && (
                    <p className="text-sm text-slate-400">
                      {t("暂无持续事项")}
                    </p>
                  )}
                </div>
              </section>
            </>
          )}
        </>
      )}
      <Sheet open={creating} onOpenChange={setCreating} title={t("新建项目")}>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            create.mutate();
          }}
          className="space-y-5"
        >
          <Field label={t("项目名称")}>
            <TextInput
              required
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          </Field>
          <Field label={t("项目描述")}>
            <TextArea
              rows={4}
              value={description}
              onChange={(e) => setDescription(e.target.value)}
            />
          </Field>
          <Button
            variant="primary"
            type="submit"
            disabled={!name.trim() || create.isPending}
          >
            {t("创建项目")}
          </Button>
        </form>
      </Sheet>
      <Sheet
        open={creatingTodo}
        onOpenChange={setCreatingTodo}
        title={t("新建待办")}
      >
        <form
          onSubmit={(e) => {
            e.preventDefault();
            createTodo.mutate();
          }}
          className="space-y-5"
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
              rows={5}
              value={todoDescription}
              onChange={(e) => setTodoDescription(e.target.value)}
              placeholder={t("补充目标、范围或完成条件")}
            />
          </Field>
          <Button
            variant="primary"
            type="submit"
            disabled={!todoTitle.trim() || createTodo.isPending}
          >
            {t("创建待办")}
          </Button>
        </form>
      </Sheet>
    </main>
  );
}
