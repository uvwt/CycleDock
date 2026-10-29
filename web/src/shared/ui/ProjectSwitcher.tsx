import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
} from "./dropdown-menu";
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
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          title={current?.name || t("选择项目")}
          aria-label={compact ? t("选择项目") : undefined}
          disabled={!projects.length && !onCreate}
          className={
            compact
              ? "grid size-10 place-items-center rounded-xl text-faint transition hover:bg-canvas hover:text-strong data-[state=open]:bg-muted data-[state=open]:text-heading disabled:opacity-40"
              : "flex min-h-10 w-full items-center justify-between gap-2.5 rounded-xl border border-line bg-surface px-3 text-left text-sm text-foreground shadow-xs transition hover:border-line-strong hover:bg-canvas data-[state=open]:border-line-strong disabled:opacity-40"
          }
        >
          {compact ? (
            <FolderKanban className="size-5" />
          ) : (
            <>
              <span className="flex min-w-0 items-center gap-2 truncate font-medium">
                <FolderKanban className="size-4 shrink-0 text-faint" />
                <span className="truncate">
                  {current?.name || t("选择项目")}
                </span>
              </span>
              <ChevronDown className="size-4 shrink-0 text-faint" />
            </>
          )}
        </button>
      </DropdownMenuTrigger>

      <DropdownMenuContent
        side={compact ? "right" : "bottom"}
        align={compact ? "end" : "start"}
        sideOffset={8}
        collisionPadding={12}
        className="w-64 p-1.5 shadow-lg rounded-xl sm:w-72"
      >
        <DropdownMenuLabel className="px-2.5 py-1 text-xs font-semibold text-muted-foreground">
          {t("选择项目")}
        </DropdownMenuLabel>
        <DropdownMenuSeparator className="my-1" />

        <DropdownMenuGroup className="max-h-64 overflow-y-auto py-0.5">
          {!projects.length ? (
            <div className="px-3 py-4 text-center text-xs text-muted-foreground">
              {t("还没有项目")}
            </div>
          ) : (
            projects.map((project) => {
              const selected = project.id === projectID;
              return (
                <DropdownMenuItem
                  key={project.id}
                  onSelect={() => onChange(project.id)}
                  className={`flex cursor-pointer items-center justify-between rounded-lg px-2.5 py-2 text-sm transition-colors ${
                    selected
                      ? "bg-muted font-medium text-heading"
                      : "text-secondary-foreground"
                  }`}
                >
                  <span className="flex min-w-0 items-center gap-2.5">
                    <FolderKanban
                      className={`size-4 shrink-0 ${
                        selected
                          ? "text-sky-700 dark:text-sky-400"
                          : "text-faint"
                      }`}
                    />
                    <span className="truncate">{project.name}</span>
                  </span>
                  {selected && (
                    <Check className="size-4 shrink-0 text-sky-700 dark:text-sky-400" />
                  )}
                </DropdownMenuItem>
              );
            })
          )}
        </DropdownMenuGroup>

        {(onCreate || onManage) && (
          <>
            <DropdownMenuSeparator className="my-1" />
            <DropdownMenuGroup className="space-y-0.5 pt-0.5">
              {onCreate && (
                <DropdownMenuItem
                  onSelect={onCreate}
                  className="flex cursor-pointer items-center gap-2.5 rounded-lg px-2.5 py-2 text-sm text-secondary-foreground"
                >
                  <FolderPlus className="size-4 shrink-0 text-faint" />
                  <span>{t("新建项目")}</span>
                </DropdownMenuItem>
              )}
              {onManage && (
                <DropdownMenuItem
                  onSelect={onManage}
                  className="flex cursor-pointer items-center gap-2.5 rounded-lg px-2.5 py-2 text-sm text-secondary-foreground"
                >
                  <Settings2 className="size-4 shrink-0 text-faint" />
                  <span>{t("项目设置")}</span>
                </DropdownMenuItem>
              )}
            </DropdownMenuGroup>
          </>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
