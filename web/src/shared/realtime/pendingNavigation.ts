// 与 public/notifications-sw.js 共享的持久化通知待跳转缓存契约。
// iOS PWA 在后台回收 SW 或冷启动回落 manifest.start_url ("/") 时，
// 前端可直接读取同源 Cache Storage 并配合 SW 消息双通道消费，避免深链丢失或重复跳转。
export const PENDING_NAVIGATION_CACHE_NAME = "cycledock-push-nav-v1";
export const PENDING_NAVIGATION_CACHE_KEY = "/__cycledock_pending_navigation__";
export const PENDING_NAVIGATION_TTL_MS = 60_000;

const MAX_HANDLED_NAVIGATION_IDS = 50;
const DEFAULT_WORKER_QUERY_TIMEOUT_MS = 800;

export type PendingNavigationRecord = {
  id: string;
  url: string;
  timestamp: number;
};

type MinimalCache = {
  match: (request: string) => Promise<{ json: () => Promise<unknown> } | undefined>;
  delete: (request: string) => Promise<boolean>;
};

type MinimalCacheStorage = {
  open: (cacheName: string) => Promise<MinimalCache>;
};

type MinimalWorker = {
  postMessage: (message: unknown, transfer?: Transferable[]) => void;
};

type MinimalServiceWorkerContainer = {
  controller?: MinimalWorker | null;
  ready?: Promise<{ active?: MinimalWorker | null }>;
  getRegistration?: () => Promise<{ active?: MinimalWorker | null } | undefined>;
};

export function resolveSameOriginTargetPath(
  rawURL: string | null | undefined,
  origin: string,
): string | null {
  if (!rawURL) return null;
  try {
    const parsed = new URL(rawURL, origin);
    if (parsed.origin !== origin) return null;
    return parsed.pathname + parsed.search + parsed.hash;
  } catch {
    return null;
  }
}

export function isFreshPendingNavigation(
  record: unknown,
  now = Date.now(),
): record is PendingNavigationRecord {
  if (!record || typeof record !== "object") return false;
  const candidate = record as Partial<PendingNavigationRecord>;
  if (typeof candidate.url !== "string" || !candidate.url) return false;
  if (
    typeof candidate.timestamp !== "number" ||
    !Number.isFinite(candidate.timestamp)
  ) {
    return false;
  }
  return now - candidate.timestamp < PENDING_NAVIGATION_TTL_MS;
}

function getDefaultCacheStorage(): MinimalCacheStorage | undefined {
  if (typeof caches !== "undefined" && typeof caches.open === "function") {
    return caches as unknown as MinimalCacheStorage;
  }
  return undefined;
}

function getDefaultOrigin(): string {
  return typeof window !== "undefined" ? window.location.origin : "";
}

function cacheRequestURL(origin: string): string {
  return new URL(PENDING_NAVIGATION_CACHE_KEY, origin).href;
}

export async function consumePendingNavigationFromCache(options?: {
  caches?: MinimalCacheStorage;
  origin?: string;
  now?: number;
}): Promise<PendingNavigationRecord | null> {
  const cacheStorage = options?.caches ?? getDefaultCacheStorage();
  const origin = options?.origin ?? getDefaultOrigin();
  const now = options?.now ?? Date.now();
  if (!cacheStorage || !origin) return null;
  try {
    const cache = await cacheStorage.open(PENDING_NAVIGATION_CACHE_NAME);
    const requestURL = cacheRequestURL(origin);
    const response = await cache.match(requestURL);
    if (!response) return null;
    let parsed: unknown = null;
    try {
      parsed = await response.json();
    } catch {
      parsed = null;
    }
    // 无论是否过期或损坏，读取后立即删除，避免旧意图滞留或重复消费。
    await cache.delete(requestURL);
    if (!isFreshPendingNavigation(parsed, now)) return null;
    if (!resolveSameOriginTargetPath(parsed.url, origin)) return null;
    return {
      id: typeof parsed.id === "string" ? parsed.id : "",
      url: parsed.url,
      timestamp: parsed.timestamp,
    };
  } catch {
    return null;
  }
}

export async function clearPendingNavigationFromCache(
  expectedID?: string,
  options?: {
    caches?: MinimalCacheStorage;
    origin?: string;
    now?: number;
  },
): Promise<void> {
  const cacheStorage = options?.caches ?? getDefaultCacheStorage();
  const origin = options?.origin ?? getDefaultOrigin();
  const now = options?.now ?? Date.now();
  if (!cacheStorage || !origin) return;
  try {
    const cache = await cacheStorage.open(PENDING_NAVIGATION_CACHE_NAME);
    const requestURL = cacheRequestURL(origin);
    if (!expectedID) {
      await cache.delete(requestURL);
      return;
    }
    const response = await cache.match(requestURL);
    if (!response) return;
    let parsed: unknown = null;
    try {
      parsed = await response.json();
    } catch {
      parsed = null;
    }
    const storedID =
      parsed && typeof parsed === "object"
        ? (parsed as { id?: unknown }).id
        : undefined;
    if (
      typeof storedID !== "string" ||
      !storedID ||
      storedID === expectedID ||
      !isFreshPendingNavigation(parsed, now)
    ) {
      await cache.delete(requestURL);
    }
  } catch {
    // 忽略缓存清理异常。
  }
}

async function resolveActiveWorker(
  serviceWorker: MinimalServiceWorkerContainer | undefined,
  timeoutMs: number,
): Promise<MinimalWorker | null> {
  if (!serviceWorker) return null;
  if (serviceWorker.controller) return serviceWorker.controller;
  if (typeof serviceWorker.getRegistration === "function") {
    try {
      const registration = await serviceWorker.getRegistration();
      if (!registration) {
        // 未注册 SW 的普通浏览器直接返回，避免等待永远不 resolve 的 serviceWorker.ready。
        return null;
      }
      if (registration.active) return registration.active;
    } catch {
      return null;
    }
  }
  if (!serviceWorker.ready) return null;
  try {
    const registration = await Promise.race([
      serviceWorker.ready,
      new Promise<null>((resolve) => {
        setTimeout(() => resolve(null), timeoutMs);
      }),
    ]);
    return registration?.active || serviceWorker.controller || null;
  } catch {
    return null;
  }
}

export async function notifyWorkerClearPendingNavigation(
  id: string | undefined,
  options?: {
    serviceWorker?: MinimalServiceWorkerContainer;
    timeoutMs?: number;
  },
): Promise<void> {
  const serviceWorker =
    options?.serviceWorker ??
    (typeof navigator !== "undefined" && "serviceWorker" in navigator
      ? (navigator.serviceWorker as unknown as MinimalServiceWorkerContainer)
      : undefined);
  const worker = await resolveActiveWorker(
    serviceWorker,
    options?.timeoutMs ?? 200,
  );
  if (!worker) return;
  try {
    worker.postMessage({
      type: "CYCLE_CLEAR_PENDING_NAVIGATION",
      id: id || undefined,
    });
  } catch {
    // 忽略 SW 通讯异常。
  }
}

export async function consumePendingNavigationFromWorker(options?: {
  serviceWorker?: MinimalServiceWorkerContainer;
  origin?: string;
  now?: number;
  timeoutMs?: number;
}): Promise<PendingNavigationRecord | null> {
  const serviceWorker =
    options?.serviceWorker ??
    (typeof navigator !== "undefined" && "serviceWorker" in navigator
      ? (navigator.serviceWorker as unknown as MinimalServiceWorkerContainer)
      : undefined);
  const origin = options?.origin ?? getDefaultOrigin();
  const now = options?.now ?? Date.now();
  const timeoutMs = options?.timeoutMs ?? DEFAULT_WORKER_QUERY_TIMEOUT_MS;
  if (!serviceWorker || !origin) return null;

  const targetWorker = await resolveActiveWorker(serviceWorker, timeoutMs);
  if (!targetWorker) return null;

  if (typeof MessageChannel === "undefined") {
    try {
      targetWorker.postMessage({ type: "CYCLE_CHECK_PENDING_NAVIGATION" });
    } catch {
      // 忽略异常。
    }
    return null;
  }

  return new Promise<PendingNavigationRecord | null>((resolve) => {
    const channel = new MessageChannel();
    let settled = false;
    const finish = (record: PendingNavigationRecord | null) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      channel.port1.onmessage = null;
      resolve(record);
    };
    const timer = setTimeout(() => finish(null), timeoutMs);
    channel.port1.onmessage = (event: MessageEvent) => {
      if (event.data?.type !== "CYCLE_PENDING_NAVIGATION") return;
      const rawURL = event.data.url;
      if (typeof rawURL !== "string" || !rawURL) {
        finish(null);
        return;
      }
      const timestamp =
        typeof event.data.timestamp === "number" ? event.data.timestamp : now;
      const candidate: PendingNavigationRecord = {
        id: typeof event.data.id === "string" ? event.data.id : "",
        url: rawURL,
        timestamp,
      };
      if (
        !isFreshPendingNavigation(candidate, now) ||
        !resolveSameOriginTargetPath(candidate.url, origin)
      ) {
        finish(null);
        return;
      }
      finish(candidate);
    };
    try {
      targetWorker.postMessage({ type: "CYCLE_CHECK_PENDING_NAVIGATION" }, [
        channel.port2,
      ]);
    } catch {
      finish(null);
    }
  });
}

export type PendingNavigationConsumerOptions = {
  onNavigate: (targetPath: string, options: { replace: boolean }) => void;
  getCurrentPath?: () => string;
  origin?: string;
  caches?: MinimalCacheStorage;
  serviceWorker?: MinimalServiceWorkerContainer;
  now?: () => number;
  workerTimeoutMs?: number;
};

export function createPendingNavigationConsumer(
  options: PendingNavigationConsumerOptions,
) {
  const handledIDs = new Set<string>();
  let inFlightWorkerQuery: Promise<PendingNavigationRecord | null> | null =
    null;

  const getOrigin = () => options.origin ?? getDefaultOrigin();
  const getNow = () => (options.now ? options.now() : Date.now());
  const getCurrentPath = () =>
    options.getCurrentPath
      ? options.getCurrentPath()
      : typeof window !== "undefined"
        ? window.location.pathname +
          window.location.search +
          window.location.hash
        : "";

  const rememberHandledID = (id: string) => {
    if (!id) return;
    handledIDs.add(id);
    if (handledIDs.size > MAX_HANDLED_NAVIGATION_IDS) {
      const oldest = handledIDs.values().next().value;
      if (oldest !== undefined) handledIDs.delete(oldest);
    }
  };

  const applyRecord = (
    record: {
      id?: string | null;
      url?: string | null;
      timestamp?: number | null;
    },
    meta: { source: "cache" | "worker" | "message"; replace?: boolean },
  ): boolean => {
    const origin = getOrigin();
    if (!origin) return false;
    const id = typeof record.id === "string" ? record.id : "";
    const now = getNow();
    const hasTimestamp =
      typeof record.timestamp === "number" &&
      Number.isFinite(record.timestamp);
    const expired =
      hasTimestamp &&
      now - (record.timestamp as number) >= PENDING_NAVIGATION_TTL_MS;

    // 一旦由消息或 SW 通道拿到意图，立刻同步清掉持久化缓存；
    // 一旦由缓存直读拿到意图，通知 SW 清理内存镜像，避免旧意图二次触发。
    if (meta.source !== "cache") {
      void clearPendingNavigationFromCache(id || undefined, {
        caches: options.caches,
        origin,
        now,
      });
    }
    if (meta.source !== "worker") {
      void notifyWorkerClearPendingNavigation(id || undefined, {
        serviceWorker: options.serviceWorker,
      });
    }

    if (expired) return false;
    const targetPath = resolveSameOriginTargetPath(record.url, origin);
    if (!targetPath) return false;

    if (id && handledIDs.has(id)) {
      return false;
    }
    rememberHandledID(id);

    const currentPath = getCurrentPath();
    const shouldReplace = meta.replace ?? currentPath === "/";
    if (targetPath !== currentPath) {
      options.onNavigate(targetPath, { replace: shouldReplace });
      return true;
    }
    return false;
  };

  const checkPending = async (checkOptions?: {
    replace?: boolean;
  }): Promise<boolean> => {
    const origin = getOrigin();
    const now = getNow();

    // 通道 1：直接读取同源 Cache Storage，不依赖 SW controller 就绪，冷启动与 SW 刚重启时最快生效。
    const fromCache = await consumePendingNavigationFromCache({
      caches: options.caches,
      origin,
      now,
    });
    if (fromCache) {
      return applyRecord(fromCache, {
        source: "cache",
        replace: checkOptions?.replace,
      });
    }

    // 通道 2：向 SW 发送 CYCLE_CHECK_PENDING_NAVIGATION，兜底 Cache Storage 不可用或内存态暂存。
    if (!inFlightWorkerQuery) {
      inFlightWorkerQuery = consumePendingNavigationFromWorker({
        serviceWorker: options.serviceWorker,
        origin,
        now,
        timeoutMs: options.workerTimeoutMs,
      }).finally(() => {
        inFlightWorkerQuery = null;
      });
    }
    const fromWorker = await inFlightWorkerQuery;
    if (fromWorker) {
      return applyRecord(fromWorker, {
        source: "worker",
        replace: checkOptions?.replace,
      });
    }
    return false;
  };

  const handleWorkerMessage = (
    data: unknown,
    messageOptions?: { replace?: boolean },
  ): boolean => {
    if (!data || typeof data !== "object") return false;
    const payload = data as {
      type?: unknown;
      url?: unknown;
      id?: unknown;
      timestamp?: unknown;
    };
    if (
      payload.type !== "CYCLE_NAVIGATE" &&
      payload.type !== "CYCLE_PENDING_NAVIGATION"
    ) {
      return false;
    }
    if (typeof payload.url !== "string" || !payload.url) return false;
    return applyRecord(
      {
        id: typeof payload.id === "string" ? payload.id : "",
        url: payload.url,
        timestamp:
          typeof payload.timestamp === "number" ? payload.timestamp : undefined,
      },
      {
        source: "message",
        replace: messageOptions?.replace,
      },
    );
  };

  return {
    checkPending,
    handleWorkerMessage,
  };
}
