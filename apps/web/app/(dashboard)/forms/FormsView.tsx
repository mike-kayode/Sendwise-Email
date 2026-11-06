"use client";

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { FileText, Plus, Trash2, Users } from "lucide-react";
import { PageHeader } from "@/components/page-header";
import { StatCard } from "@/components/stat-card";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { apiFetch } from "@/lib/auth";
import { formatDate } from "@/lib/utils";

type SignupForm = {
  id: string;
  name: string;
  slug: string;
  status: "draft" | "published" | "unpublished";
  submission_count: number;
  created_at: string;
};

const fieldDefaults = [
  { field_type: "email", label: "Email", required: true },
  { field_type: "first_name", label: "First name", required: false },
];

export default function FormsView() {
  const router = useRouter();
  const queryClient = useQueryClient();
  const [error, setError] = useState("");
  const formsQuery = useQuery({
    queryKey: ["forms"],
    queryFn: async () => {
      const response = await apiFetch<{ data: SignupForm[] }>("/forms");
      return response.data ?? [];
    },
  });

  const createForm = useMutation({
    mutationFn: () =>
      apiFetch<SignupForm>("/forms", {
        method: "POST",
        body: JSON.stringify({
          name: `Signup form ${Date.now()}`,
          fields: fieldDefaults,
          success_message: "Thank you for subscribing!",
          consent_text: "I agree to receive email updates.",
          spam_protection: true,
          double_opt_in: false,
          tag_ids: [],
        }),
      }),
    onSuccess: (form) => {
      setError("");
      queryClient.invalidateQueries({ queryKey: ["forms"] });
      router.push(`/forms/${form.id}`);
    },
    onError: (reason) => setError(reason instanceof Error ? reason.message : "Could not create the form."),
  });

  const deleteForm = useMutation({
    mutationFn: (id: string) => apiFetch<void>(`/forms/${id}`, { method: "DELETE" }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["forms"] }),
    onError: (reason) => setError(reason instanceof Error ? reason.message : "Could not delete the form."),
  });

  const forms = formsQuery.data ?? [];
  const submissions = forms.reduce((total, form) => total + (form.submission_count ?? 0), 0);
  const published = forms.filter((form) => form.status === "published").length;

  if (formsQuery.isLoading) {
    return <PageHeader eyebrow="Marketing" title="Signup forms" description="Loading your forms..." />;
  }

  return (
    <div>
      <PageHeader
        eyebrow="Marketing"
        title="Signup forms"
        description="Build hosted signup forms and embed them on your site."
        actions={
          <button
            type="button"
            onClick={() => createForm.mutate()}
            disabled={createForm.isPending}
            className="inline-flex h-9 items-center gap-1.5 rounded-lg bg-indigo-600 px-4 text-sm font-medium text-white shadow-sm hover:bg-indigo-500 disabled:opacity-60"
          >
            <Plus className="h-4 w-4" /> {createForm.isPending ? "Creating..." : "New form"}
          </button>
        }
      />

      <div className="grid gap-4 sm:grid-cols-3">
        <StatCard label="Forms" value={forms.length} icon={<FileText className="h-4 w-4" />} />
        <StatCard label="Submissions" value={submissions} icon={<Users className="h-4 w-4" />} />
        <StatCard label="Published" value={published} />
      </div>

      {(error || formsQuery.isError) && (
        <Card className="mt-5 border-rose-200">
          <CardContent className="flex items-center justify-between gap-4 p-4 text-sm text-rose-700">
            <span>{error || (formsQuery.error instanceof Error ? formsQuery.error.message : "Could not load forms.")}</span>
            {formsQuery.isError && <button type="button" className="underline" onClick={() => formsQuery.refetch()}>Retry</button>}
          </CardContent>
        </Card>
      )}

      {formsQuery.isError ? null : forms.length === 0 ? (
        <Card className="mt-5 border-dashed">
          <CardContent className="p-8 text-center">
            <FileText className="mx-auto mb-3 h-10 w-10 text-slate-400" />
            <h2 className="mb-1 text-sm font-semibold text-slate-900">No forms yet</h2>
            <p className="mx-auto mb-4 max-w-xs text-xs text-slate-500">
              Create a form to collect subscribers on a hosted page or your website.
            </p>
            <button type="button" onClick={() => createForm.mutate()} disabled={createForm.isPending}
              className="inline-flex h-9 items-center gap-1.5 rounded-lg bg-indigo-600 px-4 text-sm font-medium text-white disabled:opacity-60">
              <Plus className="h-4 w-4" /> Create form
            </button>
          </CardContent>
        </Card>
      ) : (
        <div className="mt-5 grid gap-5 lg:grid-cols-2">
          {forms.map((form) => (
            <Card key={form.id}>
              <CardHeader className="flex-row items-start justify-between gap-4">
                <div>
                  <div className="flex flex-wrap items-center gap-2">
                    <CardTitle>{form.name}</CardTitle>
                    <Badge variant={form.status === "published" ? "success" : "outline"}>{form.status}</Badge>
                  </div>
                  <CardDescription className="mt-1">/{form.slug}</CardDescription>
                </div>
                <button
                  type="button"
                  aria-label={`Delete ${form.name}`}
                  onClick={() => {
                    if (window.confirm(`Delete "${form.name}" and its submissions?`)) deleteForm.mutate(form.id);
                  }}
                  className="rounded-md p-2 text-slate-500 hover:bg-rose-50 hover:text-rose-700"
                >
                  <Trash2 className="h-4 w-4" />
                </button>
              </CardHeader>
              <CardContent>
                <div className="grid grid-cols-2 gap-4 rounded-lg bg-slate-50 p-4 text-sm">
                  <div>
                    <p className="text-xs text-slate-500">Submissions</p>
                    <p className="text-lg font-semibold text-slate-900">{form.submission_count ?? 0}</p>
                  </div>
                  <div>
                    <p className="text-xs text-slate-500">Created</p>
                    <p className="text-sm font-medium text-slate-900">{formatDate(form.created_at)}</p>
                  </div>
                </div>
                <div className="mt-4 flex flex-wrap gap-2">
                  <Link className="inline-flex h-8 items-center rounded-lg bg-slate-900 px-3 text-xs font-medium text-white hover:bg-slate-800" href={`/forms/${form.id}`}>
                    Edit form
                  </Link>
                  <Link className="inline-flex h-8 items-center rounded-lg px-3 text-xs font-medium text-slate-600 ring-1 ring-inset ring-slate-200 hover:bg-slate-50" href={`/forms/${form.id}/embed`}>
                    Embed
                  </Link>
                  <Link className="inline-flex h-8 items-center rounded-lg px-3 text-xs font-medium text-slate-600 ring-1 ring-inset ring-slate-200 hover:bg-slate-50" href={`/forms/${form.id}/submissions`}>
                    Submissions
                  </Link>
                  {form.status === "published" && (
                    <Link className="inline-flex h-8 items-center rounded-lg px-3 text-xs font-medium text-indigo-700 ring-1 ring-inset ring-indigo-200 hover:bg-indigo-50" href={`/f/${form.slug}`} target="_blank">
                      Open hosted form
                    </Link>
                  )}
                </div>
              </CardContent>
            </Card>
          ))}
        </div>
      )}
    </div>
  );
}
