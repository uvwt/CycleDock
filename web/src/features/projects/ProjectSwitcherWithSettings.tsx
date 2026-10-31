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
}: {
  projects: Project[];
  projectID: string;
  onChange: (projectID: string) => void;
  onCreate?: () => void;
  compact?: boolean;
}) {
  const [settingsOpen, setSettingsOpen] = useState(false);
  const project = projects.find((item) => item.id === projectID);

  return (
    <>
      <ProjectSwitcher
        projects={projects}
        projectID={projectID}
        onChange={onChange}
        onCreate={onCreate}
        onManage={project ? () => setSettingsOpen(true) : undefined}
        compact={compact}
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
  const [instructions, setInstructions] = useState(project.instructions);

  const save = useMutation({
    mutationFn: () =>
      api.updateProject(project.id, {
        name: name.trim(),
        description: description.trim(),
        instructions: instructions.trim(),
      }),
    onSuccess: async (updated) => {
      setName(updated.name);
      setDescription(updated.description);
      setInstructions(updated.instructions);
      await queryClient.invalidateQueries({ queryKey: keys.projects });
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
      description={t(
        "修改当前项目的名称、描述与 Agent 指令。Project Instructions 只作用于这个项目。",
      )}
    >
      <div className="space-y-4 pt-1">
        <Field label={t("名称")}>
          <TextInput
            value={name}
            onChange={(event) => setName(event.target.value)}
          />
        </Field>
        <Field label={t("描述")}>
          <TextArea
            rows={3}
            value={description}
            onChange={(event) => setDescription(event.target.value)}
          />
        </Field>
        <Field label="Project Agent Instructions">
          <TextArea
            rows={6}
            value={instructions}
            onChange={(event) => setInstructions(event.target.value)}
            placeholder={t("此项目的长期 Agent 工作指令")}
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
