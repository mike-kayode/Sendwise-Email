import type { Metadata } from "next";
import { Plus, FormInput } from "lucide-react";
import { PageHeader } from "@/components/page-header";
import { Card, CardHeader, CardTitle, CardDescription, CardContent } from "@/components/ui/card";
import { Button } from "@/components/ui/button";

export const metadata: Metadata = { title: "Forms" };

export default function FormsPage() {
  return (
    <div>
      <PageHeader
        eyebrow="Marketing"
        title="Signup forms"
        description="Collect contacts with hosted forms or an embeddable snippet."
        actions={
          <button className="inline-flex h-9 items-center gap-1.5 rounded-lg bg-indigo-600 px-4 text-sm font-medium text-white shadow-sm hover:bg-indigo-500 disabled:opacity-60 disabled:hover:bg-indigo-600" disabled>
            <Plus className="h-4 w-4" /> New form
          </button>
        }
      />

      <Card>
        <CardHeader>
          <CardTitle>Coming soon</CardTitle>
          <CardDescription>
            Build hosted signup forms and embeddable snippets to grow your audience.
            Form builder is in development for the next release.
          </CardDescription>
        </CardHeader>
        <CardContent className="rounded-lg border border-dashed border-slate-200 bg-slate-50 p-8 text-center">
          <FormInput className="h-10 w-10 mx-auto text-slate-400 mb-3" />
          <h3 className="text-sm font-semibold text-slate-900 mb-1">Form builder</h3>
          <p className="text-xs text-slate-500 max-w-xs mx-auto">
            Drag-and-drop form builder with inline validation, double opt-in, and custom
            redirect pages. Stay tuned.
          </p>
        </CardContent>
      </Card>
    </div>
  );
}
