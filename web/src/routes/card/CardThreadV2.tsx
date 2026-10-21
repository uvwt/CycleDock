import { useTranslation } from "react-i18next";
import { useCallback, useEffect, useLayoutEffect, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useNavigate } from "react-router-dom";
import { toast } from "sonner";
import {
  ArrowDown,
  Info,
  MoreHorizontal,
  Sparkles,
  Square,
  SquarePen,
} from "lucide-react";
import { api } from "../../shared/api/client";
import type { CardDetail, Project, TodoState } from "../../shared/api/types";
import { keys, queryClient } from "../../app-v2/query";
import { useCardStream } from "../../shared/realtime/useCardStream";
import {
  Button,
  IconButton,
  Input,
  LoadingButton,
  Textarea,
  Typography,
  Badge,
} from "../../components/ui/controls";
import { AdaptiveComposer } from "../../components/ui/AdaptiveComposer";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
  Popover,
  PopoverContent,
  PopoverTrigger,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Sheet,
  SheetContent,
  SheetDescription,
  SheetTitle,
  SheetTrigger,
} from "../../components/ui/overlays";
import {
  CardDetailSkeleton,
  DelayedSkeleton,
  ErrorState,
} from "../../components/ui/feedback";
import { PageHeader } from "../../components/layout/Scaffold";
import { TimelineV2 } from "./TimelineV2";
import { CardScheduleEditor } from "./CardScheduleEditor";

const todoLabels: Record<TodoState, string> = {
  todo: "待办",
  working: "进行中",
  review: "待审核",
  done: "已完成",
};
const states: TodoState[] = ["todo", "working", "review", "done"];
const ongoingLabels: Record<string, string> = {
  active: "运行中",
  paused: "已暂停",
  archived: "已归档",
};
type Mode = "discussions" | "work";

function useIsMobile() {
  const [mobile, setMobile] = useState(
    () => window.matchMedia("(max-width: 700px)").matches,
  );
  useEffect(() => {
    const media = window.matchMedia("(max-width: 700px)");
    const change = () => setMobile(media.matches);
    media.addEventListener("change", change);
    return () => media.removeEventListener("change", change);
  }, []);
  return mobile;
}

function formatDuration(start?: string, now = Date.now()) {
  if (!start) return undefined;
  const seconds = Math.max(0, Math.floor((now - Date.parse(start)) / 1000));
  return seconds >= 60
    ? `${Math.floor(seconds / 60)} 分 ${seconds % 60} 秒`
    : `${seconds} 秒`;
}

function CardStatus({
  detail,
  onUpdate,
  busy,
}: {
  detail: CardDetail;
  onUpdate: (value: string) => void;
  busy: boolean;
}) {
  const { t } = useTranslation();
  if (detail.card.kind === "discussion")
    return detail.card.discussion_state === "archived" ? (
      <Badge>{t("已归档")}</Badge>
    ) : (
      <Badge tone="accent">{t("讨论中")}</Badge>
    );
  const options =
    detail.card.kind === "todo"
      ? states.map((s) => ({ value: s, label: t(todoLabels[s]) }))
      : ["active", "paused", "archived"].map((s) => ({
          value: s,
          label: t(ongoingLabels[s]),
        }));
  const value =
    detail.card.kind === "todo"
      ? detail.card.todo_state || "todo"
      : detail.card.ongoing_state || "active";
  return (
    <Select value={value} onValueChange={onUpdate} disabled={busy}>
      <SelectTrigger className="w-32" aria-label={t("卡片状态")}>
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        {options.map((x) => (
          <SelectItem key={x.value} value={x.value}>
            {x.label}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}

function CardInfoPanel({
  detail,
  project,
  cardID,
  onChanged,
}: {
  detail: CardDetail;
  project: Project;
  cardID: string;
  onChanged: () => void;
}) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [editing, setEditing] = useState(false);
  const [title, setTitle] = useState(detail.card.title);
  const [description, setDescription] = useState(detail.card.description);
  const edit = useMutation({
    mutationFn: () => api.updateCard(cardID, title, description),
    onSuccess: () => {
      onChanged();
      setEditing(false);
      toast.success(t("已保存"));
    },
    onError: (error) => toast.error(String(error)),
  });
  return (
    <div className="max-h-[75dvh] min-w-0 space-y-4 overflow-y-auto text-[var(--v2-type-control)]">
      {!editing ? (
        <div className="space-y-3">
          <div>
            <Typography scale="meta">{t("说明")}</Typography>
            <p className="whitespace-pre-wrap break-words">
              {detail.card.description || t("暂无说明")}
            </p>
          </div>
          {detail.card.source_discussion_id && (
            <div>
              <Typography scale="meta">{t("来源讨论")}</Typography>
              <Button
                variant="ghost"
                size="compact"
                className="p-0 text-[var(--v2-accent)]"
                onClick={() =>
                  navigate(
                    `/projects/${project.id}/discussions/${detail.card.source_discussion_id}`,
                  )
                }
              >
                {t("打开原讨论")}
              </Button>
            </div>
          )}
          {detail.card.kind !== "discussion" && (
            <Button
              size="compact"
              onClick={() => {
                setTitle(detail.card.title);
                setDescription(detail.card.description);
                setEditing(true);
              }}
            >
              <SquarePen size={14} />
              {t("编辑信息")}
            </Button>
          )}
        </div>
      ) : (
        <form
          className="space-y-3"
          onSubmit={(e) => {
            e.preventDefault();
            edit.mutate();
          }}
        >
          <label className="block space-y-1">
            <Typography scale="meta">{t("标题")}</Typography>
            <Input
              value={title}
              onChange={(e) => setTitle(e.target.value)}
              required
            />
          </label>
          <label className="block space-y-1">
            <Typography scale="meta">{t("说明")}</Typography>
            <Textarea
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              rows={3}
            />
          </label>
          <div className="flex gap-2">
            <LoadingButton
              variant="primary"
              size="compact"
              loading={edit.isPending}
              disabled={!title.trim()}
            >
              {t("保存")}
            </LoadingButton>
            <Button
              size="compact"
              type="button"
              onClick={() => setEditing(false)}
            >
              {t("取消")}
            </Button>
          </div>
        </form>
      )}
      {detail.card.kind === "ongoing" && (
        <CardScheduleEditor
          cardID={cardID}
          schedules={detail.schedules}
          lastCheckin={detail.last_checkin}
          onChanged={onChanged}
        />
      )}
    </div>
  );
}

function InfoTrigger({
  detail,
  project,
  cardID,
  onChanged,
}: {
  detail: CardDetail;
  project: Project;
  cardID: string;
  onChanged: () => void;
}) {
  const { t } = useTranslation();
  const mobile = useIsMobile();
  const [open, setOpen] = useState(false);
  const trigger = (
    <IconButton icon={Info} label={t("卡片信息")} variant="ghost" />
  );
  const content = (
    <CardInfoPanel
      key={cardID}
      detail={detail}
      project={project}
      cardID={cardID}
      onChanged={onChanged}
    />
  );
  if (mobile)
    return (
      <Sheet open={open} onOpenChange={setOpen}>
        <SheetTrigger asChild>{trigger}</SheetTrigger>
        <SheetContent>
          <SheetTitle className="v2-text-section mb-2">
            {t("卡片信息")}
          </SheetTitle>
          <SheetDescription className="v2-visually-hidden">
            {t("查看或编辑卡片信息")}
          </SheetDescription>
          {content}
        </SheetContent>
      </Sheet>
    );
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>{trigger}</PopoverTrigger>
      <PopoverContent align="end" className="w-[min(360px,calc(100vw-28px))]">
        <Typography as="h3" scale="section" className="mb-3">
          {t("卡片信息")}
        </Typography>
        {content}
      </PopoverContent>
    </Popover>
  );
}

export function CardThreadV2({
  project,
  cardID,
  mode,
}: {
  project: Project;
  cardID: string;
  mode: Mode;
}) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const detail = useQuery({
    queryKey: keys.card(cardID),
    queryFn: () => api.card(cardID),
    enabled: !!cardID,
  });
  const live = useCardStream(cardID, project.id);
  const [draft, setDraft] = useState("");
  const [sendMode, setSendMode] = useState<"enqueue" | "interrupt">("enqueue");
  const [expanded, setExpanded] = useState(false);
  const [atBottom, setAtBottom] = useState(true);
  const [now, setNow] = useState(0);
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, []);
  useEffect(() => {
    const element = document.querySelector<HTMLElement>("[data-timeline]");
    if (!element) return;
    const check = () =>
      setAtBottom(
        element.scrollHeight - element.scrollTop - element.clientHeight < 120,
      );
    element.addEventListener("scroll", check, { passive: true });
    return () => element.removeEventListener("scroll", check);
  }, [detail.isPending, cardID]);
  useLayoutEffect(() => {
    if (!atBottom) return;
    const element = document.querySelector<HTMLElement>("[data-timeline]");
    if (element) element.scrollTop = element.scrollHeight;
  }, [expanded, atBottom]);
  const refresh = useCallback(async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: keys.card(cardID) }),
      queryClient.invalidateQueries({ queryKey: keys.cards(project.id) }),
    ]);
  }, [cardID, project.id]);
  const send = useMutation({
    mutationFn: () =>
      detail.data?.current_run?.status === "waiting_human"
        ? api.reply(cardID, draft.trim())
        : api.send(cardID, draft.trim(), sendMode),
    onSuccess: () => {
      setDraft("");
      void refresh();
      setExpanded(false);
    },
    onError: (error) =>
      toast.error(t("发送失败，草稿已保留：") + String(error)),
  });
  const generate = useMutation({
    mutationFn: () =>
      api.send(
        cardID,
        "请根据当前讨论中已经明确的工作目标，调用生成任务能力创建相应 Todo 或 Ongoing；如果目标不明确，请先提问澄清。",
        "enqueue",
      ),
    onSuccess: refresh,
    onError: (error) => toast.error(String(error)),
  });
  const move = useMutation({
    mutationFn: (state: string) =>
      detail.data?.card.kind === "todo"
        ? api.moveTodo(cardID, state as TodoState)
        : api.ongoingState(cardID, state),
    onSuccess: refresh,
    onError: (error) => toast.error(String(error)),
  });
  const cancel = useMutation({
    mutationFn: (id: string) => api.cancelTrigger(cardID, id),
    onSuccess: refresh,
    onError: (error) => toast.error(String(error)),
  });
  const retry = useMutation({
    mutationFn: (id: string) => api.retry(cardID, id),
    onSuccess: refresh,
    onError: (error) => toast.error(String(error)),
  });
  const stop = useMutation({
    mutationFn: (id: string) => api.cancelRun(cardID, id),
    onSuccess: refresh,
    onError: (error) => toast.error(String(error)),
  });
  const archive = useMutation({
    mutationFn: () => api.archiveDiscussion(cardID),
    onSuccess: refresh,
    onError: (error) => toast.error(String(error)),
  });
  const remove = useMutation({
    mutationFn: () => api.deleteDiscussion(cardID),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: keys.cards(project.id) });
      navigate(`/projects/${project.id}/discussions`);
    },
    onError: (error) => toast.error(String(error)),
  });
  useEffect(() => {
    if (
      detail.data?.card.project_id &&
      detail.data.card.project_id !== project.id
    )
      navigate(`/projects/${detail.data.card.project_id}/${mode}/${cardID}`, {
        replace: true,
      });
  }, [detail.data?.card.project_id, project.id, mode, cardID, navigate]);
  useEffect(() => {
    if (atBottom)
      document
        .querySelector<HTMLElement>("[data-timeline-end]")
        ?.scrollIntoView({ block: "end" });
  }, [detail.data?.timeline.length, live.live?.text, atBottom]);

  if (detail.isPending)
    return (
      <div className="min-h-full">
        <DelayedSkeleton>
          <CardDetailSkeleton />
        </DelayedSkeleton>
      </div>
    );
  if (detail.isError || !detail.data)
    return (
      <ErrorState
        title={t("无法打开卡片")}
        message={String(detail.error || t("卡片不可用"))}
        onRetry={() => void detail.refetch()}
      />
    );
  const card = detail.data.card;
  const discussion = card.kind === "discussion";
  const archived = discussion && card.discussion_state === "archived";
  const running = detail.data.current_run?.status;
  const duration = formatDuration(
    running === "running"
      ? detail.data.current_run?.started_at
      : live.live?.startedAt,
    now,
  );
  return (
    <div className="product-thread">
      <div className="product-thread-header">
        <PageHeader
          title={card.title || t("未命名讨论")}
          description={
            card.kind === "discussion"
              ? "Discussion"
              : card.kind === "todo"
                ? "Todo"
                : "Ongoing"
          }
          actions={
            <>
              <CardStatus
                detail={detail.data}
                busy={move.isPending}
                onUpdate={(state) => move.mutate(state)}
              />
              <InfoTrigger
                detail={detail.data}
                project={project}
                cardID={cardID}
                onChanged={() => void refresh()}
              />
              {discussion && (
                <DropdownMenu>
                  <DropdownMenuTrigger asChild>
                    <IconButton
                      icon={MoreHorizontal}
                      label={t("讨论操作")}
                      variant="ghost"
                    />
                  </DropdownMenuTrigger>
                  <DropdownMenuContent align="end">
                    {!archived && (
                      <DropdownMenuItem onSelect={() => archive.mutate()}>
                        {t("归档讨论")}
                      </DropdownMenuItem>
                    )}
                    <DropdownMenuItem
                      onSelect={() => {
                        if (
                          window.confirm(
                            t("删除此讨论？关联工作的卡片仍保留。"),
                          )
                        )
                          remove.mutate();
                      }}
                    >
                      {t("删除讨论")}
                    </DropdownMenuItem>
                  </DropdownMenuContent>
                </DropdownMenu>
              )}
            </>
          }
        />
      </div>
      <TimelineV2
        detail={detail.data}
        live={live.live}
        duration={duration}
        onCancel={(id) => cancel.mutate(id)}
        onRetry={(id) => retry.mutate(id)}
        pending={cancel.isPending || retry.isPending}
      />
      {!atBottom && (
        <div className="pointer-events-none absolute right-6 bottom-36">
          <IconButton
            icon={ArrowDown}
            label={t("跳到最新")}
            className="pointer-events-auto shadow-sm"
            onClick={() =>
              document
                .querySelector("[data-timeline-end]")
                ?.scrollIntoView({ behavior: "smooth" })
            }
          />
        </div>
      )}
      <div className="product-thread-composer-wrap">
        <div className="product-thread-composer">
          {live.lastError && (
            <Typography scale="meta" className="mb-2 block">
              {live.lastError}
            </Typography>
          )}
          {archived ? (
            <Typography scale="control">
              {t("此讨论已归档，无法继续发送消息。")}
            </Typography>
          ) : (
            <AdaptiveComposer
              value={draft}
              onChange={setDraft}
              onSubmit={() => {
                if (draft.trim()) send.mutate();
              }}
              placeholder={
                running === "waiting_human"
                  ? t("回复并继续")
                  : t("发送新指令或评论…")
              }
              ariaLabel={t("回复内容")}
              expanded={expanded}
              onExpandedChange={setExpanded}
              busy={send.isPending}
              keepExpanded={!!running}
              sendLabel={
                running === "waiting_human" ? t("回复并继续") : t("发送")
              }
              compactAction={
                discussion ? (
                  <IconButton
                    type="button"
                    icon={Sparkles}
                    label={t("生成任务")}
                    variant="ghost"
                    disabled={generate.isPending}
                    onClick={() => generate.mutate()}
                  />
                ) : undefined
              }
              actions={
                <div className="product-composer-action">
                  {discussion && (
                    <IconButton
                      type="button"
                      icon={Sparkles}
                      label={t("生成任务")}
                      variant="ghost"
                      disabled={generate.isPending}
                      onClick={() => generate.mutate()}
                    />
                  )}
                  {(running === "running" || running === "queued") && (
                    <Select
                      value={sendMode}
                      onValueChange={(value) =>
                        setSendMode(value as "enqueue" | "interrupt")
                      }
                    >
                      <SelectTrigger
                        aria-label={t("新指令处理方式")}
                        className="h-8 w-32"
                      >
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value="enqueue">{t("加入队列")}</SelectItem>
                        <SelectItem value="interrupt">
                          {t("立即转向")}
                        </SelectItem>
                      </SelectContent>
                    </Select>
                  )}
                  {running && (
                    <Badge tone="accent">
                      {(
                        {
                          running: t("执行中"),
                          queued: t("排队中"),
                          waiting_human: t("等待回复"),
                        } as Record<string, string>
                      )[running] || running}
                    </Badge>
                  )}
                  {detail.data.current_run &&
                    ["running", "queued"].includes(running || "") && (
                      <IconButton
                        type="button"
                        icon={Square}
                        label={t("取消当前运行")}
                        variant="ghost"
                        disabled={stop.isPending}
                        onClick={() =>
                          stop.mutate(detail.data!.current_run!.id)
                        }
                      />
                    )}
                </div>
              }
              meta={<span>{t("Enter 发送 · Shift+Enter 换行")}</span>}
            />
          )}
        </div>
      </div>
    </div>
  );
}
