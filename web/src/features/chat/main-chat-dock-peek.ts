import type { CardMessageDetail } from "../../shared/realtime/cardUpdates.ts";

export type DockPeekMessage = {
  cardID: string;
  projectID: string;
  kind: string;
  title: string;
  body: string;
  isMainDiscussion: boolean;
  isToolApproval: boolean;
};

export type DockCardMessageDecision = {
  shouldInvalidateMainChat: boolean;
  peek: DockPeekMessage | null;
};

export type DockPeekClickAction =
  | { type: "open_main_chat" }
  | { type: "navigate"; to: string };

export function cardRouteForDockPeek(
  projectID: string,
  cardID: string,
  kind: string,
): string {
  const pid = encodeURIComponent(projectID);
  const cid = encodeURIComponent(cardID);
  if (kind === "discussion") return `/chat/${pid}/${cid}`;
  if (kind === "ongoing") return `/plans/${pid}/${cid}`;
  return `/cards/${pid}/${cid}`;
}

// 统一判定悬浮球收到的卡片消息是否需要刷新主聊天缓存、以及是否应展示预览气泡。
export function resolveDockCardMessage(
  detail: CardMessageDetail | null | undefined,
  context: {
    projectID: string;
    mainCardID: string;
    isMainMuted: boolean;
    isDockOpen: boolean;
  },
): DockCardMessageDecision {
  if (
    !detail ||
    !context.projectID ||
    detail.project_id !== context.projectID ||
    !detail.id ||
    detail.viewing
  ) {
    return { shouldInvalidateMainChat: false, peek: null };
  }

  const body = detail.body.trim();
  if (!body) {
    return { shouldInvalidateMainChat: false, peek: null };
  }

  const isMainDiscussion =
    Boolean(context.mainCardID) && detail.id === context.mainCardID;
  const isToolApproval = detail.notification_kind === "tool_approval";

  // 仅在悬浮球收起时展示预览气泡，避免主聊天浮窗已展开时出现叠加遮挡。
  if (context.isDockOpen) {
    return { shouldInvalidateMainChat: isMainDiscussion, peek: null };
  }

  // 主聊天静音仅作用于主聊天自身，避免误吞同项目其他未静音卡片的预览提醒；
  // 待人工审批工具调用与 cardNotificationBody 保持一致，穿透静音与静默标记。
  const isMuted = isMainDiscussion
    ? Boolean(detail.notifications_muted || context.isMainMuted)
    : Boolean(detail.notifications_muted);
  if (!isToolApproval && (isMuted || detail.notification_suppressed)) {
    return { shouldInvalidateMainChat: isMainDiscussion, peek: null };
  }

  return {
    shouldInvalidateMainChat: isMainDiscussion,
    peek: {
      cardID: detail.id,
      projectID: detail.project_id,
      kind: detail.kind,
      title: (detail.title || "").trim(),
      body,
      isMainDiscussion,
      isToolApproval,
    },
  };
}

export function resolveDockPeekClickAction(
  peek: DockPeekMessage,
  mainCardID: string,
): DockPeekClickAction {
  if (
    peek.isMainDiscussion ||
    (Boolean(mainCardID) && peek.cardID === mainCardID)
  ) {
    return { type: "open_main_chat" };
  }
  return {
    type: "navigate",
    to: cardRouteForDockPeek(peek.projectID, peek.cardID, peek.kind),
  };
}
