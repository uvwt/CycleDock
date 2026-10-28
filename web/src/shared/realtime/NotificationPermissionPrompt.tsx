import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Bell } from "lucide-react";
import { toast } from "sonner";
import { Button } from "../ui/components";
import {
  enableNotifications,
  notificationsSupported,
  useNotificationsEnabled,
} from "./notifications";

const dismissedKey = "cycledock.notification-prompt-dismissed";

export function NotificationPermissionPrompt() {
  const { t } = useTranslation();
  const enabled = useNotificationsEnabled();
  const [pending, setPending] = useState(false);
  const [dismissed, setDismissed] = useState(() => {
    try {
      return localStorage.getItem(dismissedKey) === "true";
    } catch {
      return false;
    }
  });

  if (
    dismissed ||
    enabled ||
    !notificationsSupported() ||
    Notification.permission !== "default"
  )
    return null;

  function dismiss() {
    setDismissed(true);
    try {
      localStorage.setItem(dismissedKey, "true");
    } catch {
      // 浏览器禁止持久存储时，当前页面内仍尊重用户的关闭选择。
    }
  }

  async function enable() {
    setPending(true);
    try {
      await enableNotifications();
    } catch (error) {
      toast.error(`${t("浏览器通知启动失败")}: ${String(error)}`);
    } finally {
      setPending(false);
    }
  }

  return (
    <aside
      aria-label={t("浏览器通知")}
      className="fixed right-4 bottom-4 z-40 w-[calc(100%-2rem)] max-w-sm rounded-xl border border-slate-200 bg-white p-4 shadow-lg"
    >
      <div className="flex items-center gap-2 font-medium text-slate-900">
        <Bell className="size-4 text-sky-600" />
        <h2>{t("开启卡片更新通知")}</h2>
      </div>
      <p className="mt-2 text-sm text-slate-500">
        {t("离开卡片或切到后台，也能及时看到模型回复与卡片更新。")}
      </p>
      <div className="mt-4 flex justify-end gap-2">
        <Button onClick={dismiss} disabled={pending}>
          {t("暂不开启")}
        </Button>
        <Button
          variant="primary"
          onClick={() => void enable()}
          disabled={pending}
        >
          {t("开启通知")}
        </Button>
      </div>
    </aside>
  );
}
