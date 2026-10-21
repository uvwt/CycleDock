import { useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import { api } from "../../shared/api/client";
import type { AgentSettings as AgentConfig } from "../../shared/api/types";
import { keys, queryClient } from "../../app/query";
import {
  Button,
  ErrorView,
  Field,
  Section,
  Spinner,
  TextArea,
  TextInput,
} from "../../shared/ui/components";

export function AgentSettings() {
  const { t } = useTranslation();
  const q = useQuery({ queryKey: keys.agent, queryFn: api.agentSettings });
  const [override, setDraft] = useState<AgentConfig | null>(null);
  const draft = override ?? q.data ?? null;
  const save = useMutation({
    mutationFn: () => {
      if (!draft) throw Error("尚未加载");
      const { configured: unused, source_id: unusedSource, ...input } = draft;
      void unused;
      void unusedSource;
      return api.saveAgent(input);
    },
    onSuccess: (r) => {
      queryClient.setQueryData(keys.agent, r);
      toast.success(t("Agent 配置已保存"));
    },
    onError: (e) => toast.error(String(e)),
  });
  return q.isPending ? (
    <Spinner />
  ) : q.error ? (
    <ErrorView error={q.error} />
  ) : (
    draft && (
      <Section
        title={t("内置 Agent")}
        description={t(
          "这些配置作用于整个 Workspace；具体模型连接请前往「模型服务」。",
        )}
      >
        <div className="space-y-5">
          <label className="flex items-center justify-between gap-3 text-sm font-medium">
            {t("启用 Cycle Agent")}{" "}
            <input
              type="checkbox"
              className="size-4 accent-sky-700"
              checked={draft.enabled}
              onChange={(e) =>
                setDraft({ ...draft, enabled: e.target.checked })
              }
            />
          </label>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label={t("请求超时（毫秒）")}>
              <TextInput
                type="number"
                min={1000}
                max={600000}
                value={draft.request_timeout_ms}
                onChange={(e) =>
                  setDraft({
                    ...draft,
                    request_timeout_ms: Number(e.target.value),
                  })
                }
              />
            </Field>
            <Field label={t("最大工具轮次")}>
              <TextInput
                type="number"
                min={1}
                max={32}
                value={draft.max_tool_rounds}
                onChange={(e) =>
                  setDraft({
                    ...draft,
                    max_tool_rounds: Number(e.target.value),
                  })
                }
              />
            </Field>
          </div>
          <Field
            label="Workspace Agent Instructions"
            hint={t("整个工作空间的默认指令，与单个项目的指令分开保存。")}
          >
            <TextArea
              rows={8}
              value={draft.system_instructions}
              onChange={(e) =>
                setDraft({ ...draft, system_instructions: e.target.value })
              }
            />
          </Field>
          <Button
            variant="primary"
            disabled={save.isPending}
            onClick={() => save.mutate()}
          >
            {t("保存 Agent 设置")}
          </Button>
        </div>
      </Section>
    )
  );
}
