import { Bot } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { api } from "../../shared/api/client";
import { keys } from "../../app/query";
import { ErrorView, Section, Spinner } from "../../shared/ui/components";

export function AuthorizedClientsSettings() {
  const { t } = useTranslation();
  const clients = useQuery({
    queryKey: keys.authorizedAIClients,
    queryFn: api.authorizedAIClients,
  });

  return (
    <Section
      title={t("已授权的 AI 客户端")}
      description={t(
        "这些 AI 客户端已通过 CycleDock OAuth 获得当前工作空间的 MCP 访问权限。",
      )}
    >
      {clients.isPending ? (
        <Spinner />
      ) : clients.error ? (
        <ErrorView
          error={clients.error}
          onRetry={() => void clients.refetch()}
        />
      ) : clients.data?.length ? (
        <div className="space-y-2">
          {clients.data.map((client) => (
            <div
              key={client.client_id}
              className="flex items-center justify-between gap-4 rounded-xl border border-line bg-canvas px-4 py-3"
            >
              <div className="flex min-w-0 items-center gap-3">
                <div className="grid size-9 shrink-0 place-items-center rounded-lg bg-primary/10 text-primary">
                  <Bot className="size-4.5" />
                </div>
                <div className="min-w-0">
                  <p className="truncate text-sm font-semibold text-foreground">
                    {client.name}
                  </p>
                  <p className="mt-0.5 text-xs text-muted-foreground">
                    {t("已授权访问 CycleDock MCP")}
                  </p>
                </div>
              </div>
              <time
                dateTime={client.authorized_at}
                className="shrink-0 text-[11px] text-faint"
              >
                {new Date(client.authorized_at).toLocaleString()}
              </time>
            </div>
          ))}
        </div>
      ) : (
        <div className="rounded-xl border border-dashed border-line px-4 py-7 text-center text-sm text-muted-foreground">
          {t("还没有已授权的 AI 客户端。完成 MCP OAuth 授权后会显示在这里。")}
        </div>
      )}
    </Section>
  );
}
