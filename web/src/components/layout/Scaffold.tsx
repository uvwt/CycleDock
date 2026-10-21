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
      className={cn("v2-sidebar product-sidebar", className)}
      aria-label={t("主导航")}
    >
      <div className="product-brand">
        <img src="/cycledock-logo.png" alt="" />
        <span className="product-brand-name">CycleDock</span>
      </div>
      <nav className="product-mode-nav" aria-label={t("工作模式")}>
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
      <div className="product-sidebar-content">{children}</div>
      <div className="product-sidebar-footer space-y-1">
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
    <header className="product-page-header">
      <div className="flex min-w-0 items-center gap-2">
        {leading}
        <div className="min-w-0">
          <Typography
            as="h1"
            scale="title"
            className="product-page-header-title break-words"
          >
            {title}
          </Typography>
          {description && (
            <Typography scale="meta" className="product-page-header-description">
              {description}
            </Typography>
          )}
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
