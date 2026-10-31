import { DeviceMenu } from "../devices/DeviceMenu";
import { useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useNavigate, useSearchParams } from "react-router-dom";
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
import { ListPlus, Clock3 } from "lucide-react";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import { api } from "../../shared/api/client";
import { keys, queryClient } from "../../app/query";
import {
  Badge,
  Button,
  ErrorView,
  Field,
  Modal,
  Spinner,
  TextArea,
  TextInput,
} from "../../shared/ui/components";
import type { Card, TodoState } from "../../shared/api/types";
import { useProject } from "../../app/project-context";

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
      className={`flex min-h-[520px] flex-col rounded-2xl border p-3 ${isOver ? "border-sky-300 bg-sky-50" : "border-line bg-muted/60"}`}
    >
      <header className="mb-4 flex items-center justify-between px-1">
        <h2 className="text-sm font-semibold text-foreground">
          {t(labels[state])}
        </h2>
        <Badge
          variant="secondary"
          className="border-border/60 bg-surface font-normal text-muted-foreground"
        >
          {cards.length}
        </Badge>
      </header>
      <div className="flex flex-1 flex-col space-y-2">
        {cards.map((c) => (
          <TaskCard key={c.id} card={c} onOpen={onOpen} />
        ))}
        {!cards.length && (
          <div className="grid flex-1 place-items-center rounded-xl border border-dashed border-line p-6 text-center text-xs text-faint">
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
      className={`w-full rounded-xl border border-line bg-surface p-4 text-left shadow-sm hover:border-sky-300 ${isDragging ? "relative z-20 opacity-60" : ""}`}
    >
      <h3 className="line-clamp-2 text-sm font-semibold">{card.title}</h3>
      <p className="mt-2 line-clamp-2 text-xs leading-5 text-muted-foreground">
        {card.description}
      </p>
      <div className="mt-4 flex items-center justify-between text-[11px] text-faint">
        <span>{card.current_attempt_status || t("未运行")}</span>
        <Clock3 className="size-3" />
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
          <h1 className="text-3xl font-semibold tracking-tight text-heading">
            {t("工作")}
          </h1>
          <p className="mt-1 text-sm text-muted-foreground">
            {t("查看任务执行状态，跟踪待办进度。")}
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <DeviceMenu />
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
        <>
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
            </>
          )}
        </>
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
