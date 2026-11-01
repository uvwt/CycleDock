import { useSyncExternalStore } from "react";
import { api, APIError } from "../api/client";

const preferenceKey = "cycledock.card-notifications";
const endpointStorageKey = "cycledock.push-endpoint";
const changedEvent = "cycledock:notifications-changed";

export function notificationsSupported() {
  return (
    window.isSecureContext &&
    "Notification" in window &&
    "serviceWorker" in navigator
  );
}

function enabled() {
  try {
    return localStorage.getItem(preferenceKey) === "true";
  } catch {
    return false;
  }
}

export function setNotificationsEnabled(value: boolean) {
  localStorage.setItem(preferenceKey, String(value));
  window.dispatchEvent(new Event(changedEvent));
}

export function useNotificationsEnabled() {
  return useSyncExternalStore((listener) => {
    window.addEventListener(changedEvent, listener);
    window.addEventListener("storage", listener);
    return () => {
      window.removeEventListener(changedEvent, listener);
      window.removeEventListener("storage", listener);
    };
  }, enabled);
}

let cachedRegistration: ServiceWorkerRegistration | undefined;
let workerRequest: Promise<ServiceWorkerRegistration> | undefined;
let cachedPublicKey: string | undefined;
let pushKeyRequest: Promise<string> | undefined;
let pushEndpoint = readStoredPushEndpoint();

function readStoredPushEndpoint() {
  try {
    return localStorage.getItem(endpointStorageKey) ?? "";
  } catch {
    return "";
  }
}

function rememberPushEndpoint(endpoint: string) {
  if (!endpoint) return;
  pushEndpoint = endpoint;
  try {
    localStorage.setItem(endpointStorageKey, endpoint);
  } catch {
    // 隐私模式禁止写入时仍保留内存缓存。
  }
}

function forgetPushEndpoint() {
  pushEndpoint = "";
  try {
    localStorage.removeItem(endpointStorageKey);
  } catch {
    // 忽略存储清理失败。
  }
}

export function notificationWorker() {
  if (!workerRequest) {
    workerRequest = navigator.serviceWorker
      .register("/notifications-sw.js")
      .then(() => navigator.serviceWorker.ready)
      .then((registration) => {
        cachedRegistration = registration;
        return registration;
      })
      .catch((error: unknown) => {
        workerRequest = undefined;
        throw error;
      });
  }
  return workerRequest;
}

export function loadPushPublicKey() {
  if (!pushKeyRequest) {
    pushKeyRequest = api
      .pushConfig()
      .then((config) => {
        cachedPublicKey = config.public_key;
        window.dispatchEvent(new Event(changedEvent));
        return config.public_key;
      })
      .catch((error: unknown) => {
        pushKeyRequest = undefined;
        if (error instanceof APIError && error.status === 401) {
          cachedPublicKey = "";
          return "";
        }
        throw error;
      });
  }
  return pushKeyRequest;
}

export function usePushPublicKey() {
  return useSyncExternalStore(
    (listener) => {
      window.addEventListener(changedEvent, listener);
      return () => window.removeEventListener(changedEvent, listener);
    },
    () => cachedPublicKey,
  );
}

// 在设置页或权限提示出现时预先拉好 VAPID 公钥并激活 Service Worker，
// 让 iOS WebKit 点击开启时无需跨越网络等待即可直接进入权限与订阅调用。
export function warmPushNotifications() {
  if (!notificationsSupported() || homeScreenRequired()) return;
  void loadPushPublicKey().catch(() => undefined);
  void notificationWorker().catch(() => undefined);
}

export function homeScreenRequired() {
  const ios =
    /iPhone|iPad|iPod/.test(navigator.userAgent) ||
    (navigator.platform === "MacIntel" && navigator.maxTouchPoints > 1);
  const standalone =
    window.matchMedia("(display-mode: standalone)").matches ||
    (navigator as Navigator & { standalone?: boolean }).standalone === true;
  return ios && !standalone;
}

function subscriptionKeys(subscription: PushSubscription) {
  const json = subscription.toJSON();
  const p256dh = json.keys?.p256dh;
  const auth = json.keys?.auth;
  if (!subscription.endpoint || !p256dh || !auth) return undefined;
  return { endpoint: subscription.endpoint, keys: { p256dh, auth } };
}

function sameApplicationServerKey(
  subscription: PushSubscription,
  expected: Uint8Array,
) {
  const current = subscription.options?.applicationServerKey;
  if (!current) return true;
  const actual = new Uint8Array(current);
  if (actual.byteLength !== expected.byteLength) return false;
  for (let index = 0; index < actual.byteLength; index += 1) {
    if (actual[index] !== expected[index]) return false;
  }
  return true;
}

async function subscribeAndSave(
  registration: ServiceWorkerRegistration,
  publicKey: string,
) {
  const applicationServerKey = decodeBase64Url(publicKey);
  let subscription: PushSubscription;
  try {
    // 直接调用 subscribe，避免前置 getSubscription() 额外异步跳数消耗 iOS 用户手势；
    // 同公钥下标准实现会幂等返回已有订阅。
    subscription = await registration.pushManager.subscribe({
      userVisibleOnly: true,
      applicationServerKey,
    });
    if (!sameApplicationServerKey(subscription, applicationServerKey)) {
      await subscription.unsubscribe();
      subscription = await registration.pushManager.subscribe({
        userVisibleOnly: true,
        applicationServerKey,
      });
    }
  } catch (error) {
    if (error instanceof DOMException && error.name === "InvalidStateError") {
      const existing = await registration.pushManager.getSubscription();
      await existing?.unsubscribe();
      subscription = await registration.pushManager.subscribe({
        userVisibleOnly: true,
        applicationServerKey,
      });
    } else {
      throw error;
    }
  }
  const body = subscriptionKeys(subscription);
  if (!body) {
    throw new Error("Push subscription keys are unavailable");
  }
  rememberPushEndpoint(body.endpoint);
  await api.savePushSubscription(body);
  if (typeof document !== "undefined" && document.visibilityState !== "visible") {
    markPushIdle();
  }
}

// 点击开启时第一时间请求系统权限，并优先复用已预热的 Worker 与 VAPID 公钥直接调用 subscribe。
export async function enableNotifications(): Promise<NotificationPermission> {
  const permission =
    Notification.permission === "granted"
      ? "granted"
      : await Notification.requestPermission();
  if (permission !== "granted") return permission;
  const registration = cachedRegistration ?? (await notificationWorker());
  const publicKey =
    cachedPublicKey !== undefined ? cachedPublicKey : await loadPushPublicKey();
  if (publicKey && registration.pushManager) {
    await subscribeAndSave(registration, publicKey);
  }
  setNotificationsEnabled(true);
  return permission;
}

export async function syncPushSubscription() {
  try {
    if (!notificationsSupported() || Notification.permission !== "granted") return;
    const publicKey =
      cachedPublicKey !== undefined
        ? cachedPublicKey
        : await loadPushPublicKey().catch(() => "");
    if (!publicKey) return;
    const registration = cachedRegistration ?? (await notificationWorker());
    if (!registration.pushManager) return;
    const applicationServerKey = decodeBase64Url(publicKey);
    let existing = await registration.pushManager.getSubscription();
    if (existing && !sameApplicationServerKey(existing, applicationServerKey)) {
      await api.deletePushSubscription(existing.endpoint).catch(() => undefined);
      await existing.unsubscribe();
      forgetPushEndpoint();
      existing = null;
    }
    // 已经允许过通知的浏览器可以在页面加载时补订阅；iOS 无手势时失败则留待设置页点击重试。
    const subscription =
      existing ??
      (await registration.pushManager.subscribe({
        userVisibleOnly: true,
        applicationServerKey,
      }));
    const body = subscriptionKeys(subscription);
    if (body) {
      rememberPushEndpoint(body.endpoint);
      await api.savePushSubscription(body);
      if (
        typeof document !== "undefined" &&
        document.visibilityState !== "visible"
      ) {
        markPushIdle();
      }
    }
  } catch {
    // 设置页再次开启时会在点击手势里重试。
  }
}

export async function beatPushPresence() {
  if (typeof document !== "undefined" && document.visibilityState !== "visible") {
    markPushIdle();
    return;
  }
  const registration =
    cachedRegistration ?? (await navigator.serviceWorker.getRegistration());
  const subscription = await registration?.pushManager?.getSubscription();
  if (!subscription) return;
  rememberPushEndpoint(subscription.endpoint);
  if (typeof document !== "undefined" && document.visibilityState !== "visible") {
    markPushIdle();
    return;
  }
  try {
    await api.pushPresence(subscription.endpoint);
  } catch (error) {
    if (error instanceof APIError && error.status === 404) {
      await syncPushSubscription();
    }
  }
}

export function markPushIdle() {
  // 必须在 pagehide 里同步发出。先去问 Service Worker 再请求，页面关掉时请求来不及发出。
  if (!pushEndpoint) return;
  void fetch("/api/v1/notifications/push/inactive", {
    method: "POST",
    credentials: "same-origin",
    keepalive: true,
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ endpoint: pushEndpoint }),
  });
}

export async function clearPushSubscription() {
  if (!("serviceWorker" in navigator)) return;
  const registration = await navigator.serviceWorker.getRegistration();
  const subscription = await registration?.pushManager?.getSubscription();
  if (!subscription) {
    forgetPushEndpoint();
    return;
  }
  await api.deletePushSubscription(subscription.endpoint).catch(() => undefined);
  await subscription.unsubscribe();
  forgetPushEndpoint();
}

export async function disableNotifications() {
  setNotificationsEnabled(false);
  await clearPushSubscription();
}

function decodeBase64Url(value: string) {
  const padded = value.replace(/-/g, "+").replace(/_/g, "/");
  const binary = atob(padded + "=".repeat((4 - (padded.length % 4)) % 4));
  return Uint8Array.from(binary, (char) => char.charCodeAt(0));
}
