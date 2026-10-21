import { useEffect, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import { z } from "zod";
import { api } from "../../shared/api/client";
import { keys, queryClient } from "../../app-v2/query";
import {
  Input,
  LoadingButton,
  Textarea,
  Typography,
} from "../../components/ui/controls";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "../../components/ui/overlays";
import {
  DelayedSkeleton,
  ErrorState,
  SettingsSkeleton,
} from "../../components/ui/feedback";

const projectSchema = z.object({
  name: z.string().trim().min(1, "请输入项目名称").max(120),
  description: z.string(),
  instructions: z.string(),
});
type ProjectFormValues = z.infer<typeof projectSchema>;

export function ProjectForm() {
  const { t } = useTranslation();
  const projects = useQuery({ queryKey: keys.projects, queryFn: api.projects });
  const [selected, setSelected] = useState("");
  const current =
    projects.data?.find((p) => p.id === selected) || projects.data?.[0];
  const form = useForm<ProjectFormValues>({
    resolver: zodResolver(projectSchema),
    defaultValues: { name: "", description: "", instructions: "" },
  });
  useEffect(() => {
    if (current)
      form.reset({
        name: current.name,
        description: current.description,
        instructions: current.instructions,
      });
  }, [current, form]);
  const save = useMutation({
    mutationFn: (data: ProjectFormValues) =>
      api.updateProject(current!.id, data),
    onSuccess: async () => {
      toast.success(t("项目设置已保存"));
      await queryClient.invalidateQueries({ queryKey: keys.projects });
    },
    onError: (error) => toast.error(String(error)),
  });
  if (projects.isPending)
    return (
      <DelayedSkeleton>
        <SettingsSkeleton />
      </DelayedSkeleton>
    );
  if (projects.isError)
    return (
      <ErrorState
        message={String(projects.error)}
        onRetry={() => void projects.refetch()}
      />
    );
  if (!current)
    return (
      <Typography scale="control">
        {t("没有项目，请先在工作台创建项目。")}
      </Typography>
    );
  return (
    <form
      className="space-y-5"
      onSubmit={form.handleSubmit((data) => save.mutate(data))}
    >
      <label className="block max-w-sm space-y-2">
        <Typography scale="control">{t("选择项目")}</Typography>
        <Select value={current.id} onValueChange={setSelected}>
          <SelectTrigger aria-label={t("设置项目")}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {projects.data?.map((p) => (
              <SelectItem key={p.id} value={p.id}>
                {p.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </label>
      <label className="block max-w-lg space-y-1">
        <Typography scale="control">{t("名称")}</Typography>
        <Input
          {...form.register("name")}
          aria-invalid={!!form.formState.errors.name}
        />
        {form.formState.errors.name && (
          <Typography scale="meta" className="text-[var(--v2-danger)]">
            {t(form.formState.errors.name.message || "")}
          </Typography>
        )}
      </label>
      <label className="block max-w-lg space-y-1">
        <Typography scale="control">{t("描述")}</Typography>
        <Textarea {...form.register("description")} rows={3} />
      </label>
      <label className="block space-y-1">
        <Typography scale="control">Project Agent Instructions</Typography>
        <Textarea {...form.register("instructions")} rows={6} />
      </label>
      <Typography scale="meta">
        {t("Project Instructions 仅作用于此项目，不影响其他项目。")}
      </Typography>
      <LoadingButton type="submit" variant="primary" loading={save.isPending}>
        {t("保存项目")}
      </LoadingButton>
    </form>
  );
}
