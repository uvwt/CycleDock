import * as Dialog from "@radix-ui/react-dialog";
import * as DropdownMenu from "@radix-ui/react-dropdown-menu";
import { X, LoaderCircle, AlertCircle } from "lucide-react";
import type {
  ButtonHTMLAttributes,
  SelectHTMLAttributes,
  InputHTMLAttributes,
  ReactNode,
  TextareaHTMLAttributes,
} from "react";

const variants = {
  primary: "bg-sky-700 text-white hover:bg-sky-800 border-sky-700",
  secondary: "bg-surface text-foreground hover:bg-canvas border-line",
  ghost: "bg-transparent text-secondary hover:bg-muted border-transparent",
  danger:
    "bg-danger-muted text-danger hover:bg-danger-hover border-danger-line",
};
export function Button({
  variant = "secondary",
  className = "",
  children,
  type = "button",
  ...props
}: ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: keyof typeof variants;
}) {
  return (
    <button
      {...props}
      type={type}
      className={`inline-flex min-h-8.5 items-center justify-center gap-2 rounded-lg border px-3 text-[13px] font-medium transition-colors disabled:opacity-40 ${variants[variant]} ${className}`}
    >
      {children}
    </button>
  );
}
const controlClass =
  "w-full rounded-lg border border-line bg-surface px-3 py-2 text-[13px] leading-5 outline-none focus:border-focus disabled:opacity-40 aria-invalid:border-danger";

export function Select(props: SelectHTMLAttributes<HTMLSelectElement>) {
  return (
    <select {...props} className={`${controlClass} ${props.className ?? ""}`} />
  );
}

export function TextInput(props: InputHTMLAttributes<HTMLInputElement>) {
  return (
    <input {...props} className={`${controlClass} ${props.className ?? ""}`} />
  );
}
export function TextArea(props: TextareaHTMLAttributes<HTMLTextAreaElement>) {
  return (
    <textarea
      {...props}
      className={`${controlClass} resize-y ${props.className ?? ""}`}
    />
  );
}
export function Field({
  label,
  children,
  hint,
  error,
}: {
  label: string;
  children: ReactNode;
  hint?: string;
  error?: string;
}) {
  return (
    <label className="block space-y-1.5">
      <span className="block text-xs font-semibold text-secondary">
        {label}
      </span>
      {children}
      {error && (
        <span role="alert" className="block text-xs text-danger">
          {error}
        </span>
      )}
      {hint && (
        <span className="block text-xs text-muted-foreground">{hint}</span>
      )}
    </label>
  );
}
export function Spinner({ text = "加载中…" }: { text?: string }) {
  return (
    <span
      role="status"
      className="inline-flex items-center gap-2 text-sm text-muted-foreground"
    >
      <LoaderCircle className="size-4 animate-spin" />
      {text}
    </span>
  );
}
export function ErrorView({
  error,
  onRetry,
}: {
  error: unknown;
  onRetry?: () => void;
}) {
  return (
    <div
      role="alert"
      className="rounded-xl border border-danger-line bg-danger-muted p-4 text-sm text-danger"
    >
      <AlertCircle className="mb-2 size-5" />
      {error instanceof Error ? error.message : "操作未成功，请稍后重试"}
      {onRetry && (
        <div className="mt-3">
          <Button onClick={onRetry}>重试</Button>
        </div>
      )}
    </div>
  );
}
export function Sheet({
  open,
  onOpenChange,
  title,
  children,
  side = "right",
}: {
  open: boolean;
  onOpenChange: (x: boolean) => void;
  title: string;
  children: ReactNode;
  side?: "left" | "right";
}) {
  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-40 bg-slate-950/30" />
        <Dialog.Content
          aria-describedby={undefined}
          className={`fixed inset-y-0 z-50 flex w-[min(94vw,440px)] flex-col bg-surface shadow-xl ${side === "right" ? "right-0" : "left-0"}`}
        >
          <header className="flex min-h-17 items-center justify-between border-b border-divider px-5 py-4">
            <Dialog.Title className="text-base font-semibold">
              {title}
            </Dialog.Title>
            <Dialog.Close
              className="rounded-lg p-2 hover:bg-muted"
              aria-label="关闭"
            >
              <X className="size-5" />
            </Dialog.Close>
          </header>
          <div className="scrollbar-thin min-h-0 flex-1 overflow-y-auto p-5">
            {children}
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
export function Menu({
  trigger,
  children,
  align = "end",
  side = "bottom",
}: {
  trigger: ReactNode;
  children: ReactNode;
  align?: "start" | "center" | "end";
  side?: "top" | "right" | "bottom" | "left";
}) {
  return (
    <DropdownMenu.Root>
      <DropdownMenu.Trigger asChild>{trigger}</DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content
          align={align}
          side={side}
          sideOffset={6}
          collisionPadding={12}
          className="z-50 min-w-44 rounded-xl border border-line bg-surface p-1.5 shadow-lg outline-none"
        >
          {children}
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  );
}
export function MenuItem({
  children,
  onSelect,
  disabled = false,
  danger = false,
}: {
  children: ReactNode;
  onSelect?: () => void;
  disabled?: boolean;
  danger?: boolean;
}) {
  return (
    <DropdownMenu.Item
      disabled={disabled}
      onSelect={onSelect}
      className={`flex cursor-default select-none items-center gap-2 rounded-lg px-2.5 py-2 text-[13px] outline-none data-[disabled]:pointer-events-none data-[disabled]:opacity-40 ${
        danger
          ? "text-danger focus:bg-danger-muted"
          : "text-foreground focus:bg-muted"
      }`}
    >
      {children}
    </DropdownMenu.Item>
  );
}
export function MenuSeparator() {
  return <DropdownMenu.Separator className="my-1 h-px bg-muted" />;
}
export function Modal({
  open,
  onOpenChange,
  title,
  description,
  children,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description?: string;
  children: ReactNode;
}) {
  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-50 bg-slate-950/30" />
        <Dialog.Content className="fixed top-1/2 left-1/2 z-50 w-[min(92vw,520px)] -translate-x-1/2 -translate-y-1/2 rounded-2xl border border-line bg-surface p-5 shadow-xl outline-none sm:p-6">
          <div className="flex items-start justify-between gap-4">
            <div className="min-w-0">
              <Dialog.Title className="text-base font-semibold text-heading">
                {title}
              </Dialog.Title>
              {description && (
                <Dialog.Description className="mt-1 text-[13px] leading-5 text-muted-foreground">
                  {description}
                </Dialog.Description>
              )}
            </div>
            <Dialog.Close
              aria-label="关闭"
              className="-mt-1 -mr-1 grid size-8 shrink-0 place-items-center rounded-lg text-faint hover:bg-muted hover:text-foreground"
            >
              <X className="size-4" />
            </Dialog.Close>
          </div>
          <div className="mt-5">{children}</div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
export function ConfirmDialog({
  open,
  onOpenChange,
  title,
  description,
  confirmLabel,
  pending = false,
  onConfirm,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description: string;
  confirmLabel: string;
  pending?: boolean;
  onConfirm: () => void;
}) {
  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      title={title}
      description={description}
    >
      <div className="flex justify-end gap-2">
        <Button disabled={pending} onClick={() => onOpenChange(false)}>
          取消
        </Button>
        <Button variant="danger" disabled={pending} onClick={onConfirm}>
          {confirmLabel}
        </Button>
      </div>
    </Modal>
  );
}
export function Section({
  title,
  description,
  children,
}: {
  title: string;
  description?: string;
  children: ReactNode;
}) {
  return (
    <section className="rounded-xl border border-line bg-surface p-4 sm:p-5">
      <div className="mb-5">
        <h2 className="text-[15px] font-semibold text-heading">{title}</h2>
        {description && (
          <p className="mt-1 text-[13px] leading-5 text-muted-foreground">
            {description}
          </p>
        )}
      </div>
      {children}
    </section>
  );
}
