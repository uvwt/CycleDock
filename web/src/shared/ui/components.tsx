import { LoaderCircle, AlertCircle } from "lucide-react";
import type { ComponentProps, ReactNode } from "react";
import { cn } from "../lib/utils";
import { Button as UIButton } from "./button";
import { Label } from "./label";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "./dialog";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "./alert-dialog";
import {
  Sheet as UISheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
} from "./sheet";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "./dropdown-menu";
import {
  Tooltip as UITooltip,
  TooltipContent,
  TooltipTrigger,
} from "./tooltip";

export { Input as TextInput } from "./input";
export { Textarea as TextArea } from "./textarea";
export { NativeSelect as Select } from "./native-select";
export { Checkbox } from "./checkbox";
export { Badge } from "./badge";
export {
  Tooltip,
  TooltipTrigger,
  TooltipContent,
  TooltipProvider,
} from "./tooltip";

export function HintTooltip({
  content,
  children,
  side = "top",
}: {
  content: ReactNode;
  children: React.ReactElement;
  side?: "top" | "right" | "bottom" | "left";
}) {
  if (!content) return children;
  return (
    <UITooltip>
      <TooltipTrigger asChild>{children}</TooltipTrigger>
      <TooltipContent side={side}>{content}</TooltipContent>
    </UITooltip>
  );
}

// 页面保留现有业务命名，底层样式和交互统一由 shadcn/ui 组件负责。
const buttonVariants = {
  primary: "default",
  secondary: "outline",
  ghost: "ghost",
  danger: "destructive",
} as const;

export function Button({
  variant = "secondary",
  size = "sm",
  type = "button",
  className,
  ...props
}: Omit<ComponentProps<typeof UIButton>, "variant"> & {
  variant?: keyof typeof buttonVariants;
}) {
  return (
    <UIButton
      {...props}
      type={type}
      size={size}
      variant={buttonVariants[variant]}
      className={cn("text-[13px]", className)}
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
    <Label className="block space-y-1.5 font-normal leading-5 select-auto">
      <span className="block text-xs font-semibold text-secondary-foreground">
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
    </Label>
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
  onOpenChange: (open: boolean) => void;
  title: string;
  children: ReactNode;
  side?: "left" | "right";
}) {
  return (
    <UISheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side={side}
        aria-describedby={undefined}
        className="w-[min(94vw,440px)] gap-0 sm:max-w-none"
      >
        <SheetHeader className="min-h-17 shrink-0 justify-center border-b border-divider px-5 py-4 pr-12">
          <SheetTitle>{title}</SheetTitle>
        </SheetHeader>
        <div className="scrollbar-thin min-h-0 flex-1 overflow-y-auto p-5">
          {children}
        </div>
      </SheetContent>
    </UISheet>
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
    <DropdownMenu>
      <DropdownMenuTrigger asChild>{trigger}</DropdownMenuTrigger>
      <DropdownMenuContent
        align={align}
        side={side}
        sideOffset={6}
        collisionPadding={12}
      >
        {children}
      </DropdownMenuContent>
    </DropdownMenu>
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
    <DropdownMenuItem
      disabled={disabled}
      onSelect={onSelect}
      variant={danger ? "destructive" : "default"}
    >
      {children}
    </DropdownMenuItem>
  );
}

export const MenuSeparator = DropdownMenuSeparator;

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
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        {...(!description ? { "aria-describedby": undefined } : {})}
        className="max-h-[85dvh] overflow-y-auto sm:max-w-[520px]"
      >
        <DialogHeader className="pr-6 text-left">
          <DialogTitle>{title}</DialogTitle>
          {description && <DialogDescription>{description}</DialogDescription>}
        </DialogHeader>
        {children}
      </DialogContent>
    </Dialog>
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
    <AlertDialog
      open={open}
      onOpenChange={(next) => {
        if (!pending) onOpenChange(next);
      }}
    >
      <AlertDialogContent
        aria-busy={pending}
        onEscapeKeyDown={(event) => {
          if (pending) event.preventDefault();
        }}
      >
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription>{description}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel disabled={pending}>取消</AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            disabled={pending}
            onClick={(event) => {
              // 异步删除由调用方在成功后关闭；失败时保留确认框与错误提示。
              event.preventDefault();
              if (!pending) onConfirm();
            }}
          >
            {confirmLabel}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
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
