import { Check } from "lucide-react";
import { useTranslation } from "react-i18next";
import { getOngoingHealthInfo } from "../lib/ongoing-health";

export function OngoingHealthBadge({
  health,
  hideWhenUnknown = false,
  size = "sm",
  variant = "pill",
  className = "",
}: {
  health?: string;
  hideWhenUnknown?: boolean;
  size?: "xs" | "sm";
  variant?: "pill" | "inline" | "dot";
  className?: string;
}) {
  const { t } = useTranslation();
  const info = getOngoingHealthInfo(health);
  if (hideWhenUnknown && info.kind === "unknown") {
    return null;
  }

  if (variant === "dot") {
    return (
      <span
        data-health-status={info.kind}
        title={t(info.labelKey)}
        aria-label={t(info.labelKey)}
        className={`inline-block size-2 shrink-0 rounded-full ${info.dotClassName} ${className}`}
      />
    );
  }

  if (variant === "inline") {
    const inlineTextClass =
      info.kind === "warning" || info.kind === "critical"
        ? info.textClassName
        : "text-muted-foreground";
    return (
      <span
        data-health-status={info.kind}
        className={`inline-flex items-center gap-1 leading-none ${inlineTextClass} ${className}`}
      >
        {info.kind === "acknowledged" ? (
          <Check aria-hidden className="size-3 shrink-0" />
        ) : (
          <span
            aria-hidden
            className={`size-1.5 shrink-0 rounded-full ${info.dotClassName}`}
          />
        )}
        <span>{t(info.labelKey)}</span>
      </span>
    );
  }

  const sizeClass =
    size === "xs"
      ? "px-1.5 py-0.5 text-[10px] gap-1"
      : "px-2 py-0.5 text-[11px] gap-1.5";

  return (
    <span
      data-health-status={info.kind}
      className={`inline-flex items-center rounded-full border font-medium leading-none ${sizeClass} ${info.badgeClassName} ${className}`}
    >
      {info.kind === "acknowledged" ? (
        <Check
          aria-hidden
          className={size === "xs" ? "size-2.5 shrink-0" : "size-3 shrink-0"}
        />
      ) : (
        <span
          aria-hidden
          className={`size-1.5 shrink-0 rounded-full ${info.dotClassName}`}
        />
      )}
      <span>{t(info.labelKey)}</span>
    </span>
  );
}
