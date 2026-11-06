"use client";

import Link from "next/link";
import { useParams } from "next/navigation";
import { useQuery } from "@tanstack/react-query";
import { ArrowLeft } from "lucide-react";
import { PageHeader } from "@/components/page-header";
import { Card, CardContent } from "@/components/ui/card";
import { apiFetch } from "@/lib/auth";
import { formatDate } from "@/lib/utils";

type Submission = {
  id: string;
  data: Record<string, string>;
  consent: boolean;
  created_at: string;
};

type SubmissionResponse = {
  data: Submission[];
  pagination: { total: number; limit: number; offset: number };
};

export default function FormSubmissionsPage() {
  const { id } = useParams<{ id: string }>();
  const query = useQuery({
    queryKey: ["form-submissions", id],
    queryFn: () => apiFetch<SubmissionResponse>(`/forms/${id}/submissions?limit=50`),
  });
  const submissions = query.data?.data ?? [];

  if (query.isLoading) return <PageHeader eyebrow="Marketing" title="Form submissions" description="Loading submissions..." />;
  if (query.isError) {
    return (
      <div>
        <PageHeader eyebrow="Marketing" title="Form submissions" description="The submissions could not be loaded." />
        <p role="alert" className="text-sm text-rose-700">{query.error instanceof Error ? query.error.message : "Could not load submissions."}</p>
        <button type="button" className="mt-3 text-sm text-indigo-700 underline" onClick={() => query.refetch()}>Retry</button>
      </div>
    );
  }

  return (
    <div>
      <PageHeader
        eyebrow="Marketing"
        title="Form submissions"
        description={`${query.data?.pagination.total ?? 0} submissions`}
        actions={<Link href={`/forms/${id}`} className="inline-flex h-9 items-center gap-1.5 rounded-lg px-3 text-sm text-slate-600 ring-1 ring-inset ring-slate-200 hover:bg-slate-50"><ArrowLeft className="h-4 w-4" /> Form builder</Link>}
      />
      {submissions.length === 0 ? (
        <Card className="border-dashed"><CardContent className="p-8 text-center text-sm text-slate-500">Submissions will appear here when someone completes this form.</CardContent></Card>
      ) : (
        <div className="space-y-3">
          {submissions.map((submission) => (
            <Card key={submission.id}>
              <CardContent className="p-4">
                <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
                  <time className="text-xs text-slate-500">{formatDate(submission.created_at)}</time>
                  <span className={`rounded-full px-2 py-1 text-xs ${submission.consent ? "bg-emerald-50 text-emerald-700" : "bg-slate-100 text-slate-600"}`}>
                    {submission.consent ? "Consent recorded" : "No consent required"}
                  </span>
                </div>
                <dl className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
                  {Object.entries(submission.data).map(([key, value]) => (
                    <div key={key} className="min-w-0">
                      <dt className="text-xs capitalize text-slate-500">{key.replaceAll("_", " ")}</dt>
                      <dd className="break-words text-sm font-medium text-slate-900">{value || "—"}</dd>
                    </div>
                  ))}
                </dl>
              </CardContent>
            </Card>
          ))}
        </div>
      )}
    </div>
  );
}
