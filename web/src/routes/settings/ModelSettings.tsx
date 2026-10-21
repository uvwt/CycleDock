import { useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import { api } from "../../shared/api/client";
import { keys, queryClient } from "../../app-v2/query";
import {
  Badge,
  Button,
  Input,
  LoadingButton,
  Typography,
} from "../../components/ui/controls";
import {
  DelayedSkeleton,
  ErrorState,
  SettingsSkeleton,
} from "../../components/ui/feedback";

export function ModelSettings() {
  const { t } = useTranslation();
  const statuses = useQuery({ queryKey: keys.auth, queryFn: api.authStatuses });
  const [grokFlow, setGrokFlow] = useState<{
    id: string;
    code: string;
    url: string;
  } | null>(null);
  const [chatgptAuthURL, setChatgptAuthURL] = useState<string | null>(null);
  const [callbackURL, setCallbackURL] = useState("");
  const startChatGPT = useMutation({
    mutationFn: api.chatgptStart,
    onSuccess: (data) => {
      if (data.manual_callback) {
        setChatgptAuthURL(data.authorization_url);
        setCallbackURL("");
      } else window.location.assign(data.authorization_url);
    },
    onError: (error) => toast.error(String(error)),
  });
  const completeChatGPT = useMutation({
    mutationFn: () => api.chatgptComplete(callbackURL.trim()),
    onSuccess: async () => {
      setChatgptAuthURL(null);
      setCallbackURL("");
      await queryClient.invalidateQueries({ queryKey: keys.auth });
      toast.success(t("授权已完成"));
    },
    onError: (error) => toast.error(String(error)),
  });
  const startGrok = useMutation({
    mutationFn: api.grokStart,
    onSuccess: (data) => {
      setGrokFlow({
        id: data.flow_id,
        code: data.user_code,
        url: data.verification_url,
      });
    },
    onError: (error) => toast.error(String(error)),
  });
  const pollGrok = useMutation({
    mutationFn: () => api.grokPoll(grokFlow!.id),
    onSuccess: async (data) => {
      if (data.status === "connected") {
        toast.success(t("Grok 已连接"));
        setGrokFlow(null);
        await queryClient.invalidateQueries({ queryKey: keys.auth });
      } else toast.info(t("授权尚未完成，请在 Grok 页面继续操作"));
    },
    onError: (error) => toast.error(String(error)),
  });
  const disconnect = useMutation({
    mutationFn: api.modelDisconnect,
    onSuccess: () =>
      void queryClient.invalidateQueries({ queryKey: keys.auth }),
    onError: (error) => toast.error(String(error)),
  });
  if (statuses.isPending)
    return (
      <DelayedSkeleton>
        <SettingsSkeleton />
      </DelayedSkeleton>
    );
  if (statuses.isError)
    return (
      <ErrorState
        message={String(statuses.error)}
        onRetry={() => void statuses.refetch()}
      />
    );
  return (
    <div className="space-y-5">
      <Typography scale="control">
        {t("模型授权属于 Agent 配置，不会替代 CycleDock 登录身份。")}
      </Typography>
      {(["chatgpt", "grok"] as const).map((provider) => {
        const item = statuses.data?.find((s) => s.provider === provider);
        // Hosted 缺少被批准的 ChatGPT Client ID 时，不暴露无法使用的连接项。
        if (!item || (!item.available && !item.connected)) return null;
        return (
          <div key={provider} className="v2-panel space-y-3 p-4">
            <div className="flex justify-between gap-2">
              <Typography scale="section">
                {provider === "chatgpt" ? "ChatGPT Plan" : "Grok Subscription"}
              </Typography>
              <Badge tone={item?.connected ? "success" : "neutral"}>
                {item?.connected ? t("已连接") : t("未连接")}
              </Badge>
            </div>
            {item?.warning && (
              <Typography scale="control">{item.warning}</Typography>
            )}
            {item?.reason && (
              <Typography scale="meta">{item.reason}</Typography>
            )}
            <div className="flex flex-wrap gap-2">
              {item?.connected ? (
                <LoadingButton
                  loading={disconnect.isPending}
                  onClick={() => disconnect.mutate(provider)}
                >
                  {t("断开授权")}
                </LoadingButton>
              ) : provider === "chatgpt" ? (
                <LoadingButton
                  disabled={!item?.available}
                  loading={startChatGPT.isPending}
                  onClick={() => startChatGPT.mutate()}
                >
                  {t("通过 ChatGPT 授权")}
                </LoadingButton>
              ) : (
                <LoadingButton
                  disabled={!item?.available}
                  loading={startGrok.isPending}
                  onClick={() => startGrok.mutate()}
                >
                  {t("通过 Grok 授权")}
                </LoadingButton>
              )}
            </div>
            {provider === "chatgpt" && chatgptAuthURL && (
              <div className="space-y-3 border-t border-[var(--v2-border)] pt-3">
                <Typography scale="control">
                  {t(
                    "此环境需要手动完成授权回跳。在新窗口登录后，将浏览器跳转后的完整地址粘贴在下方。",
                  )}
                </Typography>
                <Button asChild>
                  <a
                    href={chatgptAuthURL}
                    target="_blank"
                    rel="noopener noreferrer"
                  >
                    {t("打开 ChatGPT 授权页面")}
                  </a>
                </Button>
                <label className="block space-y-1">
                  <Typography scale="meta">{t("授权回跳 URL")}</Typography>
                  <Input
                    value={callbackURL}
                    onChange={(event) => setCallbackURL(event.target.value)}
                    placeholder={t("粘贴完整的回跳 URL")}
                  />
                </label>
                <div className="flex flex-wrap gap-2">
                  <LoadingButton
                    loading={completeChatGPT.isPending}
                    disabled={!callbackURL.trim()}
                    onClick={() => completeChatGPT.mutate()}
                  >
                    {t("完成授权")}
                  </LoadingButton>
                  <Button
                    onClick={() => {
                      setChatgptAuthURL(null);
                      setCallbackURL("");
                    }}
                  >
                    {t("取消")}
                  </Button>
                </div>
              </div>
            )}
            {provider === "grok" && grokFlow && (
              <div className="space-y-2">
                <Typography scale="control">
                  {t("在授权页面输入代码：")}
                  <strong>{grokFlow.code}</strong>
                </Typography>
                <Button
                  onClick={() =>
                    window.open(grokFlow.url, "_blank", "noopener,noreferrer")
                  }
                >
                  {t("打开授权页面")}
                </Button>
                <LoadingButton
                  loading={pollGrok.isPending}
                  onClick={() => pollGrok.mutate()}
                >
                  {t("检查授权结果")}
                </LoadingButton>
              </div>
            )}
          </div>
        );
      })}
      <Typography scale="meta">
        {t(
          "OpenAI-compatible 模型请在「Agent 设置」中配置 API 地址和环境变量名，凭据不直接写入表单或数据库。",
        )}
      </Typography>
    </div>
  );
}
