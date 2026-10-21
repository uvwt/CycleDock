import * as Dialog from "@radix-ui/react-dialog";
import { X, LoaderCircle, AlertCircle } from "lucide-react";
import type {
  ButtonHTMLAttributes,
  InputHTMLAttributes,
  ReactNode,
  TextareaHTMLAttributes,
} from "react";

const variants = {
  primary: "bg-sky-700 text-white hover:bg-sky-800 border-sky-700",
  secondary: "bg-white text-slate-700 hover:bg-slate-50 border-slate-200",
  ghost: "bg-transparent text-slate-600 hover:bg-slate-100 border-transparent",
  danger: "bg-rose-50 text-rose-700 hover:bg-rose-100 border-rose-200",
};
export function Button({
  variant = "secondary",
  className = "",
  children,
  ...props
}: ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: keyof typeof variants;
}) {
  return (
    <button
      {...props}
      className={`inline-flex min-h-9 items-center justify-center gap-2 rounded-xl border px-3 text-sm font-medium transition-colors disabled:opacity-40 ${variants[variant]} ${className}`}
    >
      {children}
    </button>
  );
}
export function TextInput(props: InputHTMLAttributes<HTMLInputElement>) {
  return (
    <input
      {...props}
      className={`w-full rounded-xl border border-slate-200 bg-white px-3 py-2.5 text-sm outline-none focus:border-sky-500 ${props.className ?? ""}`}
    />
  );
}
export function TextArea(props: TextareaHTMLAttributes<HTMLTextAreaElement>) {
  return (
    <textarea
      {...props}
      className={`w-full resize-y rounded-xl border border-slate-200 bg-white px-3 py-2.5 text-sm outline-none focus:border-sky-500 ${props.className ?? ""}`}
    />
  );
}
export function Field({
  label,
  children,
  hint,
}: {
  label: string;
  children: ReactNode;
  hint?: string;
}) {
  return (
    <label className="block space-y-1.5">
      <span className="block text-xs font-semibold text-slate-600">
        {label}
      </span>
      {children}
      {hint && <span className="block text-xs text-slate-500">{hint}</span>}
    </label>
  );
}
export function Spinner({ text = "加载中…" }: { text?: string }) {
  return (
    <span
      role="status"
      className="inline-flex items-center gap-2 text-sm text-slate-500"
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
      className="rounded-2xl border border-rose-200 bg-rose-50 p-5 text-sm text-rose-700"
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
          className={`fixed inset-y-0 z-50 flex w-[min(94vw,440px)] flex-col bg-white shadow-2xl ${side === "right" ? "right-0" : "left-0"}`}
        >
          <header className="flex min-h-17 items-center justify-between border-b border-slate-100 px-5 py-4">
            <Dialog.Title className="text-base font-semibold">
              {title}
            </Dialog.Title>
            <Dialog.Close
              className="rounded-xl p-2 hover:bg-slate-100"
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
    <section className="rounded-2xl border border-slate-200 bg-white p-5">
      <div className="mb-5">
        <h2 className="text-base font-semibold text-slate-900">{title}</h2>
        {description && (
          <p className="mt-1 text-sm text-slate-500">{description}</p>
        )}
      </div>
      {children}
    </section>
  );
}
