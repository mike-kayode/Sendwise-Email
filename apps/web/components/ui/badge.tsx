import type { HTMLAttributes } from "react";
import { cn } from "@/lib/utils";

type BadgeVariant =
  | "neutral"
  | "success"
  | "warning"
  | "danger"
  | "info"
  | "indigo"
  | "outline";

const variants: Record<BadgeVariant, string> = {
  neutral: "bg-slate-100 text-slate-700 ring-slate-200",
  success: "bg-emerald-50 text-emerald-700 ring-emerald-200",
  warning: "bg-amber-50 text-amber-700 ring-amber-200",
  danger: "bg-rose-50 text-rose-700 ring-rose-200",
  info: "bg-cyan-50 text-cyan-700 ring-cyan-200",
  indigo: "bg-indigo-50 text-indigo-700 ring-indigo-200",
  outline: "bg-white text-slate-600 ring-slate-200",
};

export function Badge({
  className,
  variant = "neutral",
  ...props
}: HTMLAttributes<HTMLSpanElement> & { variant?: BadgeVariant }) {
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium ring-1 ring-inset",
        variants[variant],
        className,
      )}
      {...props}
    />
  );
}

export function StatusBadge({ status }: { status?: string | null }) {
  const variant: Record<string, BadgeVariant> = {
    completed: "success",
    sent: "success",
    subscribed: "success",
    verified: "success",
    active: "success",
    paid: "success",
    scheduled: "info",
    sending: "indigo",
    running: "info",
    draft: "neutral",
    pending: "warning",
    paused: "warning",
    misconfigured: "warning",
    unsubscribed: "neutral",
    bounced: "danger",
    complained: "danger",
    failed: "danger",
    cancelled: "danger",
  };

  const value = status?.replaceAll("_", " ") || "unknown";
  return <Badge variant={variant[status ?? ""] ?? "neutral"}>{value}</Badge>;
}
