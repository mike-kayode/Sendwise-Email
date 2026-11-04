import type { Metadata } from "next";
import { Code2, ExternalLink, FormInput, Plus, Users } from "lucide-react";
import { PageHeader } from "@/components/page-header";
import { StatCard } from "@/components/stat-card";
import { Card, CardHeader, CardTitle, CardDescription, CardContent } from "@/components/ui/card";
import { Badge, StatusBadge } from "@/components/ui/badge";
import { formatNumber } from "@/lib/utils";

export const metadata: Metadata = { title: "Forms" };

const forms = [
  { id: "frm_01", name: "Newsletter signup", status: "active", submissions: 4820, conversion: 8.4, list: "Newsletter subscribers", updated: "2 days ago" },
  { id: "frm_02", name: "Webinar registration", status: "active", submissions: 862, conversion: 12.1, list: "Webinar attendees", updated: "5 days ago" },
  { id: "frm_03", name: "Exit intent offer", status: "paused", submissions: 214, conversion: 3.2, list: "Trial accounts", updated: "2 weeks ago" },
  { id: "frm_04", name: "Footer signup", status: "active", submissions: 1840, conversion: 2.6, list: "Newsletter subscribers", updated: "3 weeks ago" },
];

export default function FormsPage() {
  return (
    <div>
      <PageHeader
        eyebrow="Marketing"
        title="Signup forms"
        description="Collect contacts with hosted forms or an embeddable snippet."
        actions={
          <button className="inline-flex h-9 items-center gap-1.5 rounded-lg bg-indigo-600 px-4 text-sm font-medium text-white shadow-sm hover:bg-indigo-500">
            <Plus className="h-4 w-4" /> New form
          </button>
        }
      />

      <div className="grid gap-4 sm:grid-cols-3">
        <StatCard label="Forms" value={4} icon={<FormInput className="h-4 w-4" />} />
        <StatCard label="Submissions this month" value={7736} change={18.2} icon={<Users className="h-4 w-4" />} />
        <StatCard label="Average conversion" value="6.6%" change={0.9} valueIsNumber={false} />
      </div>

      <div className="mt-5 grid gap-5 lg:grid-cols-2">
        {forms.map((form) => (
          <Card key={form.id}>
            <CardHeader className="flex items-start justify-between">
              <div>
                <div className="flex items-center gap-2">
                  <CardTitle>{form.name}</CardTitle>
                  <StatusBadge status={form.status} />
                </div>
                <CardDescription>Adds to {form.list}</CardDescription>
              </div>
              <Badge variant="outline">{form.conversion}% conversion</Badge>
            </CardHeader>
            <CardContent>
              <div className="grid grid-cols-2 gap-4 rounded-lg bg-slate-50 p-4 text-sm">
                <div>
                  <p className="text-xs text-slate-500">Submissions</p>
                  <p className="text-lg font-semibold text-slate-900">{formatNumber(form.submissions)}</p>
                </div>
                <div>
                  <p className="text-xs text-slate-500">Last updated</p>
                  <p className="text-sm font-medium text-slate-900">{form.updated}</p>
                </div>
              </div>
              <div className="mt-4 flex flex-wrap gap-2">
                <button className="inline-flex h-8 items-center gap-1.5 rounded-lg bg-slate-900 px-3 text-xs font-medium text-white hover:bg-slate-800">
                  <Code2 className="h-3.5 w-3.5" /> Embed code
                </button>
                <button className="inline-flex h-8 items-center gap-1.5 rounded-lg px-3 text-xs font-medium text-slate-600 ring-1 ring-inset ring-slate-200 hover:bg-slate-50">
                  <ExternalLink className="h-3.5 w-3.5" /> Open form
                </button>
                <button className="ml-auto text-xs font-medium text-indigo-600 hover:text-indigo-500">Edit</button>
              </div>
            </CardContent>
          </Card>
        ))}

        <Card className="border-dashed">
          <CardContent className="space-y-3 p-5">
            <h3 className="text-sm font-semibold text-slate-900">Embed snippet</h3>
            <p className="text-xs text-slate-500">
              Paste this into your site to show the newsletter signup form.
            </p>
            <pre className="overflow-x-auto rounded-lg bg-slate-900 p-3 text-[11px] leading-relaxed text-slate-100">
{`<script src="https://app.acme.studio/embed.js"
  data-form="frm_01" async></script>
<div data-sendwise-form="frm_01"></div>`}
            </pre>
            <button className="inline-flex h-8 items-center rounded-lg bg-white px-3 text-xs font-medium text-slate-700 ring-1 ring-inset ring-slate-200 hover:bg-slate-50">
              Copy snippet
            </button>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
