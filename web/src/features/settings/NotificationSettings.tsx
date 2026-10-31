import { useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { Field } from "../../shared/ui/components";
import {
  disableNotifications,
  enableNotifications,
  homeScreenRequired,
  notificationsSupported,
  useNotificationsEnabled,
} from "../../shared/realtime/notifications";

export function NotificationSettings() {
  const { t } = useTranslation();
  const enabled = useNotificationsEnabled();
  const [pending, setPending] = useState(false);
  const [permission, setPermission] = useState(() =>
    notificationsSupported() ? Notification.permission : "denied",
  );
  const supported = notificationsSupported();
  const needsHomeScreen = supported && homeScreenRequired();
  const canEnable = supported && permission !== "denied" && !needsHomeScreen;

  async function toggle() {
    setPending(true);
    try {
      if (enabled) {
        await disableNotifications();
        return;
      }
      // 浏览器要求通知权限必须由真实用户点击触发，不能在页面加载时自动申请。
      const next = await enableNotifications();
      setPermission(next);
    } catch (error) {
      toast.error(`${t("浏览器通知启动失败")}: ${String(error)}`);
    } finally {
      setPending(false);
    }
  }

  return (
    <Field label={t("浏览器通知")}>
      <div className="space-y-2">
        <div className="flex min-h-8 items-center justify-between gap-4">
          <span className="text-sm text-muted-foreground">
            {t(enabled ? "已开启" : "未开启")}
          </span>
          <button
            type="button"
            role="switch"
            aria-label={t("浏览器通知")}
            aria-checked={enabled}
            disabled={pending || (!enabled && !canEnable)}
            onClick={() => void toggle()}
            className={`relative inline-flex h-6 w-11 shrink-0 items-center rounded-full transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary/40 disabled:cursor-not-allowed disabled:opacity-40 ${
              enabled ? "bg-primary" : "bg-muted-foreground/30"
            }`}
          >
            <span
              aria-hidden="true"
              className={`block size-5 rounded-full bg-white shadow-sm transition-transform ${
                enabled ? "translate-x-5" : "translate-x-0.5"
              }`}
            />
          </button>
        </div>

        {needsHomeScreen && (
          <p className="text-sm text-muted-foreground">
            {t(
              "iPhone 和 iPad 需要先通过分享菜单把 CycleDock 添加到主屏幕，再用主屏幕图标打开并开启通知。Safari 标签页不能接收推送。",
            )}
          </p>
        )}
        {!supported && (
          <p className="text-sm text-muted-foreground">
            {t("当前环境不支持浏览器通知，请使用 HTTPS 或本机浏览器。")}
          </p>
        )}
        {supported && permission === "denied" && (
          <p className="text-sm text-muted-foreground">
            {t("通知权限已被阻止，请在浏览器的网站设置中允许通知。")}
          </p>
        )}
      </div>
    </Field>
  );
}
