import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import {
  MessageSquareText,
  PanelsTopLeft,
  Settings2,
  ChevronDown,
} from "lucide-react";
import { Button, IconButton, Typography, Avatar } from "../ui/controls";
import { cn } from "../ui/utils";

export function Sidebar({
  children,
  project,
  account,
  mode = "discussions",
  onModeChange,
  onProjectClick,
  onAccountClick,
  projectControl,
  accountControl,
  className,
}: {
  children: ReactNode;
  project: string;
  account: string;
  mode?: "discussions" | "work";
  onModeChange?: (mode: "discussions" | "work") => void;
  onProjectClick?: () => void;
  onAccountClick?: () => void;
  projectControl?: ReactNode;
  accountControl?: ReactNode;
  className?: string;
}) {
  const { t } = useTranslation();
  return (
    <aside
      className={cn("v2-sidebar flex h-full flex-col px-3 py-4", className)}
      aria-label={t("主导航")}
    >
      <div className="flex items-center gap-2 px-2 pb-5">
        <img
          src="/cycledock-logo.png"
          alt=""
          className="size-7 object-contain"
        />
        <span className="text-[var(--v2-type-section)] font-semibold">
          CycleDock
        </span>
      </div>
      <nav
        className="mb-4 grid grid-cols-2 gap-1 rounded-[var(--v2-radius)] bg-[var(--v2-subtle)] p-1"
        aria-label={t("工作模式")}
      >
        <Button
          variant={mode === "discussions" ? "secondary" : "ghost"}
          size="compact"
          onClick={() => onModeChange?.("discussions")}
          aria-current={mode === "discussions" ? "page" : undefined}
        >
          <MessageSquareText size={14} />
          {t("讨论")}
        </Button>
        <Button
          variant={mode === "work" ? "secondary" : "ghost"}
          size="compact"
          onClick={() => onModeChange?.("work")}
          aria-current={mode === "work" ? "page" : undefined}
        >
          <PanelsTopLeft size={14} />
          {t("工作")}
        </Button>
      </nav>
      <div className="min-h-0 flex-1 overflow-y-auto">{children}</div>
      <div className="mt-3 space-y-1 border-t border-[var(--v2-border)] pt-3">
        {projectControl ?? (
          <Button
            className="w-full justify-between overflow-hidden"
            variant="ghost"
            onClick={onProjectClick}
          >
            <span className="truncate text-left">{project}</span>
            <ChevronDown size={14} />
          </Button>
        )}
        {accountControl ?? (
          <Button
            className="w-full justify-start"
            variant="ghost"
            onClick={onAccountClick}
          >
            <Avatar
              fallback={account.slice(0, 1).toUpperCase()}
              className="size-6"
            />
            <span className="min-w-0 flex-1 truncate text-left">{account}</span>
            <Settings2 size={14} />
          </Button>
        )}
      </div>
    </aside>
  );
}

export function PageHeader({
  title,
  description,
  leading,
  actions,
}: {
  title: string;
  description?: string;
  leading?: ReactNode;
  actions?: ReactNode;
}) {
  return (
    <header className="flex min-w-0 flex-wrap items-center justify-between gap-3 border-b border-[var(--v2-border)] pb-4">
      <div className="flex min-w-0 items-center gap-2">
        {leading}
        <div className="min-w-0">
          <Typography as="h1" scale="title" className="break-words">
            {title}
          </Typography>
          {description && <Typography scale="meta">{description}</Typography>}
        </div>
      </div>
      {actions && (
        <div className="flex shrink-0 items-center gap-2">{actions}</div>
      )}
    </header>
  );
}

export function MobileNavButton({ onClick }: { onClick: () => void }) {
  const { t } = useTranslation();
  return (
    <IconButton
      icon={PanelsTopLeft}
      label={t("打开导航")}
      variant="ghost"
      onClick={onClick}
    />
  );
}
