"use client";

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";
import { Plus, FormInput, Users, ExternalLink, Code2 } from "lucide-react";
import { PageHeader } from "@/components/page-header";
import { StatCard } from "@/components/stat-card";
import { Card, CardHeader, CardTitle, CardDescription, CardContent } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { apiFetch, type ContactImport } from "@/lib/auth";
import { formatNumber } from "@/lib/utils";
import { useToast } from "@/components/ui/use-toast";

export default function FormsView() {
  const { toast } = useToast();

  const formsQuery = useQuery({
    queryKey: ["contact-lists"],
    queryFn: async () => {
      const res = await apiFetch<{ data: any[] }>("/lists");
      return res.data ?? [];
    },
  });

  const forms = formsQuery.data ?? [];
  const totalSubmissions = forms.reduce((sum: number, f: any) => sum + (f.subscriber_count ?? 0), 0);

  if (formsQuery.isLoading) {
    return (
      <div>
        <PageHeader eyebrow="Marketing" title="Signup forms" description="Loading your forms..." />
      </div>
    );
  }

  return (
    <div>
      <PageHeader
        eyebrow="Marketing"
        title="Signup forms"
        description="Collect contacts with hosted forms or an embeddable snippet."
        actions={
          <Link href="/audience/lists" className="inline-flex h-9 items-center gap-1.5 rounded-lg bg-indigo-600 px-4 text-sm font-medium text-white shadow-sm hover:bg-indigo-500">
            <Plus className="h-4 w-4" /> New form
          </Link>
        }
      />

      <div className="grid gap-4 sm:grid-cols-3">
        <StatCard label="Forms" value={forms.length} icon={<FormInput className="h-4 w-4" />} />
        <StatCard label="Submissions this month" value={totalSubmissions} change={0} icon={<Users className="h-4 w-4" />} />
        <StatCard label="Average conversion" value={forms.length > 0 ? "N/A" : "0%"} valueIsNumber={false} />
      </div>

      {forms.length === 0 ? (
        <Card className="mt-5 border-dashed">
          <CardContent className="p-8 text-center">
            <FormInput className="h-10 w-10 mx-auto text-slate-400 mb-3" />
            <h3 className="text-sm font-semibold text-slate-900 mb-1">No forms yet</h3>
            <p className="text-xs text-slate-500 max-w-xs mx-auto mb-4">
              Create your first signup form to start collecting contacts.
            </p>
            <Link href="/audience/lists" className="inline-flex h-9 items-center gap-1.5 rounded-lg bg-indigo-600 px-4 text-sm font-medium text-white shadow-sm hover:bg-indigo-500">
              <Plus className="h-4 w-4" /> Create form
            </Link>
          </CardContent>
        </Card>
      ) : (
        <div className="mt-5 grid gap-5 lg:grid-cols-2">
          {forms.map((form: any) => (
            <Card key={form.id}>
              <CardHeader className="flex items-start justify-between">
                <div>
                  <div className="flex items-center gap-2">
                    <CardTitle>{form.name}</CardTitle>
                    <Badge variant="outline">Active</Badge>
                  </div>
                  <CardDescription>Adds to {form.name}</CardDescription>
                </div>
              </CardHeader>
              <CardContent>
                <div className="grid grid-cols-2 gap-4 rounded-lg bg-slate-50 p-4 text-sm">
                  <div>
                    <p className="text-xs text-slate-500">Subscribers</p>
                    <p className="text-lg font-semibold text-slate-900">{formatNumber(form.subscriber_count ?? 0)}</p>
                  </div>
                  <div>
                    <p className="text-xs text-slate-500">Created</p>
                    <p className="text-sm font-medium text-slate-900">{form.created_at}</p>
                  </div>
                </div>
                <div className="mt-4 flex flex-wrap gap-2">
                  <button className="inline-flex h-8 items-center gap-1.5 rounded-lg bg-slate-900 px-3 text-xs font-medium text-white hover:bg-slate-800">
                    <Code2 className="h-3.5 w-3.5" /> Embed code
                  </button>
                  <button className="inline-flex h-8 items-center gap-1.5 rounded-lg px-3 text-xs font-medium text-slate-600 ring-1 ring-inset ring-slate-200 hover:bg-slate-50">
                    <ExternalLink className="h-3.5 w-3.5" /> Open form
                  </button>
                </div>
              </CardContent>
            </Card>
          ))}
        </div>
      )}
    </div>
  );
}
