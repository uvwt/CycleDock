import {
  Dialog,
  DialogTrigger,
  DialogContent,
  DialogTitle,
  DialogClose,
} from "./dialog";
import {
  Check,
  ChevronDown,
  FolderKanban,
  FolderPlus,
  Settings2,
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
    <Dialog>
      <DialogTrigger asChild>
        <button
          type="button"
          title={current?.name || t("选择项目")}
          aria-label={compact ? t("选择项目") : undefined}
          disabled={!projects.length && !onCreate}
          className={
            compact
              ? "grid size-10 place-items-center rounded-xl text-faint transition hover:bg-canvas hover:text-strong disabled:opacity-40"
              : "flex min-h-10 w-full items-center gap-2.5 rounded-xl border border-line bg-surface px-3 text-left text-sm text-foreground shadow-sm transition hover:border-line-strong hover:bg-canvas disabled:opacity-40"
          }
        >
          <FolderKanban className={compact ? "size-5" : "size-4 text-faint"} />
          {!compact && (
            <>
              <span className="min-w-0 flex-1 truncate font-medium">
                {current?.name || t("选择项目")}
              </span>
              <ChevronDown className="size-4 shrink-0 text-faint" />
            </>
          )}
        </button>
      </DialogTrigger>

      <DialogContent
        aria-describedby={undefined}
        closeLabel={t("关闭")}
        overlayClassName={`z-[60] ${compact ? "bg-slate-950/15 backdrop-blur-[1px] md:bg-transparent md:backdrop-blur-none" : "bg-slate-950/20 backdrop-blur-[1px]"}`}
        className={`fixed z-[70] gap-0 rounded-2xl sm:max-w-none border border-line bg-surface p-2 shadow-[0_24px_80px_rgba(15,23,42,0.22)] outline-none ${
          compact
            ? "top-1/2 left-1/2 w-[min(92vw,360px)] -translate-x-1/2 -translate-y-1/2 md:top-auto md:bottom-3 md:left-[4.5rem] md:w-[280px] md:translate-x-0 md:translate-y-0"
            : "top-1/2 left-1/2 w-[min(92vw,360px)] -translate-x-1/2 -translate-y-1/2"
        }`}
      >
        <div className="flex items-center justify-between px-2.5 pt-2 pr-8 pb-1.5">
          <div>
            <DialogTitle className="text-sm font-semibold text-heading">
              {t("选择项目")}
            </DialogTitle>
            <p className="mt-0.5 text-xs text-faint">
              {current?.name || t("还没有项目")}
            </p>
          </div>
        </div>

        <div className="max-h-[min(50dvh,360px)] overflow-y-auto py-1.5">
          {!projects.length && (
            <div className="px-2.5 py-6 text-center text-sm text-faint">
              {t("还没有项目")}
            </div>
          )}
          {projects.map((project) => {
            const selected = project.id === projectID;
            return (
              <DialogClose asChild key={project.id}>
                <button
                  type="button"
                  onClick={() => onChange(project.id)}
                  className={`flex w-full items-center gap-2.5 rounded-xl px-2.5 py-2.5 text-left text-sm transition ${
                    selected
                      ? "bg-muted text-heading"
                      : "text-secondary-foreground hover:bg-canvas hover:text-heading"
                  }`}
                >
                  <span className="grid size-8 shrink-0 place-items-center rounded-lg bg-muted text-muted-foreground">
                    <FolderKanban className="size-4" />
                  </span>
                  <span className="min-w-0 flex-1 truncate font-medium">
                    {project.name}
                  </span>
                  {selected && (
                    <Check className="size-4 shrink-0 text-sky-700" />
                  )}
                </button>
              </DialogClose>
            );
          })}
        </div>

        {(onCreate || onManage) && (
          <>
            <div className="mx-1 h-px bg-muted" />
            <div className="space-y-0.5 py-1.5">
              {onCreate && (
                <DialogClose asChild>
                  <button
                    type="button"
                    onClick={onCreate}
                    className="flex w-full items-center gap-2.5 rounded-xl px-2.5 py-2.5 text-left text-sm text-secondary-foreground transition hover:bg-canvas hover:text-heading"
                  >
                    <FolderPlus className="size-4 text-faint" />
                    {t("新建项目")}
                  </button>
                </DialogClose>
              )}
              {onManage && (
                <DialogClose asChild>
                  <button
                    type="button"
                    onClick={onManage}
                    className="flex w-full items-center gap-2.5 rounded-xl px-2.5 py-2.5 text-left text-sm text-secondary-foreground transition hover:bg-canvas hover:text-heading"
                  >
                    <Settings2 className="size-4 text-faint" />
                    {t("项目设置")}
                  </button>
                </DialogClose>
              )}
            </div>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}
