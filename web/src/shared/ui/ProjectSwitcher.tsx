import * as Dialog from "@radix-ui/react-dialog";
import {
  Check,
  ChevronDown,
  FolderKanban,
  FolderPlus,
  Settings2,
  X,
} from "lucide-react";
import { useTranslation } from "react-i18next";
import type { Project } from "../api/types";

export function ProjectSwitcher({
  projects,
  projectID,
  onChange,
  onCreate,
  onManage,
  compact = false,
}: {
  projects: Project[];
  projectID: string;
  onChange: (projectID: string) => void;
  onCreate?: () => void;
  onManage?: () => void;
  compact?: boolean;
}) {
  const { t } = useTranslation();
  const current = projects.find((project) => project.id === projectID);

  return (
    <Dialog.Root>
      <Dialog.Trigger asChild>
        <button
          type="button"
          title={current?.name || t("选择项目")}
          aria-label={compact ? t("选择项目") : undefined}
          disabled={!projects.length && !onCreate}
          className={
            compact
              ? "grid size-10 place-items-center rounded-xl text-slate-400 transition hover:bg-slate-50 hover:text-slate-800 disabled:opacity-40"
              : "flex min-h-10 w-full items-center gap-2.5 rounded-xl border border-slate-200 bg-white px-3 text-left text-sm text-slate-700 shadow-sm transition hover:border-slate-300 hover:bg-slate-50 disabled:opacity-40"
          }
        >
          <FolderKanban
            className={compact ? "size-5" : "size-4 text-slate-400"}
          />
          {!compact && (
            <>
              <span className="min-w-0 flex-1 truncate font-medium">
                {current?.name || t("选择项目")}
              </span>
              <ChevronDown className="size-4 shrink-0 text-slate-400" />
            </>
          )}
        </button>
      </Dialog.Trigger>

      <Dialog.Portal>
        <Dialog.Overlay
          className={`fixed inset-0 z-[60] ${
            compact
              ? "bg-slate-950/15 backdrop-blur-[1px] md:bg-transparent md:backdrop-blur-none"
              : "bg-slate-950/20 backdrop-blur-[1px]"
          }`}
        />
        <Dialog.Content
          aria-describedby={undefined}
          className={`fixed z-[70] rounded-2xl border border-slate-200 bg-white p-2 shadow-[0_24px_80px_rgba(15,23,42,0.22)] outline-none ${
            compact
              ? "top-1/2 left-1/2 w-[min(92vw,360px)] -translate-x-1/2 -translate-y-1/2 md:top-auto md:bottom-3 md:left-[4.5rem] md:w-[280px] md:translate-x-0 md:translate-y-0"
              : "top-1/2 left-1/2 w-[min(92vw,360px)] -translate-x-1/2 -translate-y-1/2"
          }`}
        >
          <div className="flex items-center justify-between px-2.5 pt-2 pb-1.5">
            <div>
              <Dialog.Title className="text-sm font-semibold text-slate-900">
                {t("选择项目")}
              </Dialog.Title>
              <p className="mt-0.5 text-xs text-slate-400">
                {current?.name || t("还没有项目")}
              </p>
            </div>
            <Dialog.Close
              aria-label={t("关闭")}
              className="grid size-8 place-items-center rounded-lg text-slate-400 transition hover:bg-slate-100 hover:text-slate-700"
            >
              <X className="size-4" />
            </Dialog.Close>
          </div>

          <div className="max-h-[min(50dvh,360px)] overflow-y-auto py-1.5">
            {!projects.length && (
              <div className="px-2.5 py-6 text-center text-sm text-slate-400">
                {t("还没有项目")}
              </div>
            )}
            {projects.map((project) => {
              const selected = project.id === projectID;
              return (
                <Dialog.Close asChild key={project.id}>
                  <button
                    type="button"
                    onClick={() => onChange(project.id)}
                    className={`flex w-full items-center gap-2.5 rounded-xl px-2.5 py-2.5 text-left text-sm transition ${
                      selected
                        ? "bg-slate-100 text-slate-900"
                        : "text-slate-600 hover:bg-slate-50 hover:text-slate-900"
                    }`}
                  >
                    <span className="grid size-8 shrink-0 place-items-center rounded-lg bg-slate-100 text-slate-500">
                      <FolderKanban className="size-4" />
                    </span>
                    <span className="min-w-0 flex-1 truncate font-medium">
                      {project.name}
                    </span>
                    {selected && (
                      <Check className="size-4 shrink-0 text-sky-700" />
                    )}
                  </button>
                </Dialog.Close>
              );
            })}
          </div>

          {(onCreate || onManage) && (
            <>
              <div className="mx-1 h-px bg-slate-100" />
              <div className="space-y-0.5 py-1.5">
                {onCreate && (
                  <Dialog.Close asChild>
                    <button
                      type="button"
                      onClick={onCreate}
                      className="flex w-full items-center gap-2.5 rounded-xl px-2.5 py-2.5 text-left text-sm text-slate-600 transition hover:bg-slate-50 hover:text-slate-900"
                    >
                      <FolderPlus className="size-4 text-slate-400" />
                      {t("新建项目")}
                    </button>
                  </Dialog.Close>
                )}
                {onManage && (
                  <Dialog.Close asChild>
                    <button
                      type="button"
                      onClick={onManage}
                      className="flex w-full items-center gap-2.5 rounded-xl px-2.5 py-2.5 text-left text-sm text-slate-600 transition hover:bg-slate-50 hover:text-slate-900"
                    >
                      <Settings2 className="size-4 text-slate-400" />
                      {t("项目设置")}
                    </button>
                  </Dialog.Close>
                )}
              </div>
            </>
          )}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
