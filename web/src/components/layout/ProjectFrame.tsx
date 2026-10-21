import { useTranslation } from "react-i18next";
import { Suspense, lazy, useMemo, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import {
  useLocation,
  useNavigate,
  useParams,
  useSearchParams,
} from "react-router-dom";
import { toast } from "sonner";
import { Menu, Plus, Search, MessageSquareText } from "lucide-react";
import { api } from "../../shared/api/client";
import type { Card, Identity } from "../../shared/api/types";
import { keys, queryClient } from "../../app-v2/query";
import { Button, IconButton, Input, Typography } from "../ui/controls";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetTitle,
  SheetTrigger,
} from "../ui/overlays";
import {
  DelayedSkeleton,
  DiscussionListSkeleton,
  EmptyState,
  ErrorState,
  SidebarSkeleton,
  ThreadSkeleton,
} from "../ui/feedback";
import { Sidebar } from "./Scaffold";
import {
  AccountChooser,
  ProjectChooser,
  ProjectCreate,
  type Mode,
} from "./ProjectSidebarControls";

const DiscussionHome = lazy(() =>
  import("../../routes/discussion/DiscussionHome").then((m) => ({
    default: m.DiscussionHome,
  })),
);
const CardThreadV2 = lazy(() =>
  import("../../routes/card/CardThreadV2").then((m) => ({
    default: m.CardThreadV2,
  })),
);
const WorkBoardV2 = lazy(() =>
  import("../../routes/work/WorkBoardV2").then((m) => ({
    default: m.WorkBoardV2,
  })),
);

type Page = "discussion" | "work" | "card";

function DiscussionNavigation({
  projectID,
  cards,
  onNavigate,
}: {
  projectID: string;
  cards: Card[];
  onNavigate?: () => void;
}) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { cardID } = useParams();
  const [search, setSearch] = useState("");
  const discussions = useMemo(
    () =>
      cards.filter(
        (c) =>
          c.kind === "discussion" &&
          c.title.toLowerCase().includes(search.toLowerCase()),
      ),
    [cards, search],
  );
  const create = useMutation({
    mutationFn: () => api.createDiscussion(projectID),
    onSuccess: async (c) => {
      await queryClient.invalidateQueries({ queryKey: keys.cards(projectID) });
      navigate(`/projects/${projectID}/discussions/${c.id}`);
      onNavigate?.();
    },
    onError: (error) => toast.error(String(error)),
  });
  return (
    <div className="flex min-h-0 flex-col gap-3">
      <div className="flex gap-2">
        <div className="relative min-w-0 flex-1">
          <Search
            size={14}
            className="product-sidebar-search-icon pointer-events-none absolute top-3 left-2"
          />
          <Input
            aria-label={t("搜索讨论")}
            placeholder={t("搜索讨论")}
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="pl-8"
          />
        </div>
        <IconButton
          icon={Plus}
          label={t("新建讨论")}
          variant="secondary"
          disabled={create.isPending}
          onClick={() => create.mutate()}
        />
      </div>
      <nav aria-label={t("讨论列表")} className="min-h-0 space-y-1">
        {!discussions.length && (
          <div className="px-2 py-5 text-[var(--v2-type-control)] text-[var(--v2-muted)]">
            {search ? t("无匹配的讨论") : t("还没有讨论")}
          </div>
        )}
        {discussions.map((c) => (
          <Button
            key={c.id}
            variant={cardID === c.id ? "secondary" : "ghost"}
            size="compact"
            className="w-full justify-start text-left"
            aria-current={cardID === c.id ? "page" : undefined}
            onClick={() => {
              navigate(`/projects/${projectID}/discussions/${c.id}`);
              onNavigate?.();
            }}
          >
            <MessageSquareText size={14} className="shrink-0" />
            <span className="min-w-0 truncate">
              {c.title || t("未命名讨论")}
            </span>
          </Button>
        ))}
      </nav>
    </div>
  );
}

function WorkNavigation({
  projectID,
  cards,
  onNavigate,
}: {
  projectID: string;
  cards: Card[];
  onNavigate?: () => void;
}) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const filters: [string, string, number][] = [
    ["", "全部工作", cards.filter((c) => c.kind !== "discussion").length],
    [
      "review",
      "待我审核",
      cards.filter((c) => c.todo_state === "review").length,
    ],
    ["ongoing", "持续事项", cards.filter((c) => c.kind === "ongoing").length],
  ];
  return (
    <nav aria-label={t("工作视图")} className="space-y-1">
      {filters.map(([filter, label, count]) => (
        <Button
          key={filter}
          variant={
            (params.get("view") || "") === filter ? "secondary" : "ghost"
          }
          size="compact"
          className="w-full justify-between"
          onClick={() => {
            navigate(
              `/projects/${projectID}/work${filter ? "?view=" + filter : ""}`,
            );
            onNavigate?.();
          }}
        >
          <span>{t(label)}</span>
          <span className="v2-text-meta">{count}</span>
        </Button>
      ))}
    </nav>
  );
}

export function ProjectFrame({
  me,
  page,
  startEmpty,
}: {
  me?: Identity;
  page: Page;
  startEmpty?: boolean;
}) {
  const { t } = useTranslation();
  const { projectID, cardID } = useParams();
  const location = useLocation();
  const navigate = useNavigate();
  const mode: Mode = location.pathname.includes("/work")
    ? "work"
    : "discussions";
  const projects = useQuery({ queryKey: keys.projects, queryFn: api.projects });
  const cards = useQuery({
    queryKey: keys.cards(projectID || ""),
    queryFn: () => api.cards(projectID!),
    enabled: !!projectID,
  });
  const project = projects.data?.find((p) => p.id === projectID);
  const [mobileOpen, setMobileOpen] = useState(false);
  const [createOpen, setCreateOpen] = useState(false);
  const gotoMode = (next: Mode) => navigate(`/projects/${projectID}/${next}`);
  if (projects.isError)
    return (
      <div className="v2 min-h-dvh p-6">
        <ErrorState
          message={String(projects.error)}
          onRetry={() => void projects.refetch()}
        />
      </div>
    );
  if (startEmpty || (!projects.isPending && !projects.data?.length))
    return (
      <main className="v2 grid min-h-dvh place-items-center p-4">
        <div className="v2-panel w-full max-w-sm p-5">
          <EmptyState
            title={t("还没有项目")}
            description={t("创建第一个项目，开始讨论或添加待办。")}
            action={
              <Button variant="primary" onClick={() => setCreateOpen(true)}>
                <Plus size={16} />
                {t("新建项目")}
              </Button>
            }
          />
          <ProjectCreate
            controlledOpen={createOpen}
            onOpenChange={setCreateOpen}
          />
        </div>
      </main>
    );
  if (!project && projects.isPending)
    return (
      <div className="v2 min-h-dvh">
        <DelayedSkeleton>
          <SidebarSkeleton />
        </DelayedSkeleton>
      </div>
    );
  if (!project)
    return (
      <main className="v2 min-h-dvh p-8">
        <ErrorState
          title={t("项目不可访问")}
          message={t("该项目不存在，或当前工作空间无访问权限。")}
          onRetry={() => navigate("/", { replace: true })}
        />
      </main>
    );

  const sidebar = (
    <Sidebar
      project={project.name}
      account={me?.display_name || t("账户")}
      mode={mode}
      onModeChange={(next) => {
        gotoMode(next);
        setMobileOpen(false);
      }}
      projectControl={
        <ProjectChooser
          projects={projects.data || []}
          project={project}
          mode={mode}
        />
      }
      accountControl={me && <AccountChooser me={me} />}
    >
      {cards.isPending ? (
        <DelayedSkeleton>
          <DiscussionListSkeleton />
        </DelayedSkeleton>
      ) : cards.isError ? (
        <ErrorState
          message={t("列表加载失败")}
          onRetry={() => void cards.refetch()}
        />
      ) : mode === "discussions" ? (
        <DiscussionNavigation
          projectID={project.id}
          cards={cards.data || []}
          onNavigate={() => setMobileOpen(false)}
        />
      ) : (
        <WorkNavigation
          projectID={project.id}
          cards={cards.data || []}
          onNavigate={() => setMobileOpen(false)}
        />
      )}
    </Sidebar>
  );

  return (
    <div className="v2 v2-app-frame">
      <div className="hidden min-h-0 md:block">{sidebar}</div>
      <div className="flex min-w-0 flex-col overflow-hidden">
        <div className="product-mobile-bar flex shrink-0 items-center gap-2 px-3 py-2 md:hidden">
          <Sheet open={mobileOpen} onOpenChange={setMobileOpen}>
            <SheetTrigger asChild>
              <IconButton icon={Menu} label={t("打开导航")} variant="ghost" />
            </SheetTrigger>
            <SheetContent className="p-0">
              <SheetTitle className="v2-visually-hidden">
                {t("工作导航")}
              </SheetTitle>
              <SheetDescription className="v2-visually-hidden">
                {t("选择项目、讨论和工作")}
              </SheetDescription>
              {sidebar}
            </SheetContent>
          </Sheet>
          <Typography scale="section" className="min-w-0 flex-1 truncate">
            {project.name}
          </Typography>
          <Button
            variant="ghost"
            size="compact"
            onClick={() => gotoMode(mode === "work" ? "discussions" : "work")}
          >
            {mode === "work" ? t("讨论") : t("工作")}
          </Button>
        </div>
        <div className="min-h-0 min-w-0 flex-1 overflow-auto">
          <Suspense
            fallback={
              <DelayedSkeleton>
                <ThreadSkeleton />
              </DelayedSkeleton>
            }
          >
            {page === "discussion" ? (
              <DiscussionHome project={project} cards={cards.data || []} />
            ) : page === "work" ? (
              <WorkBoardV2 project={project} cards={cards.data || []} />
            ) : (
              <CardThreadV2
                project={project}
                cardID={cardID || ""}
                mode={mode}
              />
            )}
          </Suspense>
        </div>
      </div>
    </div>
  );
}
