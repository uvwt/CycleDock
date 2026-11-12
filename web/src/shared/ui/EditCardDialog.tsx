import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Save } from "lucide-react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { api } from "../api/client";
import type { Card } from "../api/types";
import { keys } from "../../app/query";
import { patchCachedCard } from "../lib/card-cache";
import { Button, Field, Modal, TextArea, TextInput } from "./components";

export interface EditCardDialogProps {
  card: Card | null;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onSaved?: (updatedCard: Card) => void;
}

function EditCardDialogContent({
  card,
  open,
  onOpenChange,
  onSaved,
}: {
  card: Card;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onSaved?: (updatedCard: Card) => void;
}) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const [title, setTitle] = useState(card.title || "");
  const [description, setDescription] = useState(card.description || "");

  const updateMutation = useMutation({
    mutationFn: () => api.updateCard(card.id, title.trim(), description.trim()),
    onSuccess: (updatedCard) => {
      patchCachedCard(queryClient, card.id, card.project_id, updatedCard);
      void queryClient.invalidateQueries({ queryKey: keys.card(card.id) });
      void queryClient.invalidateQueries({
        queryKey: keys.cards(card.project_id),
      });
      void queryClient.invalidateQueries({
        queryKey: keys.schedules(card.project_id),
      });
      toast.success(t("已保存"));
      onSaved?.(updatedCard);
      onOpenChange(false);
    },
    onError: (err) => {
      toast.error(err instanceof Error ? err.message : t("保存失败"));
    },
  });

  const dialogTitle =
    card.kind === "discussion"
      ? t("编辑讨论")
      : card.kind === "ongoing"
        ? t("编辑定时任务")
        : t("编辑任务");

  const titlePlaceholder =
    card.kind === "discussion"
      ? t("讨论标题")
      : card.kind === "ongoing"
        ? t("定时任务标题")
        : t("任务标题");

  return (
    <Modal
      open={open}
      onOpenChange={(next) => {
        if (!updateMutation.isPending) {
          onOpenChange(next);
        }
      }}
      title={dialogTitle}
    >
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (title.trim() && !updateMutation.isPending) {
            updateMutation.mutate();
          }
        }}
        className="space-y-4"
      >
        <Field label={t("标题")}>
          <TextInput
            autoFocus
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            placeholder={titlePlaceholder}
            disabled={updateMutation.isPending}
          />
        </Field>
        <Field label={t("描述")}>
          <TextArea
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            rows={5}
            placeholder={t("目标、范围与必要背景")}
            disabled={updateMutation.isPending}
          />
        </Field>
        <div className="flex justify-end gap-2 pt-1">
          <Button
            type="button"
            disabled={updateMutation.isPending}
            onClick={() => onOpenChange(false)}
          >
            {t("取消")}
          </Button>
          <Button
            type="submit"
            variant="primary"
            disabled={!title.trim() || updateMutation.isPending}
          >
            <Save className="size-4" />
            {updateMutation.isPending ? t("保存中…") : t("保存")}
          </Button>
        </div>
      </form>
    </Modal>
  );
}

/**
 * 卡片编辑弹窗（统一用于讨论、任务与定时任务的基本信息编辑）
 */
export function EditCardDialog({
  card,
  open,
  onOpenChange,
  onSaved,
}: EditCardDialogProps) {
  if (!card || !open) return null;
  return (
    <EditCardDialogContent
      card={card}
      open={open}
      onOpenChange={onOpenChange}
      onSaved={onSaved}
    />
  );
}
