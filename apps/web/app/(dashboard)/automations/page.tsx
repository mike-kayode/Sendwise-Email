import Link from "next/link";
import type { Metadata } from "next";
import { ArrowRight, Plus, Zap } from "lucide-react";
import { PageHeader } from "@/components/page-header";
import { Card, CardHeader, CardTitle, CardDescription, CardContent } from "@/components/ui/card";

export const metadata: Metadata = { title: "Automations" };

export default function AutomationsPage() {
  return (
    <div>
      <PageHeader
        eyebrow="Marketing"
        title="Automations"
        description="Send the right message automatically when contacts take an action."
        actions={
          <button className="inline-flex h-9 items-center gap-1.5 rounded-lg bg-indigo-600 px-4 text-sm font-medium text-white shadow-sm hover:bg-indigo-500 disabled:opacity-60 disabled:hover:bg-indigo-600" disabled>
            <Plus className="h-4 w-4" /> New automation
          </button>
        }
      />

      <Card>
        <CardHeader>
          <CardTitle>Coming soon</CardTitle>
          <CardDescription>
            Build visual automation flows (welcome series, re-engagement, purchase follow-up)
            directly in Sendwise. This feature is in development for the next release.
          </CardDescription>
        </CardHeader>
        <CardContent className="rounded-lg border border-dashed border-slate-200 bg-slate-50 p-8 text-center">
          <Zap className="h-10 w-10 mx-auto text-slate-400 mb-3" />
          <h3 className="text-sm font-semibold text-slate-900 mb-1">Automation builder</h3>
          <p className="text-xs text-slate-500 max-w-xs mx-auto">
            Drag-and-drop workflow builder with triggers, delays, and conditional branches.
            Stay tuned.
          </p>
        </CardContent>
      </Card>
    </div>
  );
}
