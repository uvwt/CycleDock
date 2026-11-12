import type { QueryClient } from "@tanstack/react-query";
import { keys } from "../../app/query";
import type {
  Card,
  CardDetail,
  DeviceTarget,
  PendingTurn,
  TimelineEvent,
} from "../api/types";

type CachedCardDetail = CardDetail | { card: null; notFound: true };

function isLiveCardDetail(value: CachedCardDetail | undefined): value is CardDetail {
  return Boolean(value && value.card);
}

// 同步更新单卡详情缓存（CardDetail.card）与项目卡片列表缓存（Card[]），消除不同缓存结构导致的状态滞后。
export function patchCachedCard(
  queryClient: QueryClient,
  cardID: string,
  projectID: string | undefined,
  patch: Partial<Card>,
) {
  queryClient.setQueryData<CachedCardDetail>(keys.card(cardID), (old) => {
    if (!isLiveCardDetail(old)) return old;
    return {
      ...old,
      card: {
        ...old.card,
        ...patch,
      },
    };
  });

  const updateList = (list: Card[] | undefined) =>
    list
      ? list.map((item) => (item.id === cardID ? { ...item, ...patch } : item))
      : list;

  if (projectID) {
    queryClient.setQueryData<Card[]>(keys.cards(projectID), updateList);
  } else {
    queryClient.setQueriesData<Card[]>({ queryKey: ["cards"] }, updateList);
  }
}

// 将新建或服务端返回的最新卡片写入项目卡片列表与单卡详情缓存，使页面跳转与弹窗关闭无需等待列表重拉。
export function upsertCachedProjectCard(
  queryClient: QueryClient,
  projectID: string,
  card: Card,
) {
  queryClient.setQueryData<Card[]>(keys.cards(projectID), (old) => {
    if (!old) return old;
    const index = old.findIndex((item) => item.id === card.id);
    if (index === -1) return [card, ...old];
    const next = [...old];
    next[index] = { ...next[index], ...card };
    return next;
  });

  queryClient.setQueryData<CachedCardDetail>(keys.card(card.id), (old) => {
    if (!isLiveCardDetail(old)) return old;
    return {
      ...old,
      card: {
        ...old.card,
        ...card,
      },
    };
  });
}

// 删除成功后立即从本地缓存移除卡片，避免等待完整列表重刷才关闭确认框或更新侧栏。
export function removeCachedCard(queryClient: QueryClient, card: Card) {
  queryClient.setQueryData(keys.card(card.id), {
    card: null,
    notFound: true,
  });

  queryClient.setQueriesData<Card[]>({ queryKey: ["cards"] }, (old) =>
    old ? old.filter((item) => item.id !== card.id) : old,
  );
}

// 发送消息接口返回成功后，立即把新消息或排队轮次写入本地时间线缓存，避免按钮等待后续 GET 请求转圈。
export function applyOptimisticSentTurn(
  queryClient: QueryClient,
  cardID: string,
  params: {
    eventID: string;
    turnID: string;
    content: string;
    actorName?: string;
    deviceTarget?: DeviceTarget | null;
  },
) {
  queryClient.setQueryData<CachedCardDetail>(keys.card(cardID), (old) => {
    if (!isLiveCardDetail(old)) return old;

    const now = new Date().toISOString();
    const maxEventSeq = old.timeline.reduce(
      (max, item) => Math.max(max, item.sequence || 0),
      0,
    );
    const hasActiveAttempt =
      Boolean(old.current_attempt) &&
      ["queued", "running"].includes(old.current_attempt?.status || "");

    if (hasActiveAttempt) {
      if (old.pending_turns.some((item) => item.id === params.turnID)) {
        return old;
      }
      const maxTurnSeq = old.pending_turns.reduce(
        (max, item) => Math.max(max, item.sequence || 0),
        maxEventSeq,
      );
      const nextTurn: PendingTurn = {
        id: params.turnID,
        sequence: maxTurnSeq + 1,
        source: "human",
        dispatch_mode: "enqueue",
        source_event_id: params.eventID,
        content: params.content,
        device_node_id: params.deviceTarget?.node_id,
        device_name: params.deviceTarget?.name,
        created_at: now,
      };
      return {
        ...old,
        pending_turns: [...old.pending_turns, nextTurn],
      };
    }

    if (old.timeline.some((item) => item.id === params.eventID)) {
      return old;
    }
    const nextEvent: TimelineEvent = {
      id: params.eventID,
      sequence: maxEventSeq + 1,
      type: "comment",
      actor_kind: "human",
      actor_name: params.actorName || "Human",
      content: params.content,
      created_at: now,
    };
    return {
      ...old,
      timeline: [...old.timeline, nextEvent],
    };
  });
}
