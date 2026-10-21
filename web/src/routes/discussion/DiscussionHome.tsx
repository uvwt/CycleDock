import { useTranslation } from "react-i18next";
import { useEffect, useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { useNavigate } from "react-router-dom";
import { ArrowUp, MessageSquarePlus } from "lucide-react";
import { toast } from "sonner";
import { api } from "../../shared/api/client";
import type { Card, Project } from "../../shared/api/types";
import { keys, queryClient } from "../../app-v2/query";
import {
  Button,
  LoadingButton,
  Textarea,
  Typography,
} from "../../components/ui/controls";
import { EmptyState } from "../../components/ui/feedback";
import { PageHeader } from "../../components/layout/Scaffold";

type FirstMessageDraft = { text: string; cardID?: string; key: string };
function newDraft(): FirstMessageDraft {
  return { text: "", key: crypto.randomUUID() };
}
function readDraft(projectID: string): FirstMessageDraft {
  try {
    const value = sessionStorage.getItem(
      "cycledock:discussion-draft:" + projectID,
    );
    const parsed = value ? (JSON.parse(value) as FirstMessageDraft) : null;
    if (
      parsed &&
      typeof parsed.text === "string" &&
      typeof parsed.key === "string"
    )
      return parsed;
  } catch {
    /* 会话存储损坏或不可用时只使用内存草稿。 */
  }
  return newDraft();
}

export function DiscussionHome({
  project,
  cards,
}: {
  project: Project;
  cards: Card[];
}) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [draft, setDraft] = useState<FirstMessageDraft>(() =>
    readDraft(project.id),
  );
  const [activeProject, setActiveProject] = useState(project.id);
  // 项目切换须按项目隔离草稿，不能将上个项目的首条留言发往新项目。
  if (activeProject !== project.id) {
    setActiveProject(project.id);
    setDraft(readDraft(project.id));
  }
  useEffect(() => {
    try {
      sessionStorage.setItem(
        "cycledock:discussion-draft:" + project.id,
        JSON.stringify(draft),
      );
    } catch {
      /* 不影响普通发送 */
    }
  }, [draft, project.id]);
  const send = useMutation({
    mutationFn: async () => {
      if (!draft.text.trim()) throw new Error(t("请先输入讨论内容"));
      const cardID =
        draft.cardID ?? (await api.createDiscussion(project.id)).id;
      if (!draft.cardID) {
        const updated = { ...draft, cardID };
        setDraft(updated);
        try {
          sessionStorage.setItem(
            "cycledock:discussion-draft:" + project.id,
            JSON.stringify(updated),
          );
        } catch {
          /*内存草稿仍在*/
        }
      }
      await api.send(cardID, draft.text, "enqueue", draft.key);
      return cardID;
    },
    onSuccess: async (id) => {
      const fresh = newDraft();
      setDraft(fresh);
      try {
        sessionStorage.removeItem("cycledock:discussion-draft:" + project.id);
      } catch {
        /* ignore */
      }
      await queryClient.invalidateQueries({ queryKey: keys.cards(project.id) });
      navigate(`/projects/${project.id}/discussions/${id}`);
    },
    onError: (error) =>
      toast.error(t("未能发送，草稿已保留，可重试：") + String(error)),
  });
  return (
    <div className="mx-auto flex min-h-full w-full max-w-4xl flex-col px-4 py-6 md:px-8">
      <PageHeader title={t("讨论")} description={project.name} />
      <div className="flex flex-1 flex-col items-center justify-center gap-4 py-12 text-center">
        <MessageSquarePlus
          size={30}
          color="var(--v2-accent)"
          aria-hidden="true"
        />
        <Typography as="h2" scale="title">
          {t("今天想推进什么？")}
        </Typography>
        <Typography scale="body" className="max-w-lg text-[var(--v2-muted)]">
          {t("先和 Agent 讨论需求，明确目标后再生成待办或持续事项。")}
        </Typography>
        <form
          className="v2-panel mt-3 w-full max-w-2xl p-3 text-left"
          onSubmit={(event) => {
            event.preventDefault();
            if (!send.isPending) send.mutate();
          }}
        >
          <Textarea
            aria-label={t("新讨论内容")}
            placeholder={t("描述问题、想法或准备推进的工作…")}
            rows={3}
            value={draft.text}
            onChange={(e) => setDraft((v) => ({ ...v, text: e.target.value }))}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !e.shiftKey) {
                e.preventDefault();
                if (!send.isPending) send.mutate();
              }
            }}
          />
          <div className="mt-3 flex items-center justify-between gap-3">
            <Typography scale="meta">
              {draft.cardID
                ? t("首次发送尚未完成，重试将继续同一讨论")
                : t("Enter 发送 · Shift+Enter 换行")}
            </Typography>
            <LoadingButton
              type="submit"
              variant="primary"
              disabled={!draft.text.trim()}
              loading={send.isPending}
            >
              <ArrowUp size={15} />
              {t("发送")}
            </LoadingButton>
          </div>
        </form>
        {send.isError && (
          <Typography scale="control" className="text-[var(--v2-danger)]">
            {t("发送失败，内容与目标 Discussion 已保留。")}
          </Typography>
        )}
      </div>
      {!cards.some((card) => card.kind === "discussion") && (
        <EmptyState
          title={t("还没有讨论")}
          description={t("发送上面的第一条消息即可开始。")}
        />
      )}
      {cards.some((card) => card.kind === "discussion") && (
        <div className="border-t border-[var(--v2-border)] pt-4">
          <Typography as="h3" scale="section">
            {t("最近讨论")}
          </Typography>
          <div className="mt-2 grid gap-2 sm:grid-cols-2">
            {cards
              .filter((c) => c.kind === "discussion")
              .slice(0, 6)
              .map((c) => (
                <Button
                  key={c.id}
                  variant="ghost"
                  className="justify-start text-left"
                  onClick={() =>
                    navigate(`/projects/${project.id}/discussions/${c.id}`)
                  }
                >
                  {c.title || t("未命名讨论")}
                </Button>
              ))}
          </div>
        </div>
      )}
    </div>
  );
}
