import type { HTMLAttributes } from "react";
import { cn } from "../lib/utils";

/**
 * 基础灰白骨架屏占位块
 * 纯粹、克制的灰白色阶与柔和脉冲动效
 */
export function Skeleton({
  className,
  ...props
}: HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      aria-hidden="true"
      className={cn("animate-pulse rounded-md bg-muted/70", className)}
      {...props}
    />
  );
}
