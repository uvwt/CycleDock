import { useEffect, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useNavigate, useParams, useSearchParams } from "react-router-dom";
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
import { Activity, FolderPlus, ListPlus, Clock3 } from "lucide-react";
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
import { cardStateLabel, healthLabel } from "../../shared/presentation/status";
import { WorkbenchLayout } from "../workbench/WorkbenchLayout";
import { ProjectDialog } from "../workbench/ProjectDialog";

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
      className={`min-h-72 rounded-xl border p-3 ${
        isOver ? "border-sky-300 bg-sky-50" : "border-slate-200 bg-slate-100/60"
      }`}
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
        {cards.map((card) => (
          <TaskCard key={card.id} card={card} onOpen={onOpen} />
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

function TaskCard({
  card,
  onOpen,
}: {
  card: Card;
  onOpen: (card: Card) => void;
}) {
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
      className={`w-full rounded-lg border border-slate-200 bg-white p-3.5 text-left shadow-sm hover:border-sky-300 ${
        isDragging ? "relative z-20 opacity-60" : ""
      }`}
    >
      <h3 className="line-clamp-2 text-sm font-semibold">{card.title}</h3>
      <p className="mt-1.5 line-clamp-2 text-[13px] leading-5 text-slate-500">
        {card.description}
      </p>
      <div className="mt-3 flex items-center justify-between text-xs text-slate-400">
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
  const [searchParams] = useSearchParams();
  const [creating, setCreating] = useState(false);
  const [creatingTodo, setCreatingTodo] = useState(false);
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [todoTitle, setTodoTitle] = useState("");
  const [todoDescription, setTodoDescription] = useState("");

  const projects = useQuery({ queryKey: keys.projects, queryFn: api.projects });
  const routeProject = projects.data?.find(
    (project) => project.id === params.projectID,
  );
  const project = params.projectID ? routeProject : projects.data?.[0];
  const projectID = project?.id;
  const newProjectRequested = searchParams.get("newProject") === "1";
  const view = searchParams.get("view") || "all";

  // 补全默认项目路径时必须保留 newProject 等查询参数，否则弹窗会闪现后关闭。
  const routeSearch = searchParams.toString();
  useEffect(() => {
    if (!params.projectID && projects.data?.length && !projects.isPending) {
      navigate(
        "/board/" + projects.data[0].id + (routeSearch ? "?" + routeSearch : ""),
        { replace: true },
      );
    }
  }, [navigate, params.projectID, projects.data, projects.isPending, routeSearch]);

  function setProjectSheetOpen(value: boolean) {
    setCreating(value);
    if (!value && newProjectRequested) {
      navigate(projectID ? "/board/" + projectID : "/board", {
        replace: true,
      });
    }
  }

  const cards = useQuery({
    queryKey: keys.cards(projectID || ""),
    queryFn: () => api.cards(projectID!),
    enabled: Boolean(projectID),
  });

  const create = useMutation({
    mutationFn: () =>
      api.createProject({
        name: name.trim(),
        description: description.trim(),
        instructions: "",
      }),
    onSuccess: async (created) => {
      setCreating(false);
      setName("");
      setDescription("");
      await queryClient.invalidateQueries({ queryKey: keys.projects });
      navigate("/board/" + created.id);
    },
    onError: (error) =>
      toast.error(error instanceof Error ? error.message : t("创建项目失败")),
  });

  const createTodo = useMutation({
    mutationFn: () =>
      api.createTodo(projectID!, todoTitle.trim(), todoDescription.trim()),
    onSuccess: async (card) => {
      setCreatingTodo(false);
      setTodoTitle("");
      setTodoDescription("");
      await queryClient.invalidateQueries({ queryKey: keys.cards(projectID!) });
      navigate(`/cards/${projectID}/${card.id}`);
    },
    onError: (error) =>
      toast.error(error instanceof Error ? error.message : t("创建待办失败")),
  });

  const move = useMutation({
    mutationFn: ({ id, state }: { id: string; state: TodoState }) =>
      api.moveTodo(id, state),
    onSuccess: () =>
      void queryClient.invalidateQueries({
        queryKey: keys.cards(projectID || ""),
      }),
    onError: (error) =>
      toast.error(error instanceof Error ? error.message : t("移动失败")),
  });

  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 8 } }),
    useSensor(KeyboardSensor),
  );

  function dragEnd(event: DragEndEvent) {
    const state = event.over?.id as TodoState | undefined;
    const card = cards.data?.find((item) => item.id === event.active.id);
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
    if (!projectID) return;
    navigate(
      (card.kind === "discussion" ? "/chat/" : "/cards/") +
        projectID +
        "/" +
        card.id,
    );
  }

  if (projects.isPending) {
    return (
      <div className="p-8">
        <Spinner />
      </div>
    );
  }
  if (projects.error) {
    return (
      <div className="p-8">
        <ErrorView error={projects.error} />
      </div>
    );
  }
  if (params.projectID && !routeProject) {
    return (
      <main className="mx-auto max-w-2xl px-5 py-16 text-center">
        <h1 className="text-xl font-semibold">{t("项目不可访问")}</h1>
        <p className="mt-2 text-sm text-slate-500">
          {t("这个项目不存在、已被删除，或不属于当前工作空间。")}
        </p>
        <Button className="mt-6" onClick={() => navigate("/board")}>
          {t("返回项目")}
        </Button>
      </main>
    );
  }
  if (!projectID || !project) {
    return (
      <>
        <main className="mx-auto max-w-2xl px-5 py-16 text-center">
          <h1 className="text-xl font-semibold">{t("还没有项目")}</h1>
          <p className="mt-2 text-sm text-slate-500">
            {t("创建第一个项目后，就可以开始讨论和安排工作。")}
          </p>
          <Button
            variant="primary"
            className="mt-6"
            onClick={() => setCreating(true)}
          >
            <FolderPlus className="size-4" />
            {t("新建项目")}
          </Button>
        </main>
        <ProjectDialog
          open={creating || newProjectRequested}
          onOpenChange={setProjectSheetOpen}
          name={name}
          description={description}
          setName={setName}
          setDescription={setDescription}
          pending={create.isPending}
          onSubmit={() => create.mutate()}
        />
      </>
    );
  }

  const projectCards = cards.data || [];
  return (
    <>
      <WorkbenchLayout mode="work" projectID={projectID} cards={projectCards}>
        <main className="mx-auto max-w-[1600px] space-y-6 px-4 pt-6 pb-14 sm:px-5 md:px-8">
          <header className="flex flex-wrap items-center justify-between gap-4 pl-11">
            <div>
              <h1 className="text-2xl font-semibold tracking-tight text-slate-900">
                {view === "review" ? t("待我审核") : view === "ongoing" ? t("持续事项") : project.name}
              </h1>
              <p className="mt-1 text-sm text-slate-500">
                {project.description || t("查看执行状态，跟踪长期工作。")}
              </p>
            </div>
            <div className="flex flex-wrap gap-2">
              <Button variant="primary" onClick={() => setCreatingTodo(true)}>
                <ListPlus className="size-4" />
                {t("新建待办")}
              </Button>
            </div>
          </header>

          {cards.isPending ? (
            <Spinner />
          ) : cards.error ? (
            <ErrorView error={cards.error} />
          ) : (
            <>
              {view !== "ongoing" && <DndContext sensors={sensors} onDragEnd={dragEnd}>
                <div className={`grid auto-cols-[minmax(260px,86vw)] grid-flow-col gap-3 overflow-x-auto pb-3 lg:grid-flow-row ${view === "review" ? "lg:grid-cols-1" : "lg:grid-cols-4"} lg:overflow-visible`}>
                  {states.filter((state) => view !== "review" || state === "review").map((state) => (
                    <Lane
                      key={state}
                      state={state}
                      cards={projectCards.filter(
                        (card) =>
                          card.kind === "todo" && card.todo_state === state,
                      )}
                      onOpen={open}
                    />
                  ))}
                </div>
              </DndContext>}

              {view !== "review" && <section className="space-y-3">
                <h2 className="flex items-center gap-2 font-semibold">
                  <Activity className="size-5 text-sky-700" />
                  {t("持续事项")}
                  <span className="text-sm text-slate-400">
                    {
                      projectCards.filter((card) => card.kind === "ongoing")
                        .length
                    }
                  </span>
                </h2>
                <div className="grid gap-3 md:grid-cols-2 lg:grid-cols-3">
                  {projectCards
                    .filter((card) => card.kind === "ongoing")
                    .map((card) => (
                      <button
                        key={card.id}
                        type="button"
                        onClick={() => open(card)}
                        className="rounded-xl border border-slate-200 bg-white p-4 text-left hover:border-sky-300"
                      >
                        <div className="flex justify-between gap-3">
                          <span className="font-semibold">{card.title}</span>
                          <span className="text-xs text-slate-400">
                            {healthLabel(t, card.health)}
                          </span>
                        </div>
                        <p className="mt-2 line-clamp-2 text-sm text-slate-500">
                          {card.description}
                        </p>
                        <p className="mt-4 text-xs text-sky-700">
                          {cardStateLabel(t, card)}
                        </p>
                      </button>
                    ))}
                  {!projectCards.some((card) => card.kind === "ongoing") && (
                    <p className="text-sm text-slate-400">
                      {t("暂无持续事项")}
                    </p>
                  )}
                </div>
              </section>}
            </>
          )}
        </main>
      </WorkbenchLayout>

      <ProjectDialog
        open={creating || newProjectRequested}
        onOpenChange={setProjectSheetOpen}
        name={name}
        description={description}
        setName={setName}
        setDescription={setDescription}
        pending={create.isPending}
        onSubmit={() => create.mutate()}
      />

      <Sheet
        open={creatingTodo}
        onOpenChange={setCreatingTodo}
        title={t("新建待办")}
      >
        <form
          onSubmit={(event) => {
            event.preventDefault();
            createTodo.mutate();
          }}
          className="space-y-5"
        >
          <Field label={t("待办标题")}>
            <TextInput
              autoFocus
              required
              value={todoTitle}
              onChange={(event) => setTodoTitle(event.target.value)}
              placeholder={t("要完成什么？")}
            />
          </Field>
          <Field label={t("待办描述")}>
            <TextArea
              rows={5}
              value={todoDescription}
              onChange={(event) => setTodoDescription(event.target.value)}
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
    </>
  );
}
