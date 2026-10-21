import {
  forwardRef,
  type ButtonHTMLAttributes,
  type ComponentProps,
  type HTMLAttributes,
  type InputHTMLAttributes,
  type TextareaHTMLAttributes,
} from "react";
import { Slot } from "@radix-ui/react-slot";
import * as SwitchPrimitive from "@radix-ui/react-switch";
import * as AvatarPrimitive from "@radix-ui/react-avatar";
import * as SeparatorPrimitive from "@radix-ui/react-separator";
import { LoaderCircle, type LucideIcon } from "lucide-react";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "./utils";

const buttonVariants = cva("v2-button", {
  variants: {
    variant: { primary: "", secondary: "", ghost: "", danger: "" },
    size: { compact: "", default: "", prominent: "" },
  },
  defaultVariants: { variant: "secondary", size: "default" },
});

type ButtonProps = ButtonHTMLAttributes<HTMLButtonElement> &
  VariantProps<typeof buttonVariants> & { asChild?: boolean };
export const Button = forwardRef<HTMLButtonElement, ButtonProps>(
  function Button(
    {
      asChild = false,
      variant = "secondary",
      size = "default",
      className,
      ...props
    },
    ref,
  ) {
    const Component = asChild ? Slot : "button";
    return (
      <Component
        ref={ref}
        data-variant={variant}
        data-size={size}
        className={cn(buttonVariants({ variant, size }), className)}
        {...props}
      />
    );
  },
);

export function IconButton({
  icon: Icon,
  label,
  ...props
}: Omit<ComponentProps<typeof Button>, "children"> & {
  icon: LucideIcon;
  label: string;
}) {
  return (
    <Button
      {...props}
      className={cn("v2-icon-button", props.className)}
      aria-label={label}
      title={label}
    >
      <Icon size={16} aria-hidden="true" />
    </Button>
  );
}

export function LoadingButton({
  loading,
  children,
  disabled,
  ...props
}: ComponentProps<typeof Button> & { loading?: boolean }) {
  return (
    <Button {...props} disabled={disabled || loading} aria-busy={loading}>
      {loading && (
        <LoaderCircle size={15} className="animate-spin" aria-hidden="true" />
      )}
      {children}
    </Button>
  );
}

export function Input({
  className,
  ...props
}: InputHTMLAttributes<HTMLInputElement>) {
  return <input className={cn("v2-input", className)} {...props} />;
}
export function Textarea({
  className,
  ...props
}: TextareaHTMLAttributes<HTMLTextAreaElement>) {
  return <textarea className={cn("v2-input", className)} {...props} />;
}

export function Typography({
  as: Tag = "p",
  scale = "body",
  className,
  ...props
}: HTMLAttributes<HTMLElement> & {
  as?: "p" | "h1" | "h2" | "h3" | "span";
  scale?: "meta" | "control" | "body" | "section" | "title";
}) {
  return <Tag {...props} className={cn("v2-text-" + scale, className)} />;
}

export function Badge({
  tone = "neutral",
  className,
  ...props
}: HTMLAttributes<HTMLSpanElement> & {
  tone?: "neutral" | "accent" | "success" | "danger";
}) {
  return (
    <span {...props} data-tone={tone} className={cn("v2-badge", className)} />
  );
}

export function Switch({
  className,
  ...props
}: ComponentProps<typeof SwitchPrimitive.Root>) {
  return (
    <SwitchPrimitive.Root {...props} className={cn("v2-switch", className)}>
      <SwitchPrimitive.Thumb className="v2-switch-thumb" />
    </SwitchPrimitive.Root>
  );
}

export function Avatar({
  src,
  fallback,
  className,
}: {
  src?: string;
  fallback: string;
  className?: string;
}) {
  return (
    <AvatarPrimitive.Root
      className={cn(
        "inline-flex size-8 shrink-0 items-center justify-center overflow-hidden rounded-[var(--v2-radius)] bg-[var(--v2-subtle)] text-[var(--v2-type-control)] font-semibold",
        className,
      )}
    >
      {src && (
        <AvatarPrimitive.Image
          src={src}
          alt={fallback}
          className="size-full object-cover"
        />
      )}
      <AvatarPrimitive.Fallback delayMs={0}>
        {fallback}
      </AvatarPrimitive.Fallback>
    </AvatarPrimitive.Root>
  );
}

export function Separator({
  className,
  ...props
}: ComponentProps<typeof SeparatorPrimitive.Root>) {
  return (
    <SeparatorPrimitive.Root
      {...props}
      className={cn("v2-separator", className)}
    />
  );
}
