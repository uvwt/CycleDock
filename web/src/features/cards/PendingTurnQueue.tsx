import { useMutation } from "@tanstack/react-query";
import { useState } from "react";
import {
  CornerDownRight,
  ListEnd,
  LoaderCircle,
  Pencil,
  Trash2,
} from "lucide-react";
import { useTranslation } from "react-i18next";
import type { PendingTurn } from "../../shared/api/types";
import { Button, Field, Modal, TextArea } from "../../shared/ui/components";

export function PendingTurnQueue({
  turns,
  cancelingTurnID,
  steeringTurnID,
  onCancel,
  onSteer,
  onEdit,
}: {
  turns: PendingTurn[];
  cancelingTurnID: string;
  steeringTurnID: string;
  onCancel: (turnID: string) => void;
  onSteer: (turnID: string) => void;
  onEdit: (turnID: string, content: string) => Promise<void>;
}) {
  const { t } = useTranslation();
  const [selectedID, setSelectedID] = useState("");
  const [draft, setDraft] = useState("");
  const edit = useMutation({
    mutationFn: ({ turnID, content }: { turnID: string; content: string }) =>
      onEdit(turnID, content),
    onSuccess: () => setSelectedID(""),
  });
  function open(turn: PendingTurn) {
    edit.reset();
    setSelectedID(turn.id);
    setDraft(turn.content);
  }
  const selected = turns.find((turn) => turn.id === selectedID);
  const busy = Boolean(cancelingTurnID || steeringTurnID || edit.isPending);
  const ordered = [...turns].sort(
    (a, b) =>
      Number(b.dispatch_mode === "interrupt") -
        Number(a.dispatch_mode === "interrupt") || a.sequence - b.sequence,
  );
  if (!turns.length) return null;

  return (
    <>
      <section
        aria-label={t("待执行队列")}
        className="mx-3 -mb-3 rounded-t-2xl border border-line bg-surface px-2 pt-1 pb-4"
      >
        <ul className="max-h-48 overflow-y-auto">
          {ordered.map((turn) => (
            <li
              key={turn.id}
              className="flex min-w-0 items-center gap-2 px-1 py-1.5 sm:px-2"
              data-queued-turn={turn.id}
            >
              <ListEnd className="size-4 shrink-0 text-faint" aria-hidden />
              <button
                type="button"
                onClick={() => open(turn)}
                className="min-w-0 flex-1 truncate text-left text-sm text-foreground"
                title={turn.content}
              >
                {turn.content}
              </button>
              {turn.device_node_id && (
                <span
                  className="max-w-20 truncate text-[11px] text-faint"
                  title={turn.device_name || turn.device_node_id}
                >
                  {turn.device_name || turn.device_node_id}
                </span>
              )}
              {turn.source === "human" && (
                <button
                  type="button"
                  disabled={busy || turn.dispatch_mode === "interrupt"}
                  onClick={() => onSteer(turn.id)}
                  aria-label={t("引导")}
                  title={t("立即执行这条排队输入，替代当前执行")}
                  className="flex h-8 shrink-0 items-center gap-1 rounded-lg px-1.5 text-xs text-muted-foreground hover:bg-muted disabled:opacity-40"
                >
                  {steeringTurnID === turn.id ? (
                    <LoaderCircle className="size-4 animate-spin" />
                  ) : (
                    <CornerDownRight className="size-4" />
                  )}
                  <span className="hidden sm:inline">{t("引导")}</span>
                </button>
              )}
              <button
                type="button"
                disabled={busy}
                onClick={() => onCancel(turn.id)}
                aria-label={t("取消排队")}
                title={t("取消排队")}
                className="grid size-8 shrink-0 place-items-center rounded-lg text-muted-foreground hover:bg-muted disabled:opacity-40"
              >
                {cancelingTurnID === turn.id ? (
                  <LoaderCircle className="size-4 animate-spin" />
                ) : (
                  <Trash2 className="size-4" />
                )}
              </button>
              {turn.source === "human" && (
                <button
                  type="button"
                  aria-label={t("编辑内容")}
                  title={t("编辑内容")}
                  disabled={busy}
                  onClick={() => open(turn)}
                  className="grid size-8 shrink-0 place-items-center rounded-lg text-muted-foreground hover:bg-muted disabled:opacity-40"
                >
                  <Pencil className="size-4" />
                </button>
              )}
            </li>
          ))}
        </ul>
      </section>
      <Modal
        open={Boolean(selected)}
        onOpenChange={(open) => {
          if (!open && !edit.isPending) setSelectedID("");
        }}
        title={selected?.source === "human" ? t("编辑内容") : t("排队输入")}
      >
        {selected?.source === "human" ? (
          <form
            className="space-y-4"
            onSubmit={(event) => {
              event.preventDefault();
              if (!draft.trim() || edit.isPending) return;
              edit.mutate({ turnID: selected.id, content: draft.trim() });
            }}
          >
            <Field label={t("排队输入")}>
              <TextArea
                autoFocus
                rows={5}
                value={draft}
                readOnly={edit.isPending}
                onChange={(event) => setDraft(event.target.value)}
              />
            </Field>
            {edit.error && (
              <p role="alert" className="text-sm text-danger">
                {edit.error instanceof Error
                  ? edit.error.message
                  : t("保存失败")}
              </p>
            )}
            <div className="flex justify-end gap-2">
              <Button
                type="button"
                variant="secondary"
                disabled={edit.isPending}
                onClick={() => setSelectedID("")}
              >
                {t("取消")}
              </Button>
              <Button
                type="submit"
                variant="primary"
                disabled={
                  edit.isPending ||
                  !draft.trim() ||
                  draft.trim() === selected.content
                }
              >
                {edit.isPending ? (
                  <LoaderCircle className="size-4 animate-spin" />
                ) : null}
                {t("保存")}
              </Button>
            </div>
          </form>
        ) : (
          <p className="max-h-[60dvh] overflow-y-auto whitespace-pre-wrap break-words text-sm leading-6 text-foreground">
            {selected?.content}
          </p>
        )}
      </Modal>
    </>
  );
}
