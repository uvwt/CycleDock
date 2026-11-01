import { useMutation } from "@tanstack/react-query";
import { Check, ChevronDown } from "lucide-react";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import { api } from "../../shared/api/client";
import { keys, queryClient } from "../../app/query";
import type { Card, TodoState, OngoingState } from "../../shared/api/types";
import { Menu, MenuItem } from "../../shared/ui/components";

const todoOptions: { value: TodoState; label: string; dotClassName: string }[] =
  [
    { value: "todo", label: "待办", dotClassName: "bg-slate-400" },
    { value: "working", label: "进行中", dotClassName: "bg-sky-500" },
    { value: "review", label: "待审核", dotClassName: "bg-amber-500" },
    { value: "done", label: "已完成", dotClassName: "bg-emerald-500" },
  ];

const ongoingOptions: {
  value: OngoingState;
  label: string;
  dotClassName: string;
}[] = [
  { value: "active", label: "运行中", dotClassName: "bg-emerald-500" },
  { value: "paused", label: "已暂停", dotClassName: "bg-amber-500" },
  { value: "archived", label: "已归档", dotClassName: "bg-slate-400" },
];

export function CardStatusSwitch({ card }: { card: Card }) {
  const { t } = useTranslation();
  const isTodo = card.kind === "todo";
  const current = isTodo ? card.todo_state : card.ongoing_state;
  const options = isTodo ? todoOptions : ongoingOptions;
  const selected = options.find((option) => option.value === current);

  const update = useMutation({
    mutationFn: (state: string) =>
      isTodo
        ? api.moveTodo(card.id, state as TodoState)
        : api.ongoingState(card.id, state),
    onSuccess: async () => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: keys.card(card.id) }),
        queryClient.invalidateQueries({
          queryKey: keys.cards(card.project_id),
        }),
        queryClient.invalidateQueries({
          queryKey: keys.schedules(card.project_id),
        }),
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
          className="flex h-8 items-center gap-1.5 rounded-lg border border-line bg-canvas px-2.5 text-xs font-medium text-foreground hover:border-line-strong hover:bg-muted data-[state=open]:border-line-strong data-[state=open]:bg-muted"
        >
          <span
            aria-hidden
            className={`size-1.5 shrink-0 rounded-full ${selected?.dotClassName || "bg-slate-400"}`}
          />
          <span>{t(selected?.label || "待办")}</span>
          <ChevronDown className="size-3 text-faint" />
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
            <span
              aria-hidden
              className={`size-1.5 shrink-0 rounded-full ${option.dotClassName}`}
            />
            <span className="flex-1">{t(option.label)}</span>
            {option.value === current && <Check className="size-3.5" />}
          </MenuItem>
        );
      })}
    </Menu>
  );
}
