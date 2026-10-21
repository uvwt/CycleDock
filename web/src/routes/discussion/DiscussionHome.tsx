import { useTranslation } from "react-i18next";
import { useEffect, useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { useNavigate } from "react-router-dom";
import { ArrowRight, MessageSquareText } from "lucide-react";
import { toast } from "sonner";
import { api } from "../../shared/api/client";
import type { Card, Project } from "../../shared/api/types";
import { keys, queryClient } from "../../app-v2/query";
import { Typography } from "../../components/ui/controls";
import { AdaptiveComposer } from "../../components/ui/AdaptiveComposer";
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
  const [composerExpanded, setComposerExpanded] = useState(false);
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
      setComposerExpanded(false);
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
  const discussions = cards.filter((card) => card.kind === "discussion");
  return (
    <div className="product-discussion-home">
      <div className="product-discussion-inner">
        <PageHeader title={t("讨论")} description={project.name} />
        <section className="product-discussion-launch">
          <h2>{t("今天想推进什么？")}</h2>
          <p className="product-discussion-launch-copy">
            {t("先和 Agent 讨论需求，明确目标后再生成待办或持续事项。")}
          </p>
          <div className="product-discussion-composer">
            <AdaptiveComposer
              value={draft.text}
              onChange={(text) => setDraft((value) => ({ ...value, text }))}
              onSubmit={() => {
                if (!send.isPending) send.mutate();
              }}
              placeholder={t("描述问题、想法或准备推进的工作…")}
              ariaLabel={t("新讨论内容")}
              expanded={composerExpanded}
              onExpandedChange={setComposerExpanded}
              busy={send.isPending}
              sendLabel={t("发送")}
              meta={
                <span>
                  {draft.cardID
                    ? t("首次发送尚未完成，重试将继续同一讨论")
                    : t("Enter 发送 · Shift+Enter 换行")}
                </span>
              }
            />
          </div>
          {send.isError && (
            <Typography
              scale="control"
              className="mt-3 text-[var(--v2-danger)]"
            >
              {t("发送失败，内容与目标 Discussion 已保留。")}
            </Typography>
          )}
        </section>

        {!discussions.length ? (
          <EmptyState
            title={t("还没有讨论")}
            description={t("发送上面的第一条消息即可开始。")}
          />
        ) : (
          <section className="product-recent-section">
            <div className="product-recent-header">
              <Typography as="h3" scale="section">
                {t("最近讨论")}
              </Typography>
              <Typography scale="meta">{project.name}</Typography>
            </div>
            <div className="product-recent-list">
              {discussions.slice(0, 6).map((card) => (
                <button
                  key={card.id}
                  className="product-recent-item"
                  onClick={() =>
                    navigate(
                      `/projects/${project.id}/discussions/${card.id}`,
                    )
                  }
                >
                  <span className="product-recent-item-icon" aria-hidden="true">
                    <MessageSquareText size={15} />
                  </span>
                  <span className="product-recent-item-title">
                    {card.title || t("未命名讨论")}
                  </span>
                  <ArrowRight
                    size={15}
                    className="product-recent-item-arrow"
                    aria-hidden="true"
                  />
                </button>
              ))}
            </div>
          </section>
        )}
      </div>
    </div>
  );
}
