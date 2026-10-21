import { useTranslation } from "react-i18next";
import { useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { api } from "../../shared/api/client";
import { keys, queryClient } from "../../app-v2/query";
import {
  Button,
  Input,
  LoadingButton,
  Typography,
} from "../../components/ui/controls";
import {
  DelayedSkeleton,
  ErrorState,
  Skeleton,
} from "../../components/ui/feedback";

export function LoginV2() {
  const { t } = useTranslation();
  const loginMethods = useQuery({
    queryKey: ["login-methods"],
    queryFn: api.loginMethods,
    retry: false,
  });
  const [password, setPassword] = useState("");
  const login = useMutation({
    mutationFn: () => api.login(password),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: keys.me }),
  });
  return (
    <main className="v2 grid min-h-dvh place-items-center p-4">
      <section className="v2-panel w-full max-w-sm p-7">
        <div className="mb-7 flex items-center gap-3">
          <img
            src="/cycledock-logo.png"
            className="size-10 object-contain"
            alt="CycleDock"
          />
          <Typography as="h1" scale="title">
            CycleDock
          </Typography>
        </div>
        <Typography as="h2" scale="section">
          {t("登录工作空间")}
        </Typography>
        <Typography scale="control" className="mt-1 text-[var(--v2-muted)]">
          {t("登录后继续管理讨论和工作。")}
        </Typography>
        {loginMethods.isPending ? (
          <DelayedSkeleton>
            <Skeleton className="mt-6 h-9 w-full" />
          </DelayedSkeleton>
        ) : loginMethods.isError ? (
          <ErrorState
            message={t("无法读取可用登录方式")}
            onRetry={() => void loginMethods.refetch()}
          />
        ) : (
          <div className="mt-5 space-y-3">
            {loginMethods.data?.providers.map((provider) => (
              <Button
                key={provider.id}
                className="w-full"
                onClick={() => window.location.assign(provider.start_url)}
              >
                {t("使用")} {provider.name} {t("登录")}
              </Button>
            ))}
            {loginMethods.data?.password && (
              <form
                className="space-y-3"
                onSubmit={(event) => {
                  event.preventDefault();
                  login.mutate();
                }}
              >
                <label className="block space-y-1">
                  <Typography scale="control">{t("管理员密码")}</Typography>
                  <Input
                    autoComplete="current-password"
                    type="password"
                    value={password}
                    onChange={(event) => setPassword(event.target.value)}
                    required
                  />
                </label>
                <LoadingButton
                  className="w-full"
                  variant="primary"
                  type="submit"
                  loading={login.isPending}
                >
                  {t("登录")}
                </LoadingButton>
                {login.isError && (
                  <p
                    role="alert"
                    className="v2-text-control text-[var(--v2-danger)]"
                  >
                    {String(login.error)}
                  </p>
                )}
              </form>
            )}
            {!loginMethods.data?.password &&
              !loginMethods.data?.providers.length && (
                <p className="v2-text-control">{t("当前未配置登录方式。")}</p>
              )}
          </div>
        )}
      </section>
    </main>
  );
}
