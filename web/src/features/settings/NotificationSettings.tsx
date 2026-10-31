import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { Button, Field } from "../../shared/ui/components";
import {
  disableNotifications,
  enableNotifications,
  homeScreenRequired,
  loadPushPublicKey,
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
  const [pushConfigured, setPushConfigured] = useState(false);
  const supported = notificationsSupported();
  const needsHomeScreen = supported && homeScreenRequired();
  useEffect(() => {
    if (!supported) return;
    void loadPushPublicKey()
      .then((key) => setPushConfigured(key !== ""))
      .catch(() => setPushConfigured(false));
  }, [supported]);
  async function toggle() {
    setPending(true);
    try {
      if (enabled) {
        await disableNotifications();
        return;
      }
      // 权限请求必须由点击触发，不能在登录或建立实时连接时自动弹窗。
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
        <p className="text-sm text-muted-foreground">
          {t(
            pushConfigured
              ? "切到后台、离开当前卡片，或关闭页面后，有新消息时通知。"
              : "切到后台或离开当前卡片后，有新消息时通知。网页需保持打开。",
          )}
        </p>
        {needsHomeScreen && (
          <p className="text-sm text-muted-foreground">
            {t(
              "iPhone 和 iPad 需要先通过分享菜单把 CycleDock 添加到主屏幕，再用主屏幕图标打开并开启通知。Safari 标签页不能接收推送。",
            )}
          </p>
        )}
        {supported && !pushConfigured && !needsHomeScreen && (
          <p className="text-sm text-muted-foreground">
            {t("关闭页面后的推送尚未配置，当前只在网页打开时通知。")}
          </p>
        )}
        <Button
          onClick={() => void toggle()}
          disabled={
            pending ||
            (!enabled &&
              (!supported || permission === "denied" || needsHomeScreen))
          }
        >
          {t(enabled ? "关闭通知" : "开启通知")}
        </Button>
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
