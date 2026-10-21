import type { ComponentProps } from "react";
import * as CheckboxPrimitive from "@radix-ui/react-checkbox";
import * as RadioGroupPrimitive from "@radix-ui/react-radio-group";
import * as ScrollAreaPrimitive from "@radix-ui/react-scroll-area";
import * as AlertDialogPrimitive from "@radix-ui/react-alert-dialog";
import { Check, Circle } from "lucide-react";
import { cn } from "./utils";
import { Button } from "./controls";

export function Checkbox({
  className,
  ...props
}: ComponentProps<typeof CheckboxPrimitive.Root>) {
  return (
    <CheckboxPrimitive.Root
      {...props}
      className={cn(
        "flex size-[18px] shrink-0 items-center justify-center rounded-[var(--v2-radius-sm)] border border-[var(--v2-border)] bg-[var(--v2-surface)] text-white data-[state=checked]:border-[var(--v2-accent)] data-[state=checked]:bg-[var(--v2-accent)] disabled:opacity-50",
        className,
      )}
    >
      <CheckboxPrimitive.Indicator>
        <Check size={14} aria-hidden="true" />
      </CheckboxPrimitive.Indicator>
    </CheckboxPrimitive.Root>
  );
}
export const RadioGroup = RadioGroupPrimitive.Root;
export function RadioGroupItem({
  className,
  ...props
}: ComponentProps<typeof RadioGroupPrimitive.Item>) {
  return (
    <RadioGroupPrimitive.Item
      {...props}
      className={cn(
        "inline-flex size-[18px] shrink-0 items-center justify-center rounded-full border border-[var(--v2-border)] bg-[var(--v2-surface)] text-[var(--v2-accent)] data-[state=checked]:border-[var(--v2-accent)] disabled:opacity-50",
        className,
      )}
    >
      <RadioGroupPrimitive.Indicator>
        <Circle size={9} fill="currentColor" strokeWidth={0} />
      </RadioGroupPrimitive.Indicator>
    </RadioGroupPrimitive.Item>
  );
}
export function ScrollArea({
  children,
  className,
  ...props
}: ComponentProps<typeof ScrollAreaPrimitive.Root>) {
  return (
    <ScrollAreaPrimitive.Root
      {...props}
      className={cn("relative overflow-hidden", className)}
    >
      <ScrollAreaPrimitive.Viewport className="h-full w-full rounded-[inherit]">
        {children}
      </ScrollAreaPrimitive.Viewport>
      <ScrollAreaPrimitive.Scrollbar
        orientation="vertical"
        className="flex w-2 touch-none select-none p-[2px]"
      >
        <ScrollAreaPrimitive.Thumb className="relative flex-1 rounded-full bg-[var(--v2-border)]" />
      </ScrollAreaPrimitive.Scrollbar>
      <ScrollAreaPrimitive.Corner />
    </ScrollAreaPrimitive.Root>
  );
}
export const AlertDialog = AlertDialogPrimitive.Root;
export const AlertDialogTrigger = AlertDialogPrimitive.Trigger;
export function AlertDialogContent({
  title,
  description,
  confirmLabel = "确认",
  cancelLabel = "取消",
  onConfirm,
  danger = false,
  disabled = false,
}: {
  title: string;
  description: string;
  confirmLabel?: string;
  cancelLabel?: string;
  onConfirm: () => void;
  danger?: boolean;
  disabled?: boolean;
}) {
  return (
    <AlertDialogPrimitive.Portal>
      <AlertDialogPrimitive.Overlay className="v2 v2-overlay-backdrop" />
      <AlertDialogPrimitive.Content className="v2 v2-overlay v2-overlay-panel v2-dialog">
        <AlertDialogPrimitive.Title className="v2-text-section">
          {title}
        </AlertDialogPrimitive.Title>
        <AlertDialogPrimitive.Description className="v2-text-control mt-2 text-[var(--v2-muted)]">
          {description}
        </AlertDialogPrimitive.Description>
        <div className="mt-6 flex justify-end gap-2">
          <AlertDialogPrimitive.Cancel asChild>
            <Button type="button">{cancelLabel}</Button>
          </AlertDialogPrimitive.Cancel>
          <AlertDialogPrimitive.Action asChild>
            <Button
              type="button"
              variant={danger ? "danger" : "primary"}
              disabled={disabled}
              onClick={onConfirm}
            >
              {confirmLabel}
            </Button>
          </AlertDialogPrimitive.Action>
        </div>
      </AlertDialogPrimitive.Content>
    </AlertDialogPrimitive.Portal>
  );
}
