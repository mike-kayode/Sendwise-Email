import type { ReactNode } from "react";
import { ArrowDownRight, ArrowUpRight } from "lucide-react";
import { Card } from "@/components/ui/card";
import { cn, formatNumber } from "@/lib/utils";

export function StatCard({
  label,
  value,
  change,
  changeLabel = "vs last month",
  icon,
  valueIsNumber = true,
}: {
  label: string;
  value: number | string;
  change?: number;
  changeLabel?: string;
  icon?: ReactNode;
  valueIsNumber?: boolean;
}) {
  const positive = (change ?? 0) >= 0;

  return (
    <Card className="p-5">
      <div className="flex items-start justify-between gap-3">
        <div>
          <p className="text-sm font-medium text-slate-500">{label}</p>
          <p className="mt-2 text-2xl font-semibold tracking-tight text-slate-900">
            {valueIsNumber && typeof value === "number" ? formatNumber(value) : value}
          </p>
        </div>
        {icon ? (
          <span className="flex h-9 w-9 items-center justify-center rounded-lg bg-indigo-50 text-indigo-600">
            {icon}
          </span>
        ) : null}
      </div>
      {change !== undefined ? (
        <div className="mt-3 flex items-center gap-2 text-xs">
          <span
            className={cn(
              "inline-flex items-center gap-0.5 rounded-full px-1.5 py-0.5 font-medium",
              positive ? "bg-emerald-50 text-emerald-700" : "bg-rose-50 text-rose-700",
            )}
          >
            {positive ? <ArrowUpRight className="h-3 w-3" /> : <ArrowDownRight className="h-3 w-3" />}
            {Math.abs(change).toFixed(1)}%
          </span>
          <span className="text-slate-400">{changeLabel}</span>
        </div>
      ) : null}
    </Card>
  );
}
