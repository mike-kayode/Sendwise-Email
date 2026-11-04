import { cn, formatNumber } from "@/lib/utils";

export function BarChart({
  data,
  height = 180,
  color = "bg-indigo-500",
}: {
  data: { label: string; value: number }[];
  height?: number;
  color?: string;
}) {
  const max = Math.max(...data.map((point) => point.value), 1);

  return (
    <div className="flex items-end gap-2" style={{ height }}>
      {data.map((point) => {
        const percent = Math.max((point.value / max) * 100, 3);
        return (
          <div key={point.label} className="group flex h-full flex-1 flex-col items-center justify-end gap-2">
            <div className="relative flex w-full flex-1 items-end">
              <div
                className={cn("w-full rounded-t-md transition group-hover:opacity-80", color)}
                style={{ height: `${percent}%` }}
                title={`${point.label}: ${formatNumber(point.value)}`}
              />
            </div>
            <span className="text-[10px] text-slate-400">{point.label}</span>
          </div>
        );
      })}
    </div>
  );
}

export function GroupedBarChart({
  data,
}: {
  data: { label: string; opens: number; clicks: number }[];
}) {
  return (
    <div className="flex h-44 items-end gap-3">
      {data.map((point) => (
        <div key={point.label} className="flex h-full flex-1 flex-col items-center justify-end gap-2">
          <div className="flex w-full flex-1 items-end justify-center gap-1">
            <div
              className="w-2.5 rounded-t bg-emerald-800"
              style={{ height: `${Math.max(point.opens, 4)}%` }}
              title={`Opens: ${point.opens}%`}
            />
            <div
              className="w-2.5 rounded-t bg-emerald-200"
              style={{ height: `${Math.max(point.clicks * 4, 3)}%` }}
              title={`Clicks: ${point.clicks}%`}
            />
          </div>
          <span className="text-[10px] text-slate-400">{point.label}</span>
        </div>
      ))}
    </div>
  );
}

export function DonutChart({
  value,
  label,
  sublabel,
}: {
  value: number;
  label: string;
  sublabel: string;
}) {
  return (
    <div className="flex items-center gap-5">
      <div
        className="relative flex h-28 w-28 items-center justify-center rounded-full"
        style={{
          background: `conic-gradient(#4f46e5 ${value}%, #e2e8f0 ${value}% 100%)`,
        }}
      >
        <div className="flex h-20 w-20 flex-col items-center justify-center rounded-full bg-white">
          <span className="text-lg font-semibold text-slate-900">{value}%</span>
          <span className="text-[10px] text-slate-500">{label}</span>
        </div>
      </div>
      <div className="space-y-2 text-sm">
        <div>
          <p className="font-medium text-slate-900">{sublabel}</p>
          <p className="text-xs text-slate-500">Across all campaigns this month</p>
        </div>
        <div className="flex items-center gap-2 text-xs text-slate-500">
          <span className="h-2 w-2 rounded-full bg-indigo-600" /> Engaged
        </div>
        <div className="flex items-center gap-2 text-xs text-slate-500">
          <span className="h-2 w-2 rounded-full bg-slate-200" /> Not engaged
        </div>
      </div>
    </div>
  );
}

export function ProgressBar({ value, className }: { value: number; className?: string }) {
  return (
    <div className={cn("h-2 w-full overflow-hidden rounded-full bg-slate-100", className)}>
      <div
        className="h-full rounded-full bg-indigo-600"
        style={{ width: `${Math.min(Math.max(value, 0), 100)}%` }}
      />
    </div>
  );
}
