import { useTranslation } from "react-i18next";

export function RunningDots() {
  const { t } = useTranslation();
  return (
    <span
      role="status"
      aria-label={t("正在执行")}
      className="inline-flex h-4 items-center gap-1 align-middle"
    >
      <span aria-hidden className="live-dot size-1 rounded-full bg-current" />
      <span aria-hidden className="live-dot size-1 rounded-full bg-current" />
      <span aria-hidden className="live-dot size-1 rounded-full bg-current" />
    </span>
  );
}
