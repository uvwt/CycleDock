import { useEffect, useMemo, useRef, useState } from "react";
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

const laneAccentMap: Record<TodoState, { badge: string; activeTab: string; dot: string }> = {
  todo: {
    badge: "border-slate-200 bg-slate-100/80 text-slate-600 dark:border-slate-800 dark:bg-slate-900/60 dark:text-slate-400",
    activeTab: "border-slate-300 bg-surface text-foreground shadow-xs dark:border-slate-700",
    dot: "bg-slate-400",
  },
  working: {
    badge: "border-sky-200 bg-sky-50 text-sky-700 dark:border-sky-900/60 dark:bg-sky-950/40 dark:text-sky-300",
    activeTab: "border-sky-300/80 bg-surface text-sky-700 shadow-xs dark:border-sky-700/80 dark:text-sky-300",
    dot: "bg-sky-500",
  },
  review: {
    badge: "border-amber-200 bg-amber-50 text-amber-700 dark:border-amber-900/60 dark:bg-amber-950/40 dark:text-amber-300",
    activeTab: "border-amber-300/80 bg-surface text-amber-700 shadow-xs dark:border-amber-700/80 dark:text-amber-300",
    dot: "bg-amber-500",
  },
  done: {
    badge: "border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-900/60 dark:bg-emerald-950/40 dark:text-emerald-300",
    activeTab: "border-emerald-300/80 bg-surface text-emerald-700 shadow-xs dark:border-emerald-700/80 dark:text-emerald-300",
    dot: "bg-emerald-500",
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
  className = "",
  hasMoreDone,
  loadingMoreDone,
  onLoadMoreDone,
}: {
  state: TodoState;
  cards: Card[];
  onOpen: (card: Card) => void;
  className?: string;
  hasMoreDone?: boolean;
  loadingMoreDone?: boolean;
  onLoadMoreDone?: () => void;
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
          ? "border-sky-300 bg-sky-50/50 dark:bg-sky-950/20 "
          : "border-line bg-muted/40 ") +
        className
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
      <div className="flex min-h-0 flex-1 flex-col space-y-2.5 overflow-y-auto pr-1 pb-24 lg:pb-2">
        {cards.map((c) => (
          <TaskCard key={c.id} card={c} onOpen={onOpen} />
        ))}
        {state === "done" && hasMoreDone && (
          <div className="pt-2 pb-1 text-center">
            <button
              type="button"
              disabled={loadingMoreDone}
              onClick={onLoadMoreDone}
              className="inline-flex items-center gap-1.5 rounded-lg border border-line bg-surface px-3 py-1.5 text-xs font-medium text-muted-foreground shadow-xs hover:bg-muted/70 hover:text-foreground disabled:opacity-50 transition-colors"
            >
              {loadingMoreDone ? (
                <>
                  <Spinner text="" />
                  <span>{t("加载中...")}</span>
                </>
              ) : (
                <span>{t("加载更早的已完成卡片")}</span>
              )}
            </button>
          </div>
        )}
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
  const [isDesktop, setIsDesktop] = useState(() => {
    if (typeof window !== "undefined" && window.matchMedia) {
      return window.matchMedia("(min-width: 1024px)").matches;
    }
    return true;
  });

  useEffect(() => {
    if (typeof window === "undefined" || !window.matchMedia) return;
    const media = window.matchMedia("(min-width: 1024px)");
    const listener = (e: MediaQueryListEvent) => setIsDesktop(e.matches);
    media.addEventListener("change", listener);
    return () => media.removeEventListener("change", listener);
  }, []);

  const {
    projectID,
    isPending: projectsPending,
    error: projectsError,
    refetch: refetchProjects,
    setProjectID,
  } = useProject();

  const [moreDoneCards, setMoreDoneCards] = useState<Card[]>([]);
  const [hasMoreDone, setHasMoreDone] = useState(false);
  const [loadingMoreDone, setLoadingMoreDone] = useState(false);

  // When projectID changes, reset extra loaded done cards
  useEffect(() => {
    setMoreDoneCards([]);
    setHasMoreDone(false);
  }, [projectID]);

  const cards = useQuery({
    queryKey: keys.cards(projectID || ""),
    queryFn: () => api.cards(projectID!),
    enabled: !!projectID,
  });

  // Check if initial active cards list hit the 30 done cards limit
  useEffect(() => {
    if (!cards.data) return;
    const initialDone = cards.data.filter(
      (c) => c.kind === "todo" && c.todo_state === "done",
    );
    // If backend truncated at 30 items, there might be more
    if (initialDone.length >= 30 && moreDoneCards.length === 0) {
      setHasMoreDone(true);
    }
  }, [cards.data, moreDoneCards.length]);

  const handleLoadMoreDone = async () => {
    if (!projectID || loadingMoreDone) return;
    setLoadingMoreDone(true);
    try {
      const offset = (cards.data || []).filter(
        (c) => c.kind === "todo" && c.todo_state === "done",
      ).length + moreDoneCards.length;
      const res = await api.projectDoneCards(projectID, 30, offset);
      if (res.cards.length === 0) {
        setHasMoreDone(false);
      } else {
        setMoreDoneCards((prev) => {
          const map = new Map<string, Card>();
          for (const c of prev) map.set(c.id, c);
          for (const c of res.cards) map.set(c.id, c);
          return Array.from(map.values());
        });
        setHasMoreDone(res.has_more);
      }
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t("加载失败"));
    } finally {
      setLoadingMoreDone(false);
    }
  };

  const cardsByState = useMemo(() => {
    const map: Record<TodoState, Card[]> = {
      todo: [],
      working: [],
      review: [],
      done: [],
    };
    for (const c of cards.data || []) {
      if (c.kind === "todo" && c.todo_state && states.includes(c.todo_state)) {
        map[c.todo_state].push(c);
      }
    }
    if (moreDoneCards.length > 0) {
      const doneMap = new Map<string, Card>();
      for (const c of map.done) doneMap.set(c.id, c);
      for (const c of moreDoneCards) doneMap.set(c.id, c);
      map.done = Array.from(doneMap.values());
    }
    return map;
  }, [cards.data, moreDoneCards]);

  // 默认激活用户最需要关注的列：working > todo > 首个非空列 > working
  const [selectedTab, setSelectedTab] = useState<TodoState | null>(null);
  const activeTab: TodoState = useMemo(() => {
    if (selectedTab) return selectedTab;
    if (cardsByState.working.length > 0) return "working";
    if (cardsByState.todo.length > 0) return "todo";
    const firstNonEmpty = states.find((s) => cardsByState[s].length > 0);
    return firstNonEmpty || "working";
  }, [selectedTab, cardsByState]);

  const mobileContainerRef = useRef<HTMLDivElement>(null);
  const isProgrammaticScrollRef = useRef(false);

  // 初始化或当激活 Tab 变化时自动对齐移动端滑动容器
  useEffect(() => {
    if (isDesktop) return;
    const container = mobileContainerRef.current;
    if (!container) return;
    const targetIndex = states.indexOf(activeTab);
    if (targetIndex >= 0) {
      const targetScrollLeft = targetIndex * container.clientWidth;
      if (Math.abs(container.scrollLeft - targetScrollLeft) > 2) {
        container.scrollTo({
          left: targetScrollLeft,
          behavior: isProgrammaticScrollRef.current ? "smooth" : "auto",
        });
      }
    }
  }, [activeTab, isDesktop]);

  function handleTabSelect(state: TodoState) {
    setSelectedTab(state);
    const container = mobileContainerRef.current;
    if (!container) return;
    const targetIndex = states.indexOf(state);
    if (targetIndex >= 0) {
      isProgrammaticScrollRef.current = true;
      const targetScrollLeft = targetIndex * container.clientWidth;
      container.scrollTo({
        left: targetScrollLeft,
        behavior: "smooth",
      });
      setTimeout(() => {
        isProgrammaticScrollRef.current = false;
      }, 400);
    }
  }

  function handleMobileScroll() {
    if (isProgrammaticScrollRef.current) return;
    const container = mobileContainerRef.current;
    if (!container || container.clientWidth === 0) return;
    const scrollLeft = container.scrollLeft;
    const width = container.clientWidth;
    const index = Math.round(scrollLeft / width);
    if (index >= 0 && index < states.length) {
      const state = states[index];
      if (state !== activeTab) {
        setSelectedTab(state);
      }
    }
  }

  const creatingFromURL = searchParams.get("new") === "project";

  function setCreateProjectOpen(open: boolean) {
    setCreating(open);
    if (open || !creatingFromURL) return;

    const next = new URLSearchParams(searchParams);
    next.delete("new");
    setSearchParams(next, { replace: true });
  }

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
            <>
              {/* 移动端 (lg:hidden)：顶部分段 Tabs + 左右平滑滚动单列容器 */}
              {!isDesktop && (
                <div className="flex min-h-0 flex-1 flex-col">
                {/* 顶部吸顶分段器 Tabs */}
                <div className="shrink-0 pb-3">
                  <div className="flex items-center gap-1.5 overflow-x-auto rounded-xl border border-line bg-muted/50 p-1.5 scrollbar-none">
                    {states.map((state) => {
                      const isActive = activeTab === state;
                      const count = cardsByState[state].length;
                      const accent = laneAccentMap[state];
                      return (
                        <button
                          key={state}
                          type="button"
                          onClick={() => handleTabSelect(state)}
                          className={
                            "flex flex-1 items-center justify-center gap-1.5 whitespace-nowrap rounded-lg px-2.5 py-1.5 text-xs font-medium transition-all " +
                            (isActive
                              ? accent.activeTab
                              : "text-muted-foreground hover:bg-muted hover:text-foreground")
                          }
                        >
                          <span
                            className={
                              "size-1.5 shrink-0 rounded-full transition-opacity " +
                              accent.dot +
                              (isActive ? " opacity-100" : " opacity-40")
                            }
                          />
                          <span>{t(labels[state])}</span>
                          <span
                            className={
                              "ml-0.5 rounded-full px-1.5 py-0.2 text-[10px] tabular-nums " +
                              (isActive
                                ? "bg-muted font-semibold text-foreground"
                                : "bg-muted/70 text-muted-foreground")
                            }
                          >
                            {count}
                          </span>
                        </button>
                      );
                    })}
                  </div>
                </div>

                {/* 移动端左右滑动单列卡片平铺容器 */}{/* w-full snap-center 配合 px-0.5，在右侧留足间隙与边界 */}
                <div
                  ref={mobileContainerRef}
                  onScroll={handleMobileScroll}
                  className="flex min-h-0 flex-1 snap-x snap-mandatory overflow-x-auto overflow-y-hidden scroll-smooth scrollbar-none"
                >
                  {states.map((state) => (
                    <div
                      key={state}
                      className="min-h-0 w-full shrink-0 snap-center snap-always px-0.5 pb-2"
                    >
                      <Lane
                        state={state}
                        cards={cardsByState[state]}
                        onOpen={open}
                        className="h-full"
                        hasMoreDone={state === "done" && hasMoreDone}
                        loadingMoreDone={state === "done" && loadingMoreDone}
                        onLoadMoreDone={handleLoadMoreDone}
                      />
                    </div>
                  ))}
                </div>
              </div>
              )}

              {/* 桌面端 (hidden lg:flex)：成熟的 4 列并行看板与拖拽视图 */}
              {isDesktop && (
                <div className="flex min-h-0 flex-1 flex-col">
                <DndContext sensors={sensors} onDragEnd={dragEnd}>
                  <div className="grid min-h-0 flex-1 grid-cols-4 gap-3.5 overflow-visible">
                    {states.map((state) => (
                      <Lane
                        key={state}
                        state={state}
                        cards={cardsByState[state]}
                        onOpen={open}
                        hasMoreDone={state === "done" && hasMoreDone}
                        loadingMoreDone={state === "done" && loadingMoreDone}
                        onLoadMoreDone={handleLoadMoreDone}
                      />
                    ))}
                  </div>
                </DndContext>
              </div>
              )}
            </>
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
