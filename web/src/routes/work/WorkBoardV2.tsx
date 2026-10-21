/* oxlint-disable react/refs -- dnd-kit 的 transform/isOver 是 hook 返回的渲染状态；callback ref 按其官方用法注册节点，React Compiler 不应将其错误判作访问 ref.current。 */
import { useTranslation } from "react-i18next";
import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { useNavigate, useSearchParams } from "react-router-dom";
import {
  DndContext,
  PointerSensor,
  useDraggable,
  useDroppable,
  useSensor,
  useSensors,
  type DragEndEvent,
} from "@dnd-kit/core";
import { CSS } from "@dnd-kit/utilities";
import { Plus, GripVertical, ListChecks } from "lucide-react";
import { toast } from "sonner";
import { api } from "../../shared/api/client";
import type { Card, Project, TodoState } from "../../shared/api/types";
import { keys, queryClient } from "../../app-v2/query";
import {
  Button,
  Input,
  LoadingButton,
  Textarea,
  Typography,
  Badge,
} from "../../components/ui/controls";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
} from "../../components/ui/overlays";
import { EmptyState } from "../../components/ui/feedback";
import { PageHeader } from "../../components/layout/Scaffold";

const columns: { state: TodoState; label: string }[] = [
  { state: "todo", label: "待办" },
  { state: "working", label: "进行中" },
  { state: "review", label: "待审核" },
  { state: "done", label: "已完成" },
];

function TodoTile({ card, onOpen }: { card: Card; onOpen: () => void }) {
  const { t } = useTranslation();
  const drag = useDraggable({ id: card.id, data: { state: card.todo_state } });
  return (
    <div
      ref={drag.setNodeRef}
      className="v2-panel flex min-w-0 items-start gap-2 p-3"
      style={{
        transform: CSS.Translate.toString(drag.transform),
        opacity: drag.isDragging ? 0.6 : 1,
      }}
    >
      <button
        {...drag.listeners}
        {...drag.attributes}
        className="mt-0.5 cursor-grab touch-none text-[var(--v2-muted)]"
        aria-label={`${t("拖动")} ${card.title}`}
        title={t("拖动至目标状态")}
      >
        <GripVertical size={15} />
      </button>
      <button className="min-w-0 flex-1 text-left" onClick={onOpen}>
        <Typography scale="body" className="break-words font-semibold">
          {card.title}
        </Typography>
        {card.description && (
          <Typography scale="meta" className="mt-1 line-clamp-2">
            {card.description}
          </Typography>
        )}
        {card.current_run_status && (
          <Badge tone="accent" className="mt-2">
            {card.current_run_status}
          </Badge>
        )}
      </button>
    </div>
  );
}

function TodoColumn({
  state,
  label,
  cards,
  onOpen,
}: {
  state: TodoState;
  label: string;
  cards: Card[];
  onOpen: (id: string) => void;
}) {
  const { t } = useTranslation();
  const drop = useDroppable({ id: state });
  return (
    <div
      ref={drop.setNodeRef}
      className="min-w-[238px] rounded-[var(--v2-radius-lg)] bg-[var(--v2-subtle)] p-3"
      data-column={state}
      style={{
        outline: drop.isOver ? "2px solid var(--v2-accent)" : undefined,
      }}
    >
      <div className="mb-3 flex items-center justify-between gap-2">
        <Typography as="h3" scale="section">
          {t(label)}
        </Typography>
        <Badge>{cards.length}</Badge>
      </div>
      <div className="min-h-24 space-y-2">
        {cards.length === 0 ? (
          <p className="v2-text-meta px-1 py-4">{t("暂无任务")}</p>
        ) : (
          cards.map((card) => (
            <TodoTile
              key={card.id}
              card={card}
              onOpen={() => onOpen(card.id)}
            />
          ))
        )}
      </div>
    </div>
  );
}

function NewTodo({
  open,
  onOpenChange,
  projectID,
}: {
  open: boolean;
  onOpenChange: (v: boolean) => void;
  projectID: string;
}) {
  const { t } = useTranslation();
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const navigate = useNavigate();
  const create = useMutation({
    mutationFn: () =>
      api.createTodo(projectID, title.trim(), description.trim()),
    onSuccess: async (card) => {
      await queryClient.invalidateQueries({ queryKey: keys.cards(projectID) });
      onOpenChange(false);
      setTitle("");
      setDescription("");
      navigate(`/projects/${projectID}/work/${card.id}`);
    },
    onError: (error) => toast.error(String(error)),
  });
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogTitle className="v2-text-section">{t("新建待办")}</DialogTitle>
        <DialogDescription className="v2-text-control mt-1 text-[var(--v2-muted)]">
          {t("独立创建的待办不需要来源 Discussion。")}
        </DialogDescription>
        <form
          className="mt-4 space-y-3"
          onSubmit={(e) => {
            e.preventDefault();
            if (title.trim()) create.mutate();
          }}
        >
          <label className="block space-y-1">
            <Typography scale="control">{t("标题")}</Typography>
            <Input
              required
              value={title}
              onChange={(e) => setTitle(e.target.value)}
            />
          </label>
          <label className="block space-y-1">
            <Typography scale="control">{t("任务说明")}</Typography>
            <Textarea
              rows={3}
              value={description}
              onChange={(e) => setDescription(e.target.value)}
            />
          </label>
          <div className="flex justify-end gap-2">
            <Button onClick={() => onOpenChange(false)} type="button">
              {t("取消")}
            </Button>
            <LoadingButton
              type="submit"
              variant="primary"
              loading={create.isPending}
              disabled={!title.trim()}
            >
              {t("创建待办")}
            </LoadingButton>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

export function WorkBoardV2({
  project,
  cards,
}: {
  project: Project;
  cards: Card[];
}) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [search] = useSearchParams();
  const [createOpen, setCreateOpen] = useState(false);
  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 7 } }),
  );
  const move = useMutation({
    mutationFn: ({ id, state }: { id: string; state: TodoState }) =>
      api.moveTodo(id, state),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: keys.cards(project.id) });
    },
    onError: (error) => {
      toast.error(t("任务状态变更失败：") + String(error));
      void queryClient.invalidateQueries({ queryKey: keys.cards(project.id) });
    },
  });
  const todos = cards.filter((c) => c.kind === "todo");
  const ongoing = cards.filter((c) => c.kind === "ongoing");
  const view = search.get("view") || "";
  const visible =
    view === "review" ? todos.filter((c) => c.todo_state === "review") : todos;
  const onDragEnd = ({ active, over }: DragEndEvent) => {
    if (!over || !columns.some((c) => c.state === over.id)) return;
    const card = todos.find((c) => c.id === active.id);
    if (!card || card.todo_state === over.id) return;
    move.mutate({ id: card.id, state: over.id as TodoState });
  };
  return (
    <div className="min-w-0 space-y-7 p-4 md:p-8">
      <PageHeader
        title={
          view === "review"
            ? t("待我审核")
            : view === "ongoing"
              ? t("持续事项")
              : t("工作")
        }
        description={project.name}
        actions={
          <Button variant="primary" onClick={() => setCreateOpen(true)}>
            <Plus size={15} />
            {t("新建待办")}
          </Button>
        }
      />
      {view !== "ongoing" && (
        <section className="min-w-0">
          <div className="mb-3 flex items-center gap-2">
            <ListChecks size={17} color="var(--v2-muted)" />
            <Typography as="h2" scale="section">
              {t("待办看板")}
            </Typography>
          </div>
          <DndContext sensors={sensors} onDragEnd={onDragEnd}>
            <div
              className="min-w-0 overflow-x-auto pb-3"
              aria-label={t("待办看板")}
            >
              <div className="grid min-w-[980px] grid-cols-4 gap-3">
                {columns.map((col) => (
                  <TodoColumn
                    key={col.state}
                    state={col.state}
                    label={col.label}
                    cards={visible.filter(
                      (c) => (c.todo_state || "todo") === col.state,
                    )}
                    onOpen={(id) =>
                      navigate(`/projects/${project.id}/work/${id}`)
                    }
                  />
                ))}
              </div>
            </div>
          </DndContext>
        </section>
      )}
      {view !== "review" && (
        <section className="space-y-3">
          <Typography as="h2" scale="section">
            {t("持续事项")}
          </Typography>
          {ongoing.length ? (
            <div className="grid min-w-0 gap-3 sm:grid-cols-2 lg:grid-cols-3">
              {ongoing.map((c) => (
                <button
                  key={c.id}
                  className="v2-panel min-w-0 space-y-2 p-4 text-left hover:border-[var(--v2-accent)]"
                  onClick={() =>
                    navigate(`/projects/${project.id}/work/${c.id}`)
                  }
                >
                  <Typography scale="section" className="break-words">
                    {c.title}
                  </Typography>
                  {c.description && (
                    <Typography scale="control" className="line-clamp-2">
                      {c.description}
                    </Typography>
                  )}
                  <div className="flex flex-wrap gap-2">
                    <Badge
                      tone={c.ongoing_state === "active" ? "accent" : "neutral"}
                    >
                      {c.ongoing_state === "active"
                        ? t("运行中")
                        : c.ongoing_state === "paused"
                          ? t("已暂停")
                          : t("已归档")}
                    </Badge>
                    <Badge>{c.health || t("尚无检查")}</Badge>
                  </div>
                </button>
              ))}
            </div>
          ) : (
            <div className="v2-panel">
              <EmptyState
                title={t("暂无持续事项")}
                description={t("可从讨论生成长期或周期工作。")}
              />
            </div>
          )}
        </section>
      )}
      <NewTodo
        open={createOpen}
        onOpenChange={setCreateOpen}
        projectID={project.id}
      />
    </div>
  );
}
