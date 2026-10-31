import { useEffect, useState } from "react";
import { ListChecks, X } from "lucide-react";
import { useTranslation } from "react-i18next";
import type { TaskProgress } from "../../shared/realtime/cardStreamState";

const statusLabels: Record<string, string> = {
  active: "进行中",
  blocked: "已阻塞",
  completed: "已完成",
  pending: "未开始",
  in_progress: "进行中",
  pass: "已通过",
  failed: "未通过",
};

function statusLabel(status: string | undefined, t: (value: string) => string) {
  if (!status) return "";
  return t(statusLabels[status] || status);
}

export function TaskProgressCard({ task }: { task?: TaskProgress }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [closedID, setClosedID] = useState("");
  useEffect(() => {
    if (task?.id && task.id !== closedID) setOpen(true);
  }, [task?.id, closedID]);

  if (!task?.id) return null;
  const total = task.step_count || 0;
  const completed = Math.min(task.completed_step_count || 0, total || task.completed_step_count || 0);
  const ratio = total > 0 ? Math.round((completed / total) * 100) : 0;
  function close() {
    setClosedID(task?.id || "");
    setOpen(false);
  }

  return (
    <div className="relative">
      <button
        type="button"
        aria-expanded={open}
        aria-label={t("任务进度")}
        title={t("任务进度")}
        onClick={() => (open ? close() : setOpen(true))}
        className="flex h-8 shrink-0 items-center gap-1.5 rounded-lg border border-line bg-surface/90 px-2.5 text-xs text-muted-foreground shadow-sm backdrop-blur transition hover:bg-muted"
      >
        <ListChecks className="size-3.5" />
        <span className="hidden sm:inline">{t("任务进度")}</span>
        {total > 0 && (
          <span className="tabular-nums">
            {completed}/{total}
          </span>
        )}
      </button>
      {open && (
        <section
          aria-label={t("任务进度")}
          className="absolute top-full right-0 z-20 mt-2 w-80 max-w-[calc(100vw-2rem)] rounded-2xl border border-line bg-surface p-3 text-left shadow-lg"
        >
          <div className="flex items-start gap-2">
            <div className="min-w-0 flex-1">
              <p className="truncate text-sm font-medium text-foreground">
                {task.title}
              </p>
              <p className="mt-0.5 text-[11px] text-faint">
                {[statusLabel(task.status, t), total > 0 ? `${completed}/${total}` : ""]
                  .filter(Boolean)
                  .join(" · ")}
              </p>
            </div>
            <button
              type="button"
              aria-label={t("关闭任务进度")}
              title={t("关闭任务进度")}
              onClick={close}
              className="grid size-7 shrink-0 place-items-center rounded-lg text-faint hover:bg-muted"
            >
              <X className="size-3.5" />
            </button>
          </div>
          {total > 0 && (
            <div className="mt-3 h-1.5 overflow-hidden rounded-full bg-muted">
              <div
                className="h-full rounded-full bg-sky-600"
                style={{ width: `${ratio}%` }}
              />
            </div>
          )}
          {task.current_step && (
            <p className="mt-3 text-xs text-secondary-foreground">
              <span className="text-faint">{t("当前步骤")}</span> {task.current_step}
            </p>
          )}
          {task.blocker && (
            <p className="mt-2 text-xs leading-5 text-warning">{task.blocker}</p>
          )}
          {task.summary && (
            <p className="mt-2 text-xs leading-5 text-muted-foreground">
              {task.summary}
            </p>
          )}
          {!!task.steps?.length && (
            <ul className="mt-3 max-h-48 space-y-1.5 overflow-y-auto">
              {task.steps.map((step, index) => (
                <li
                  key={`${index}:${step.title}`}
                  className="flex items-center gap-2 text-xs text-secondary-foreground"
                >
                  <span
                    aria-hidden
                    className={`size-1.5 shrink-0 rounded-full ${
                      step.status === "completed"
                        ? "bg-emerald-500"
                        : step.status === "in_progress"
                          ? "bg-sky-500"
                          : "bg-placeholder"
                    }`}
                  />
                  <span className="min-w-0 flex-1 truncate">{step.title}</span>
                  {step.status && (
                    <span className="shrink-0 text-[11px] text-faint">
                      {statusLabel(step.status, t)}
                    </span>
                  )}
                </li>
              ))}
            </ul>
          )}
        </section>
      )}
    </div>
  );
}
