import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { api } from "../../shared/api/client";
import { keys } from "../../app/query";
import { useAgentStatus } from "./useAgentStatus";

export function ModelSetupNotice() {
  const status = useAgentStatus();
  const me = useQuery({ queryKey: keys.me, queryFn: api.me });
  const { t } = useTranslation();
  if (status.data?.model_configured) return null;

  return (
    <div
      role="status"
      className="mx-1 mb-2 rounded-lg bg-muted px-3 py-2 text-xs text-muted-foreground"
    >
      {status.isError ? (
        <>
          {t("无法确认模型配置，请重试。")}{" "}
          <button
            type="button"
            onClick={() => void status.refetch()}
            className="text-primary underline"
          >
            {t("重试")}
          </button>
        </>
      ) : status.isPending ? (
        t("正在检查模型配置…")
      ) : (
        <>
          <p>
            {t(
              "尚未配置可用模型，Agent 暂时无法回复。草稿会保留，已排队的消息将在配置完成后继续执行。",
            )}
          </p>
          {me.data?.role === "owner" ? (
            <Link
              to="/settings/models"
              className="mt-1 inline-block text-primary underline"
            >
              {t("配置模型")}
            </Link>
          ) : (
            <p className="mt-1">{t("请联系工作区所有者配置并启用模型。")}</p>
          )}
        </>
      )}
    </div>
  );
}
