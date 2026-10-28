import { useSyncExternalStore } from "react";

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

// 保留在点击调用链中，原生权限请求前不能先等待 Worker 注册等异步操作。
export async function enableNotifications(): Promise<NotificationPermission> {
  const permission =
    Notification.permission === "granted"
      ? "granted"
      : await Notification.requestPermission();
  if (permission === "granted") {
    await notificationWorker();
    setNotificationsEnabled(true);
  }
  return permission;
}
