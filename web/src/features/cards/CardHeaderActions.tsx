import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import {
  Archive,
  FileText,
  MoreHorizontal,
  Pencil,
  Save,
  Trash2,
} from "lucide-react";
import { toast } from "sonner";
import { api } from "../../shared/api/client";
import type { Card, CardDetail } from "../../shared/api/types";
import { keys, queryClient } from "../../app/query";
import { TaskDetailsPanel } from "./TaskDetailsPanel";
import {
  Button,
  ConfirmDialog,
  Field,
  Menu,
  MenuItem,
  MenuSeparator,
  Modal,
  TextArea,
  TextInput,
} from "../../shared/ui/components";

export function CardHeaderActions({
  detail,
  projectCards,
  onDeleted,
}: {
  detail: CardDetail;
  projectCards: Card[];
  onDeleted: () => void;
}) {
  const card = detail.card;
  const [detailsOpen, setDetailsOpen] = useState(false);
  const [editOpen, setEditOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [title, setTitle] = useState(card.title);
  const [description, setDescription] = useState(card.description);

  const sync = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: keys.card(card.id) }),
      queryClient.invalidateQueries({ queryKey: keys.cards(card.project_id) }),
    ]);
  };

  const update = useMutation({
    mutationFn: () => api.updateCard(card.id, title.trim(), description.trim()),
    onSuccess: async () => {
      await sync();
      setEditOpen(false);
      toast.success("已保存");
    },
    onError: showError,
  });

  const archive = useMutation({
    mutationFn: () => api.archiveDiscussion(card.id),
    onSuccess: async () => {
      await sync();
      toast.success("讨论已归档");
    },
    onError: showError,
  });

  const remove = useMutation({
    mutationFn: () => api.deleteDiscussion(card.id),
    onSuccess: async () => {
      await queryClient.invalidateQueries({
        queryKey: keys.cards(card.project_id),
      });
      onDeleted();
    },
    onError: showError,
  });

  function openEditor() {
    setTitle(card.title);
    setDescription(card.description);
    setEditOpen(true);
  }

  return (
    <>
      <Menu
        trigger={
          <button
            type="button"
            title="更多操作"
            aria-label="更多操作"
            className="grid size-8 place-items-center rounded-lg text-slate-400 transition hover:bg-slate-100 hover:text-slate-700 data-[state=open]:bg-slate-100 data-[state=open]:text-slate-700"
          >
            <MoreHorizontal className="size-[18px]" />
          </button>
        }
      >
        {card.kind !== "discussion" && (
          <MenuItem onSelect={() => setDetailsOpen(true)}>
            <FileText className="size-4" />
            任务详情
          </MenuItem>
        )}
        <MenuItem onSelect={openEditor}>
          <Pencil className="size-4" />
          编辑信息
        </MenuItem>

        {card.kind === "discussion" && (
          <>
            {card.discussion_state === "active" && (
              <MenuItem
                disabled={archive.isPending}
                onSelect={() => archive.mutate()}
              >
                <Archive className="size-4" />
                归档讨论
              </MenuItem>
            )}
            <MenuSeparator />
            <MenuItem
              danger
              disabled={remove.isPending}
              onSelect={() => setDeleteOpen(true)}
            >
              <Trash2 className="size-4" />
              删除讨论
            </MenuItem>
          </>
        )}
      </Menu>

      {card.kind !== "discussion" && (
        <Modal
          open={detailsOpen}
          onOpenChange={setDetailsOpen}
          title="任务详情"
          description="查看任务目标、来源关系，以及持续事项的健康状态和定时日程。"
        >
          <TaskDetailsPanel
            detail={detail}
            projectCards={projectCards}
            onChange={sync}
          />
        </Modal>
      )}

      <Modal
        open={editOpen}
        onOpenChange={setEditOpen}
        title={card.kind === "discussion" ? "编辑讨论信息" : "编辑任务信息"}
        description="标题和描述会作为后续 Agent 执行时的长期上下文。"
      >
        <div className="space-y-4">
          <Field label="标题">
            <TextInput
              autoFocus
              value={title}
              onChange={(event) => setTitle(event.target.value)}
              placeholder={card.kind === "discussion" ? "讨论标题" : "任务标题"}
            />
          </Field>
          <Field label="描述">
            <TextArea
              value={description}
              onChange={(event) => setDescription(event.target.value)}
              rows={5}
              placeholder="目标、范围与必要背景"
            />
          </Field>
          <div className="flex justify-end gap-2 pt-1">
            <Button
              disabled={update.isPending}
              onClick={() => setEditOpen(false)}
            >
              取消
            </Button>
            <Button
              variant="primary"
              disabled={!title.trim() || update.isPending}
              onClick={() => update.mutate()}
            >
              <Save className="size-4" />
              保存
            </Button>
          </div>
        </div>
      </Modal>

      <ConfirmDialog
        open={deleteOpen}
        onOpenChange={setDeleteOpen}
        title="删除这个讨论？"
        description="已生成的 Todo / Ongoing 不会被删除，但这个讨论及其会话内容将不再保留。"
        confirmLabel="删除讨论"
        pending={remove.isPending}
        onConfirm={() => remove.mutate()}
      />
    </>
  );
}

function showError(error: unknown) {
  toast.error(error instanceof Error ? error.message : "操作失败");
}
