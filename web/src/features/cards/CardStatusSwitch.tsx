import { useMutation } from "@tanstack/react-query";
import { Check, ChevronDown } from "lucide-react";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import { api } from "../../shared/api/client";
import { keys, queryClient } from "../../app/query";
import type { Card, TodoState, OngoingState } from "../../shared/api/types";
import { Menu, MenuItem } from "../../shared/ui/components";

const todoOptions: { value: TodoState; label: string }[] = [
  { value: "todo", label: "待办" },
  { value: "working", label: "进行中" },
  { value: "review", label: "待审核" },
  { value: "done", label: "已完成" },
];

const ongoingOptions: { value: OngoingState; label: string }[] = [
  { value: "active", label: "运行中" },
  { value: "paused", label: "已暂停" },
  { value: "archived", label: "已归档" },
];

export function CardStatusSwitch({ card }: { card: Card }) {
  const { t } = useTranslation();
  const isTodo = card.kind === "todo";
  const current = isTodo ? card.todo_state : card.ongoing_state;
  const options = isTodo ? todoOptions : ongoingOptions;

  const update = useMutation({
    mutationFn: (state: string) =>
      isTodo
        ? api.moveTodo(card.id, state as TodoState)
        : api.ongoingState(card.id, state),
    onSuccess: async () => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: keys.card(card.id) }),
        queryClient.invalidateQueries({ queryKey: keys.cards(card.project_id) }),
      ]);
    },
    onError: (error) =>
      toast.error(error instanceof Error ? error.message : t("状态更新失败")),
  });

  if (card.kind === "discussion") return null;

  return (
    <Menu
      align="start"
      trigger={
        <button
          type="button"
          aria-label={t("切换任务状态")}
          title={t("切换任务状态")}
          className="flex h-8 items-center gap-1.5 rounded-lg border border-slate-200 bg-slate-50 px-2.5 text-xs font-medium text-slate-700 hover:border-slate-300 hover:bg-slate-100 data-[state=open]:border-slate-300 data-[state=open]:bg-slate-100"
        >
          <span className={`size-1.5 rounded-full ${current === "done" || current === "active" ? "bg-emerald-500" : current === "review" ? "bg-amber-500" : current === "working" ? "bg-sky-500" : "bg-slate-400"}`} />
          <span>{t(options.find((option) => option.value === current)?.label || "待办")}</span>
          <ChevronDown className="size-3 text-slate-400" />
        </button>
      }
    >
      {options.map((option) => {
        const disabled =
          update.isPending ||
          option.value === current ||
          (isTodo && option.value === "done" && current !== "review");
        return (
          <MenuItem
            key={option.value}
            disabled={disabled}
            onSelect={() => update.mutate(option.value)}
          >
            <span className="flex-1">{t(option.label)}</span>
            {option.value === current && <Check className="size-3.5" />}
          </MenuItem>
        );
      })}
    </Menu>
  );
}
