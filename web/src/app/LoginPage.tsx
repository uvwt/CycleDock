import { useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { Link, useLocation } from "react-router-dom";
import { ArrowRight, ShieldCheck } from "lucide-react";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import { api } from "../shared/api/client";
import { keys, queryClient } from "./query";
import {
  Button,
  ErrorView,
  Field,
  Spinner,
  TextInput,
} from "../shared/ui/components";
export function LoginPage() {
  const { t } = useTranslation();
  const methods = useQuery({
    queryKey: ["login-methods"],
    queryFn: api.loginMethods,
  });
  const [password, setPassword] = useState("");
  const location = useLocation();
  const returnTo = new URLSearchParams(location.search).get("return_to");
  const safeReturn =
    returnTo?.startsWith("/") && !returnTo.startsWith("//")
      ? returnTo
      : "/chat";
  const login = useMutation({
    mutationFn: () => api.login(password),
    onSuccess: (me) => {
      queryClient.setQueryData(keys.me, me);
      window.location.assign(safeReturn);
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "登录失败"),
  });
  return (
    <main className="grid min-h-screen bg-slate-50 lg:grid-cols-[1fr_1fr]">
      <section className="hidden flex-col justify-between bg-slate-900 p-12 text-white lg:flex">
        <Link to="/chat" className="flex items-center gap-3 text-xl font-bold">
          <span className="grid size-10 place-items-center rounded-xl bg-sky-400 text-slate-900">
            C
          </span>
          CycleDock
        </Link>
        <div>
          <p className="mb-5 text-sm text-sky-300">Human × Agent</p>
          <h1 className="max-w-lg text-5xl leading-tight font-semibold tracking-tight">
            {t("把讨论变成工作。")}
            <br />
            {t("让进展清晰可见。")}
          </h1>
          <p className="mt-6 max-w-md text-slate-300">
            {t("统一的对话时间线、看板、持续工作与 AI Agent。")}
          </p>
        </div>
        <div className="flex items-center gap-2 text-sm text-slate-400">
          <ShieldCheck className="size-4" />
          {t("工作空间权限由服务端维护")}
        </div>
      </section>
      <section className="flex items-center justify-center px-6 py-16">
        <div className="w-full max-w-sm space-y-6">
          <h2 className="text-3xl font-semibold text-slate-900">
            {t("登录 CycleDock")}
          </h2>
          <p className="text-sm text-slate-500">{t("继续进入你的工作空间")}</p>
          {methods.isPending && <Spinner />}
          {methods.error && <ErrorView error={methods.error} />}
          {methods.data?.providers.map((p) => (
            <a
              key={p.id}
              href={
                p.start_url +
                (p.start_url.includes("?") ? "&" : "?") +
                "return_to=" +
                encodeURIComponent(safeReturn)
              }
              className="flex min-h-12 items-center justify-center gap-2 rounded-xl border border-slate-200 bg-white font-medium hover:border-sky-400"
            >
              {t("使用")} {p.name} {t("登录")} <ArrowRight className="size-4" />
            </a>
          ))}
          {methods.data?.password && (
            <form
              onSubmit={(e) => {
                e.preventDefault();
                login.mutate();
              }}
              className="space-y-4"
            >
              <Field label={t("管理员密码")}>
                <TextInput
                  type="password"
                  autoComplete="current-password"
                  required
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  placeholder={t("输入管理员密码")}
                />
              </Field>
              <Button
                variant="primary"
                type="submit"
                disabled={login.isPending || !password}
                className="w-full"
              >
                {t("登录")}
              </Button>
            </form>
          )}
          <p className="text-xs text-slate-400">
            {t(
              "Hosted 使用 NexusDock 统一认证；Self-hosted 可使用管理员密码。",
            )}
          </p>
        </div>
      </section>
    </main>
  );
}
