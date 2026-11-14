"use client";

import { Input, Select } from "@/components/ui/input";

export type ComplianceFooterSetting = {
  alignment: "left" | "center" | "right";
  background_color: string;
  text_color: string;
  button_color: string;
  button_text: string;
};

export const defaultComplianceFooter: ComplianceFooterSetting = {
  alignment: "center",
  background_color: "#f8fafc",
  text_color: "#64748b",
  button_color: "#4f46e5",
  button_text: "Unsubscribe",
};

export function ComplianceFooterEditor({
  value,
  onChange,
}: {
  value: ComplianceFooterSetting;
  onChange: (value: ComplianceFooterSetting) => void;
}) {
  const update = (patch: Partial<ComplianceFooterSetting>) => onChange({ ...value, ...patch });

  return (
    <section className="space-y-3 rounded-xl border border-slate-200 bg-white p-5" aria-label="Compliance footer editor">
      <div>
        <h2 className="text-base font-semibold text-slate-900">Compliance footer</h2>
        <p className="mt-1 text-xs text-slate-500">
          The unsubscribe button is always included below the email content. Postal address details are optional.
        </p>
      </div>
      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <label className="text-sm font-medium text-slate-700">
          Alignment
          <Select
            aria-label="Footer alignment"
            className="mt-1"
            value={value.alignment}
            onChange={(event) => update({ alignment: event.target.value as ComplianceFooterSetting["alignment"] })}
          >
            <option value="left">Left</option>
            <option value="center">Center</option>
            <option value="right">Right</option>
          </Select>
        </label>
        <label className="text-sm font-medium text-slate-700">
          Footer background
          <Input aria-label="Footer background color" className="mt-1 h-10" type="color" value={value.background_color} onChange={(event) => update({ background_color: event.target.value })} />
        </label>
        <label className="text-sm font-medium text-slate-700">
          Footer text
          <Input aria-label="Footer text color" className="mt-1 h-10" type="color" value={value.text_color} onChange={(event) => update({ text_color: event.target.value })} />
        </label>
        <label className="text-sm font-medium text-slate-700">
          Unsubscribe button
          <Input aria-label="Unsubscribe button color" className="mt-1 h-10" type="color" value={value.button_color} onChange={(event) => update({ button_color: event.target.value })} />
        </label>
        <label className="text-sm font-medium text-slate-700 sm:col-span-2">
          Button label
          <Input aria-label="Unsubscribe button label" className="mt-1" maxLength={60} value={value.button_text} onChange={(event) => update({ button_text: event.target.value })} />
        </label>
        <p className="self-end text-xs text-slate-500">Placement is fixed below all template content so the unsubscribe control remains available.</p>
      </div>
    </section>
  );
}
