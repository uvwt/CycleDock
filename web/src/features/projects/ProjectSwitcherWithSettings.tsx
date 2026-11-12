import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import type { Project } from "../../shared/api/types";
import { api } from "../../shared/api/client";
import { keys, queryClient } from "../../app/query";
import {
  Button,
  Field,
  Modal,
  TextArea,
  TextInput,
} from "../../shared/ui/components";
import { ProjectSwitcher } from "../../shared/ui/ProjectSwitcher";

export function ProjectSwitcherWithSettings({
  projects,
  projectID,
  onChange,
  onCreate,
  compact = false,
  compactSize = "md",
  minimal = false,
}: {
  projects: Project[];
  projectID: string;
  onChange: (projectID: string) => void;
  onCreate?: () => void;
  compact?: boolean;
  compactSize?: "sm" | "md";
  minimal?: boolean;
}) {
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [createOpen, setCreateOpen] = useState(false);
  const project = projects.find((item) => item.id === projectID);

  return (
    <>
      <ProjectSwitcher
        projects={projects}
        projectID={projectID}
        onChange={onChange}
        onCreate={onCreate ?? (() => setCreateOpen(true))}
        onManage={project ? () => setSettingsOpen(true) : undefined}
        compact={compact}
        compactSize={compactSize}
        minimal={minimal}
      />
      <ProjectCreateDialog
        open={createOpen}
        onOpenChange={setCreateOpen}
        onCreated={onChange}
      />
      {project && (
        <ProjectSettingsDialog
          key={project.id}
          project={project}
          open={settingsOpen}
          onOpenChange={setSettingsOpen}
        />
      )}
    </>
  );
}

function ProjectCreateDialog({
  open,
  onOpenChange,
  onCreated,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onCreated: (projectID: string) => void;
}) {
  const { t } = useTranslation();
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");

  const create = useMutation({
    mutationFn: () =>
      api.createProject({
        name: name.trim(),
        description: description.trim(),
      }),
    onSuccess: (created) => {
      setName("");
      setDescription("");
      queryClient.setQueryData<Project[]>(keys.projects, (old) =>
        old ? [...old, created] : [created],
      );
      void queryClient.invalidateQueries({ queryKey: keys.projects });
      toast.success(t("项目已创建"));
      onOpenChange(false);
      onCreated(created.id);
    },
    onError: (error) => toast.error(String(error)),
  });

  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      title={t("新建项目")}
      description={t("创建一个新的项目看板来组织任务与 Agent 会话。")}
    >
      <form
        onSubmit={(event) => {
          event.preventDefault();
          if (name.trim() && !create.isPending) {
            create.mutate();
          }
        }}
        className="space-y-4 pt-1"
      >
        <Field label={t("项目名称")}>
          <TextInput
            autoFocus
            required
            value={name}
            onChange={(event) => setName(event.target.value)}
            placeholder={t("输入项目名称")}
          />
        </Field>
        <Field label={t("项目描述")}>
          <TextArea
            rows={4}
            value={description}
            onChange={(event) => setDescription(event.target.value)}
            placeholder={t("描述该项目的工作范围或目标")}
          />
        </Field>
        <div className="flex justify-end gap-2 pt-2">
          <Button
            variant="ghost"
            type="button"
            onClick={() => onOpenChange(false)}
          >
            {t("取消")}
          </Button>
          <Button
            variant="primary"
            type="submit"
            disabled={!name.trim() || create.isPending}
          >
            {t("创建项目")}
          </Button>
        </div>
      </form>
    </Modal>
  );
}

function ProjectSettingsDialog({
  project,
  open,
  onOpenChange,
}: {
  project: Project;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const { t } = useTranslation();
  const [name, setName] = useState(project.name);
  const [description, setDescription] = useState(project.description);

  const save = useMutation({
    mutationFn: () =>
      api.updateProject(project.id, {
        name: name.trim(),
        description: description.trim(),
      }),
    onSuccess: (updated) => {
      setName(updated.name);
      setDescription(updated.description);
      queryClient.setQueryData<Project[]>(keys.projects, (old) =>
        old
          ? old.map((item) => (item.id === updated.id ? updated : item))
          : old,
      );
      void queryClient.invalidateQueries({ queryKey: keys.projects });
      toast.success(t("项目已保存"));
      onOpenChange(false);
    },
    onError: (error) => toast.error(String(error)),
  });

  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      title={t("项目设置")}
      description={t("修改当前项目的名称与项目说明。")}
    >
      <div className="space-y-4 pt-1">
        <Field label={t("名称")}>
          <TextInput
            value={name}
            onChange={(event) => setName(event.target.value)}
          />
        </Field>
        <Field label={t("项目说明")}>
          <TextArea
            rows={6}
            value={description}
            onChange={(event) => setDescription(event.target.value)}
            placeholder={t(
              "介绍项目背景、目标，以及希望 Agent 遵守的工作要求。对项目内所有 Card 生效。",
            )}
          />
        </Field>
        <div className="flex justify-end gap-2 pt-2">
          <Button variant="ghost" onClick={() => onOpenChange(false)}>
            {t("取消")}
          </Button>
          <Button
            variant="primary"
            disabled={!name.trim() || save.isPending}
            onClick={() => save.mutate()}
          >
            {t("保存项目")}
          </Button>
        </div>
      </div>
    </Modal>
  );
}
