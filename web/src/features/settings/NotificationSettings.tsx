import { useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { Button, Field } from "../../shared/ui/components";
import {
  enableNotifications,
  notificationsSupported,
  setNotificationsEnabled,
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
  async function toggle() {
    setPending(true);
    try {
      if (enabled) {
        setNotificationsEnabled(false);
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
        <p className="text-sm text-slate-500">
          {t("切到后台或离开当前卡片后，卡片有更新时通知。网页需保持打开。")}
        </p>
        <Button
          onClick={() => void toggle()}
          disabled={
            pending || (!enabled && (!supported || permission === "denied"))
          }
        >
          {t(enabled ? "关闭通知" : "开启通知")}
        </Button>
        {!supported && (
          <p className="text-sm text-slate-500">
            {t("当前环境不支持浏览器通知，请使用 HTTPS 或本机浏览器。")}
          </p>
        )}
        {supported && permission === "denied" && (
          <p className="text-sm text-slate-500">
            {t("通知权限已被阻止，请在浏览器的网站设置中允许通知。")}
          </p>
        )}
      </div>
    </Field>
  );
}
