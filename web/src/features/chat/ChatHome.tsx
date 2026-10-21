import { useMemo, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { Link, useNavigate } from "react-router-dom";
import {
  MessageSquareText,
  Plus,
  Search,
  ArrowUpRight,
  FolderKanban,
} from "lucide-react";
import { toast } from "sonner";
import { api } from "../../shared/api/client";
import { keys, queryClient } from "../../app/query";
import { Button, ErrorView, Spinner } from "../../shared/ui/components";
import type { Card } from "../../shared/api/types";

export default function ChatHome() {
  const navigate = useNavigate();
  const projects = useQuery({ queryKey: keys.projects, queryFn: api.projects });
  const [selected, setSelected] = useState("");
  const [term, setTerm] = useState("");
  const projectID = projects.data?.some((p) => p.id === selected)
    ? selected
    : projects.data?.[0]?.id;
  const cards = useQuery({
    queryKey: keys.cards(projectID || ""),
    queryFn: () => api.cards(projectID!),
    enabled: !!projectID,
  });
  const discussions = useMemo(
    () =>
      ((cards.data || []).filter((x) => x.kind === "discussion") as Card[])
        .filter((x) =>
          (x.title + " " + x.description)
            .toLowerCase()
            .includes(term.trim().toLowerCase()),
        )
        .sort((a, b) => b.updated_at.localeCompare(a.updated_at)),
    [cards.data, term],
  );
  const create = useMutation({
    mutationFn: () => api.createDiscussion(projectID!),
    onSuccess: async (c) => {
      await queryClient.invalidateQueries({ queryKey: keys.cards(projectID!) });
      navigate(`/chat/${projectID}/${c.id}`);
    },
    onError: (e) =>
      toast.error(e instanceof Error ? e.message : "创建讨论失败"),
  });
  return (
    <main className="mx-auto max-w-6xl space-y-8 px-5 py-9 md:px-8">
      <div className="flex flex-wrap items-start justify-between gap-5">
        <div>
          <p className="text-xs font-semibold tracking-widest text-sky-700">
            DISCUSSIONS
          </p>
          <h1 className="mt-2 text-3xl font-semibold tracking-tight text-slate-900">
            聊天
          </h1>
          <p className="mt-2 text-sm text-slate-500">
            从一个想法开始，与 Agent 讨论，再把方向变成任务。
          </p>
        </div>
        <Button
          variant="primary"
          disabled={!projectID || create.isPending}
          onClick={() => create.mutate()}
        >
          <Plus className="size-4" />
          新建讨论
        </Button>
      </div>
      {projects.isPending && <Spinner text="读取项目…" />}
      {projects.error && <ErrorView error={projects.error} />}
      {projectID && (
        <>
          <div className="flex flex-col gap-3 sm:flex-row">
            <label className="relative w-full sm:w-56">
              <span className="sr-only">项目</span>
              <FolderKanban className="pointer-events-none absolute top-3 left-3 size-4 text-slate-400" />
              <select
                aria-label="项目"
                value={projectID}
                onChange={(e) => setSelected(e.target.value)}
                className="w-full appearance-none rounded-xl border border-slate-200 bg-white py-2.5 pr-8 pl-10 text-sm"
              >
                {projects.data?.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.name}
                  </option>
                ))}
              </select>
            </label>
            <label className="relative flex-1">
              <Search className="pointer-events-none absolute top-3 left-3 size-4 text-slate-400" />
              <input
                value={term}
                onChange={(e) => setTerm(e.target.value)}
                placeholder="搜索讨论…"
                className="w-full rounded-xl border border-slate-200 bg-white py-2.5 pr-3 pl-10 text-sm outline-none focus:border-sky-500"
              />
            </label>
          </div>
          {cards.isPending ? (
            <Spinner text="读取讨论…" />
          ) : cards.error ? (
            <ErrorView
              error={cards.error}
              onRetry={() => void cards.refetch()}
            />
          ) : discussions.length === 0 ? (
            <div className="flex min-h-64 flex-col items-center justify-center rounded-3xl border border-dashed border-slate-200 bg-white text-center">
              <MessageSquareText className="size-9 text-slate-300" />
              <h2 className="mt-4 font-medium">还没有讨论</h2>
              <p className="mt-1 text-sm text-slate-500">
                新建一个讨论，直接发送第一条消息。
              </p>
              <Button
                className="mt-5"
                variant="primary"
                onClick={() => create.mutate()}
              >
                <Plus className="size-4" />
                开始讨论
              </Button>
            </div>
          ) : (
            <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
              {discussions.map((card) => (
                <Link
                  to={`/chat/${projectID}/${card.id}`}
                  key={card.id}
                  className="group flex min-h-44 flex-col justify-between rounded-2xl border border-slate-200 bg-white p-5 transition hover:border-sky-300 hover:shadow-sm"
                >
                  <div>
                    <div className="flex items-start justify-between gap-3">
                      <MessageSquareText className="size-5 text-sky-700" />
                      <ArrowUpRight className="size-4 text-slate-300 group-hover:text-sky-700" />
                    </div>
                    <h2 className="mt-4 line-clamp-2 font-semibold text-slate-900">
                      {card.title || "新讨论"}
                    </h2>
                    <p className="mt-1 line-clamp-2 text-sm leading-6 text-slate-500">
                      {card.description || "继续对话，探索想法…"}
                    </p>
                  </div>
                  <div className="mt-4 flex items-center justify-between text-xs text-slate-400">
                    <span>
                      {card.discussion_state === "archived"
                        ? "已归档"
                        : "讨论中"}
                    </span>
                    <span>
                      {new Date(card.updated_at).toLocaleDateString()}
                    </span>
                  </div>
                </Link>
              ))}
            </div>
          )}
        </>
      )}
      {projects.data?.length === 0 && (
        <div className="rounded-2xl border border-slate-200 bg-white p-8 text-center">
          <p>还没有项目，请先在看板中创建一个项目。</p>
          <Link className="mt-4 inline-block text-sm text-sky-700" to="/board">
            前往看板
          </Link>
        </div>
      )}
    </main>
  );
}
