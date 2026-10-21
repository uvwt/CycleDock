import { type ComponentProps, type ReactNode } from "react";
import * as DialogPrimitive from "@radix-ui/react-dialog";
import * as PopoverPrimitive from "@radix-ui/react-popover";
import * as DropdownPrimitive from "@radix-ui/react-dropdown-menu";
import * as SelectPrimitive from "@radix-ui/react-select";
import * as TooltipPrimitive from "@radix-ui/react-tooltip";
import * as TabsPrimitive from "@radix-ui/react-tabs";
import { Check, ChevronDown, ChevronRight, X } from "lucide-react";
import { cn } from "./utils";
import { IconButton } from "./controls";

export const Dialog = DialogPrimitive.Root;
export const DialogTrigger = DialogPrimitive.Trigger;
export const DialogClose = DialogPrimitive.Close;
export const DialogTitle = DialogPrimitive.Title;
export const DialogDescription = DialogPrimitive.Description;
export function DialogContent({
  children,
  className,
  ...props
}: ComponentProps<typeof DialogPrimitive.Content>) {
  return (
    <DialogPrimitive.Portal>
      <DialogPrimitive.Overlay className="v2 v2-overlay-backdrop" />
      <DialogPrimitive.Content
        {...props}
        className={cn("v2 v2-overlay v2-overlay-panel v2-dialog", className)}
      >
        {children}
        <DialogPrimitive.Close
          aria-label="关闭"
          className="absolute top-3 right-3 rounded-[var(--v2-radius-sm)] p-1 hover:bg-[var(--v2-subtle)]"
        >
          <X size={16} />
        </DialogPrimitive.Close>
      </DialogPrimitive.Content>
    </DialogPrimitive.Portal>
  );
}

export const Sheet = DialogPrimitive.Root;
export const SheetTrigger = DialogPrimitive.Trigger;
export const SheetTitle = DialogPrimitive.Title;
export const SheetDescription = DialogPrimitive.Description;
export function SheetContent({
  children,
  className,
  ...props
}: ComponentProps<typeof DialogPrimitive.Content>) {
  return (
    <DialogPrimitive.Portal>
      <DialogPrimitive.Overlay className="v2 v2-overlay-backdrop" />
      <DialogPrimitive.Content
        {...props}
        className={cn("v2 v2-overlay v2-overlay-panel v2-sheet", className)}
      >
        {children}
        <DialogPrimitive.Close
          aria-label="关闭"
          className="absolute top-3 right-3 rounded-[var(--v2-radius-sm)] p-1 hover:bg-[var(--v2-subtle)]"
        >
          <X size={16} />
        </DialogPrimitive.Close>
      </DialogPrimitive.Content>
    </DialogPrimitive.Portal>
  );
}

export const Popover = PopoverPrimitive.Root;
export const PopoverTrigger = PopoverPrimitive.Trigger;
export function PopoverContent({
  children,
  sideOffset = 6,
  align = "start",
  className,
  ...props
}: ComponentProps<typeof PopoverPrimitive.Content>) {
  return (
    <PopoverPrimitive.Portal>
      <PopoverPrimitive.Content
        {...props}
        sideOffset={sideOffset}
        align={align}
        collisionPadding={12}
        className={cn("v2 v2-overlay p-3 max-w-[calc(100vw-24px)]", className)}
      >
        {children}
        <PopoverPrimitive.Arrow className="fill-[var(--v2-surface)]" />
      </PopoverPrimitive.Content>
    </PopoverPrimitive.Portal>
  );
}

export const DropdownMenu = DropdownPrimitive.Root;
export const DropdownMenuTrigger = DropdownPrimitive.Trigger;
export const DropdownMenuSeparator = DropdownPrimitive.Separator;
export function DropdownMenuContent({
  children,
  sideOffset = 5,
  align = "start",
  className,
  ...props
}: ComponentProps<typeof DropdownPrimitive.Content>) {
  return (
    <DropdownPrimitive.Portal>
      <DropdownPrimitive.Content
        {...props}
        sideOffset={sideOffset}
        align={align}
        collisionPadding={12}
        className={cn("v2 v2-overlay min-w-40 p-1", className)}
      >
        {children}
      </DropdownPrimitive.Content>
    </DropdownPrimitive.Portal>
  );
}
export function DropdownMenuItem({
  className,
  ...props
}: ComponentProps<typeof DropdownPrimitive.Item>) {
  return (
    <DropdownPrimitive.Item
      {...props}
      className={cn("v2-menu-item", className)}
    />
  );
}

export const Select = SelectPrimitive.Root;
export const SelectValue = SelectPrimitive.Value;
export function SelectTrigger({
  children,
  className,
  ...props
}: ComponentProps<typeof SelectPrimitive.Trigger>) {
  return (
    <SelectPrimitive.Trigger
      {...props}
      className={cn("v2-input flex justify-between gap-2 text-left", className)}
    >
      {children}
      <SelectPrimitive.Icon>
        <ChevronDown size={15} />
      </SelectPrimitive.Icon>
    </SelectPrimitive.Trigger>
  );
}
export function SelectContent({
  children,
  className,
  ...props
}: ComponentProps<typeof SelectPrimitive.Content>) {
  return (
    <SelectPrimitive.Portal>
      <SelectPrimitive.Content
        {...props}
        position="popper"
        sideOffset={5}
        className={cn(
          "v2 v2-overlay max-h-[min(320px,60dvh)] min-w-[var(--radix-select-trigger-width)] overflow-auto p-1",
          className,
        )}
      >
        <SelectPrimitive.Viewport>{children}</SelectPrimitive.Viewport>
      </SelectPrimitive.Content>
    </SelectPrimitive.Portal>
  );
}
export function SelectItem({
  children,
  className,
  ...props
}: ComponentProps<typeof SelectPrimitive.Item>) {
  return (
    <SelectPrimitive.Item
      {...props}
      className={cn("v2-menu-item relative pr-7", className)}
    >
      <SelectPrimitive.ItemText>{children}</SelectPrimitive.ItemText>
      <SelectPrimitive.ItemIndicator className="absolute right-2">
        <Check size={14} />
      </SelectPrimitive.ItemIndicator>
    </SelectPrimitive.Item>
  );
}

export const TooltipProvider = TooltipPrimitive.Provider;
export const Tooltip = TooltipPrimitive.Root;
export const TooltipTrigger = TooltipPrimitive.Trigger;
export function TooltipContent({
  children,
  sideOffset = 6,
  ...props
}: ComponentProps<typeof TooltipPrimitive.Content>) {
  return (
    <TooltipPrimitive.Portal>
      <TooltipPrimitive.Content
        {...props}
        sideOffset={sideOffset}
        className="v2 v2-overlay px-2 py-1 text-[var(--v2-type-meta)]"
      >
        {children}
      </TooltipPrimitive.Content>
    </TooltipPrimitive.Portal>
  );
}

export const Tabs = TabsPrimitive.Root;
export const TabsContent = TabsPrimitive.Content;
export function TabsList({
  children,
  className,
  ...props
}: ComponentProps<typeof TabsPrimitive.List>) {
  return (
    <TabsPrimitive.List
      {...props}
      className={cn(
        "inline-flex gap-1 rounded-[var(--v2-radius)] bg-[var(--v2-subtle)] p-1",
        className,
      )}
    >
      {children}
    </TabsPrimitive.List>
  );
}
export function TabsTrigger({
  children,
  className,
  ...props
}: ComponentProps<typeof TabsPrimitive.Trigger>) {
  return (
    <TabsPrimitive.Trigger
      {...props}
      className={cn(
        "rounded-[var(--v2-radius-sm)] px-3 py-1.5 text-[var(--v2-type-control)] text-[var(--v2-muted)] data-[state=active]:bg-[var(--v2-surface)] data-[state=active]:text-[var(--v2-text)] data-[state=active]:shadow-sm",
        className,
      )}
    >
      {children}
    </TabsPrimitive.Trigger>
  );
}

/** 非模式类弹层只负责局部操作，不占主内容列宽；移动端是否改为 Sheet 由页面决定。 */
export function ResponsiveInfo({
  title,
  children,
  mobile,
}: {
  title: string;
  children: ReactNode;
  mobile: boolean;
}) {
  const trigger = (
    <IconButton icon={ChevronRight} variant="ghost" label={title} />
  );
  if (mobile)
    return (
      <Sheet>
        <SheetTrigger asChild>{trigger}</SheetTrigger>
        <SheetContent>
          <SheetTitle className="v2-text-section mb-4">{title}</SheetTitle>
          {children}
        </SheetContent>
      </Sheet>
    );
  return (
    <Popover>
      <PopoverTrigger asChild>{trigger}</PopoverTrigger>
      <PopoverContent>
        <div className="v2-text-section mb-2">{title}</div>
        {children}
      </PopoverContent>
    </Popover>
  );
}
