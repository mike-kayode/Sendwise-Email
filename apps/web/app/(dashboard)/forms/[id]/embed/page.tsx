"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { useParams } from "next/navigation";
import { useQuery } from "@tanstack/react-query";
import { ArrowLeft, Copy } from "lucide-react";
import { PageHeader } from "@/components/page-header";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { apiFetch } from "@/lib/auth";

type FormInfo = { name: string; slug: string; status: string };

export default function FormEmbedPage() {
  const { id } = useParams<{ id: string }>();
  const [copyState, setCopyState] = useState("");
  const [origin, setOrigin] = useState("");
  const query = useQuery({ queryKey: ["form", id], queryFn: () => apiFetch<FormInfo>(`/forms/${id}`) });
  useEffect(() => setOrigin(window.location.origin), []);

  if (query.isLoading) return <PageHeader eyebrow="Marketing" title="Embed form" description="Loading form..." />;
  if (query.isError || !query.data) {
    return <PageHeader eyebrow="Marketing" title="Embed form" description={query.error instanceof Error ? query.error.message : "The form could not be loaded."} />;
  }

  const hostedURL = `${origin}/f/${query.data.slug}`;
  const snippet = `<iframe src="${hostedURL}" title="${query.data.name.replaceAll('"', "&quot;")}" width="100%" height="640" frameborder="0"></iframe>`;
  const copy = async (value: string, label: string) => {
    try {
      await navigator.clipboard.writeText(value);
      setCopyState(`${label} copied`);
    } catch {
      setCopyState("Clipboard access was denied. Select and copy the text instead.");
    }
  };

  return (
    <div>
      <PageHeader
        eyebrow="Marketing"
        title="Embed form"
        description={`Share ${query.data.name} on your website.`}
        actions={<Link href={`/forms/${id}`} className="inline-flex h-9 items-center gap-1.5 rounded-lg px-3 text-sm text-slate-600 ring-1 ring-inset ring-slate-200 hover:bg-slate-50"><ArrowLeft className="h-4 w-4" /> Form builder</Link>}
      />
      {query.data.status !== "published" && <p className="mb-4 rounded-lg bg-amber-50 p-3 text-sm text-amber-800">Publish this form before sharing it.</p>}
      {copyState && <p role="status" className="mb-4 text-sm text-slate-600">{copyState}</p>}
      <Card>
        <CardHeader><CardTitle>Hosted form URL</CardTitle></CardHeader>
        <CardContent>
          <div className="flex flex-col gap-3 sm:flex-row">
            <input aria-label="Hosted form URL" readOnly value={hostedURL} className="min-w-0 flex-1 rounded-lg border border-slate-300 px-3 py-2 text-sm" />
            <button type="button" onClick={() => copy(hostedURL, "URL")} className="inline-flex items-center justify-center gap-2 rounded-lg bg-slate-900 px-4 py-2 text-sm font-medium text-white"><Copy className="h-4 w-4" /> Copy URL</button>
            <Link href={`/f/${query.data.slug}`} target="_blank" className="inline-flex items-center justify-center rounded-lg px-4 py-2 text-sm font-medium text-indigo-700 ring-1 ring-inset ring-indigo-200">Preview</Link>
          </div>
        </CardContent>
      </Card>
      <Card className="mt-5">
        <CardHeader><CardTitle>HTML embed</CardTitle></CardHeader>
        <CardContent>
          <p className="mb-3 text-sm text-slate-600">Paste this iframe where the signup form should appear.</p>
          <textarea aria-label="HTML embed code" readOnly rows={4} value={snippet} className="w-full rounded-lg border border-slate-300 bg-slate-50 p-3 font-mono text-xs text-slate-800" />
          <button type="button" onClick={() => copy(snippet, "Embed code")} className="mt-3 inline-flex items-center gap-2 rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white"><Copy className="h-4 w-4" /> Copy embed code</button>
        </CardContent>
      </Card>
    </div>
  );
}
