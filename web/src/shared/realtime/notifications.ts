import { useSyncExternalStore } from "react";
import { api, APIError } from "../api/client";

const preferenceKey = "cycledock.card-notifications";
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

export async function notificationWorker() {
  await navigator.serviceWorker.register("/notifications-sw.js");
  return navigator.serviceWorker.ready;
}

let pushKeyRequest: Promise<string> | undefined;
let pushEndpoint = "";

function rememberPushEndpoint(endpoint: string) {
  if (endpoint) pushEndpoint = endpoint;
}

export function loadPushPublicKey() {
  if (!pushKeyRequest) {
    pushKeyRequest = api
      .pushConfig()
      .then((config) => config.public_key)
      .catch((error: unknown) => {
        pushKeyRequest = undefined;
        if (error instanceof APIError && error.status === 401) return "";
        throw error;
      });
  }
  return pushKeyRequest;
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

// 点击开启时要尽快调用 subscribe。VAPID 公钥在进入页面时预先取好，避免打断用户手势。
export async function enableNotifications(): Promise<NotificationPermission> {
  const keyRequest = loadPushPublicKey();
  const permission =
    Notification.permission === "granted"
      ? "granted"
      : await Notification.requestPermission();
  if (permission !== "granted") return permission;
  const registration = await notificationWorker();
  setNotificationsEnabled(true);
  const publicKey = await keyRequest;
  if (publicKey && registration.pushManager) {
    const existing = await registration.pushManager.getSubscription();
    const subscription =
      existing ??
      (await registration.pushManager.subscribe({
        userVisibleOnly: true,
        applicationServerKey: decodeBase64Url(publicKey),
      }));
    const body = subscriptionKeys(subscription);
    if (body) {
      rememberPushEndpoint(body.endpoint);
      await api.savePushSubscription(body);
    }
  }
  return permission;
}

export async function syncPushSubscription() {
  try {
    if (!notificationsSupported() || Notification.permission !== "granted") return;
    const publicKey = await loadPushPublicKey().catch(() => "");
    if (!publicKey) return;
    const registration = await notificationWorker();
    if (!registration.pushManager) return;
    // 已经允许过通知的浏览器可以在页面加载时补订阅。没有手势时失败则保持页面内通知。
    const subscription =
      (await registration.pushManager.getSubscription()) ??
      (await registration.pushManager.subscribe({
        userVisibleOnly: true,
        applicationServerKey: decodeBase64Url(publicKey),
      }));
    const body = subscriptionKeys(subscription);
    if (body) {
      rememberPushEndpoint(body.endpoint);
      await api.savePushSubscription(body);
    }
  } catch {
    // 设置页再次开启时会在点击手势里重试。
  }
}

export async function beatPushPresence() {
  const registration = await navigator.serviceWorker.getRegistration();
  const subscription = await registration?.pushManager?.getSubscription();
  if (!subscription) return;
  rememberPushEndpoint(subscription.endpoint);
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
  if (!subscription) return;
  await api.deletePushSubscription(subscription.endpoint).catch(() => undefined);
  await subscription.unsubscribe();
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
