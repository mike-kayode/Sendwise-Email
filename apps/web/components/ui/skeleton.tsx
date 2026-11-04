"use client";

import { cn } from "@/lib/utils";

export function Skeleton({
  className,
}: {
  className?: string;
}) {
  return (
    <span
      className={cn(
        "animate-pulse flex items-center gap-2 rounded-md bg-muted",
        className
      )}
    />
  );
}

export function SkeletonText({
  className,
}: {
  className?: string;
}) {
  return (
    <span className={cn("h-4 rounded-full bg-muted pulse-text", className)} />
  );
}