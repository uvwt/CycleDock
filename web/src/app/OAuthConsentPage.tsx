import { useMemo, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useLocation, useNavigate } from "react-router-dom";
import { KeyRound, LayoutList, ShieldAlert } from "lucide-react";
import { useTranslation } from "react-i18next";
import { api, APIError } from "../shared/api/client";
import type { OAuthConsentParams } from "../shared/api/types";
import { Button, ErrorView, Skeleton } from "../shared/ui/components";

function parseConsentQuery(search: string): {
  queryError: string;
  params: OAuthConsentParams;
} {
  const sp = new URLSearchParams(search);
  return {
    queryError: sp.get("error")?.trim() ?? "",
    params: {
      response_type: sp.get("response_type")?.trim() ?? "",
      client_id: sp.get("client_id")?.trim() ?? "",
      redirect_uri: sp.get("redirect_uri")?.trim() ?? "",
      scope: sp.get("scope")?.trim() ?? "",
      state: sp.get("state") ?? "",
      code_challenge: sp.get("code_challenge")?.trim() ?? "",
      code_challenge_method: sp.get("code_challenge_method")?.trim() ?? "",
      resource: sp.get("resource")?.trim() ?? "",
    },
  };
}

export function OAuthConsentPage() {
  const { t } = useTranslation();
  const location = useLocation();
  const navigate = useNavigate();
  const [submitError, setSubmitError] = useState<unknown>(null);

  const { queryError, params } = useMemo(
    () => parseConsentQuery(location.search),
    [location.search],
  );

  const hasRequiredParams = Boolean(
    !queryError &&
      params.response_type &&
      params.client_id &&
      params.redirect_uri &&
      params.code_challenge &&
      params.code_challenge_method,
  );

  const consentQuery = useQuery({
    queryKey: ["oauth-consent", location.search],
    queryFn: () => api.getOAuthConsent(params),
    enabled: hasRequiredParams,
    retry: false,
  });

  const decideMutation = useMutation({
    mutationFn: (decision: "approve" | "deny") =>
      api.submitOAuthConsent({ ...params, decision }),
    onMutate: () => {
      setSubmitError(null);
    },
    onSuccess: (result) => {
      // 通过浏览器顶层导航跳回已注册的第三方回调地址，避免 HTML form POST 触发 CSP form-action 拦截。
      window.location.assign(result.redirect_to);
    },
    onError: (err) => {
      setSubmitError(err);
    },
  });

  const displayError = useMemo(() => {
    if (queryError) {
      return new APIError(
        400,
        queryError,
        queryError === "server_error"
          ? t("OAuth 授权服务暂时不可用，请稍后回到客户端重试。")
          : t("授权请求无效或已失效，请回到客户端重新发起授权。"),
      );
    }
    if (!hasRequiredParams) {
      return new APIError(
        400,
        "oauth_request_invalid",
        t("授权参数不完整，请回到客户端重新发起授权。"),
      );
    }
    return submitError ?? consentQuery.error ?? null;
  }, [consentQuery.error, hasRequiredParams, queryError, submitError, t]);

  const info = consentQuery.data;
  const clientName = info?.client_name || t("未命名 MCP 客户端");

  return (
    <main className="scrollbar-thin flex h-full min-h-0 flex-col items-center overflow-y-auto overscroll-contain bg-canvas px-4 pt-[calc(env(safe-area-inset-top,0px)+1.25rem)] pb-[max(1.25rem,env(safe-area-inset-bottom,0px))]">
      <div className="my-auto w-full max-w-[460px] space-y-3">
        <section className="rounded-2xl border border-line bg-surface p-5 shadow-sm sm:p-6">
          <header className="flex items-center justify-between gap-3 border-b border-line pb-4">
            <div className="flex items-center gap-2.5 text-base font-bold tracking-tight text-heading">
              <img
                src="/cycledock-logo.png"
                alt="CycleDock"
                className="size-8 shrink-0"
              />
              <span>CycleDock</span>
            </div>
            <span className="inline-flex h-6 shrink-0 items-center rounded-full border border-sky-200 bg-sky-50 px-2.5 text-xs font-semibold text-primary dark:border-sky-400/30 dark:bg-sky-500/15 dark:text-sky-300">
              MCP OAuth
            </span>
          </header>

          {displayError ? (
            <div className="mt-4 space-y-4">
              <div>
                <h1 className="text-xl font-semibold tracking-tight text-heading">
                  {t("授权访问工作空间")}
                </h1>
                <p className="mt-1.5 text-sm leading-6 text-muted-foreground">
                  {t("无法完成本次授权请求。")}
                </p>
              </div>
              <ErrorView error={displayError} />
              <Button
                type="button"
                variant="secondary"
                size="lg"
                className="h-10 w-full rounded-xl text-sm"
                onClick={() => navigate("/chat", { replace: true })}
              >
                {t("返回工作台")}
              </Button>
            </div>
          ) : consentQuery.isPending || !info ? (
            <div className="mt-4 space-y-4" aria-busy="true">
              <div className="space-y-2">
                <Skeleton className="h-7 w-44" />
                <Skeleton className="h-4 w-full" />
              </div>
              <Skeleton className="h-32 w-full rounded-xl" />
              <div className="space-y-2.5">
                <Skeleton className="h-14 w-full rounded-xl" />
                <Skeleton className="h-14 w-full rounded-xl" />
                <Skeleton className="h-14 w-full rounded-xl" />
              </div>
              <div className="grid grid-cols-2 gap-2.5 pt-1">
                <Skeleton className="h-11 w-full rounded-xl" />
                <Skeleton className="h-11 w-full rounded-xl" />
              </div>
            </div>
          ) : (
            <div className="mt-4 space-y-4">
              <div>
                <h1 className="text-xl font-semibold tracking-tight text-heading sm:text-[22px]">
                  {t("授权访问工作空间")}
                </h1>
                <p className="mt-1 text-sm leading-6 text-muted-foreground">
                  {t("请确认是否允许该外部客户端通过 MCP 接入你的 CycleDock 工作空间。")}
                </p>
              </div>

              <div className="overflow-hidden rounded-xl border border-line bg-muted/60">
                <div className="flex flex-col gap-1 px-4 py-2.5 sm:flex-row sm:items-baseline sm:justify-between sm:gap-4">
                  <span className="shrink-0 text-xs font-medium text-muted-foreground">
                    {t("请求客户端")}
                  </span>
                  <strong
                    className="text-sm font-semibold text-heading sm:text-right break-words"
                    data-testid="consent-client"
                  >
                    {clientName}
                  </strong>
                </div>
                <div className="flex flex-col gap-1 border-t border-line px-4 py-2.5 sm:flex-row sm:items-baseline sm:justify-between sm:gap-4">
                  <span className="shrink-0 text-xs font-medium text-muted-foreground">
                    {t("目标工作空间")}
                  </span>
                  <strong
                    className="text-sm font-semibold text-heading sm:text-right break-words"
                    data-testid="consent-workspace"
                  >
                    {info.workspace_name}
                  </strong>
                </div>
                <div className="grid gap-1 border-t border-line px-4 py-2.5">
                  <span className="text-xs font-medium text-muted-foreground">
                    {t("授权回调地址")}
                  </span>
                  <span
                    className="break-all font-mono text-xs leading-5 text-foreground"
                    data-testid="consent-redirect-uri"
                  >
                    {info.redirect_uri}
                  </span>
                </div>
              </div>

              <div className="space-y-2">
                <p className="text-xs font-semibold text-muted-foreground">
                  {t("权限范围")}
                </p>
                <ul className="space-y-2">
                  <li className="flex items-start gap-2.5 rounded-xl border border-line bg-surface px-3.5 py-2.5">
                    <span className="mt-0.5 grid size-6 shrink-0 place-items-center rounded-lg bg-sky-50 text-primary dark:bg-sky-500/15 dark:text-sky-300">
                      <KeyRound className="size-3.5" />
                    </span>
                    <div className="min-w-0 text-xs leading-5">
                      <p className="font-semibold text-heading">
                        {t("独立的 OAuth Client Source")}
                      </p>
                      <p className="text-muted-foreground">
                        {t("为该客户端分配独立身份标识，可在设置中随时查看或撤销授权。")}
                      </p>
                    </div>
                  </li>
                  <li className="flex items-start gap-2.5 rounded-xl border border-line bg-surface px-3.5 py-2.5">
                    <span className="mt-0.5 grid size-6 shrink-0 place-items-center rounded-lg bg-sky-50 text-primary dark:bg-sky-500/15 dark:text-sky-300">
                      <LayoutList className="size-3.5" />
                    </span>
                    <div className="min-w-0 text-xs leading-5">
                      <p className="font-semibold text-heading">
                        {t("通过 MCP 读取和更新工作")}
                      </p>
                      <p className="text-muted-foreground">
                        {t("可读取项目与卡片上下文，并提交进度回复、巡检结论与待验收确认。")}
                      </p>
                    </div>
                  </li>
                  <li className="flex items-start gap-2.5 rounded-xl border border-warning-line bg-warning-muted px-3.5 py-2.5">
                    <span className="mt-0.5 grid size-6 shrink-0 place-items-center rounded-lg text-warning">
                      <ShieldAlert className="size-3.5" />
                    </span>
                    <div className="min-w-0 text-xs leading-5">
                      <p className="font-semibold text-warning">
                        {t("人工验收边界受保护")}
                      </p>
                      <p className="text-foreground">
                        {t("不可代替人类完成 Review → Done 最终验收。")}
                      </p>
                    </div>
                  </li>
                </ul>
              </div>

              <div className="flex flex-col gap-2.5 pt-1 sm:flex-row-reverse">
                <Button
                  type="button"
                  variant="primary"
                  size="lg"
                  className="h-10 flex-1 rounded-xl text-sm"
                  data-testid="consent-approve"
                  disabled={decideMutation.isPending}
                  onClick={() => decideMutation.mutate("approve")}
                >
                  {decideMutation.isPending &&
                  decideMutation.variables === "approve"
                    ? t("正在授权…")
                    : t("授权")}
                </Button>
                <Button
                  type="button"
                  variant="secondary"
                  size="lg"
                  className="h-10 flex-1 rounded-xl text-sm"
                  data-testid="consent-deny"
                  disabled={decideMutation.isPending}
                  onClick={() => decideMutation.mutate("deny")}
                >
                  {decideMutation.isPending &&
                  decideMutation.variables === "deny"
                    ? t("正在处理…")
                    : t("拒绝")}
                </Button>
              </div>
            </div>
          )}
        </section>

        <p className="text-center text-xs text-faint">
          {t("授权后可随时在 CycleDock 设置 → AI 客户端中撤销访问权限。")}
        </p>
      </div>
    </main>
  );
}
