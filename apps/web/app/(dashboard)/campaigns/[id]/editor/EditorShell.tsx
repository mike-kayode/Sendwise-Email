"use client";

import Link from "next/link";
import { useQuery } from "@tanstack/react-query";
import { ArrowLeft } from "lucide-react";
import { PageHeader } from "@/components/page-header";
import { EmailPreview } from "@/components/email-preview";
import { Card, CardContent } from "@/components/ui/card";
import { apiFetch, type Campaign } from "@/lib/auth";
import EditorForm from "./EditorForm";

type EditorShellProps = {
  campaignId: string;
};

export default function EditorShell({ campaignId }: EditorShellProps) {
  const campaignQuery = useQuery({
    queryKey: ["campaign", campaignId],
    queryFn: async () => apiFetch<Campaign>(`/campaigns/${campaignId}`),
  });

  const backLink = (
    <Link
      href={`/campaigns/${campaignId}`}
      className="mb-4 inline-flex items-center gap-1 text-sm text-slate-500 hover:text-slate-900"
    >
      <ArrowLeft className="h-4 w-4" /> Back to campaigns
    </Link>
  );

  if (campaignQuery.isLoading) {
    return (
      <div>
        {backLink}
        <PageHeader title="Campaign editor" description="Loading your campaign..." />
      </div>
    );
  }

  if (campaignQuery.error || !campaignQuery.data) {
    return (
      <div>
        {backLink}
        <PageHeader title="Campaign editor" description="Unable to load this campaign." />
        <Card>
          <CardContent className="py-10 text-center">
            <p className="text-slate-500">{campaignQuery.error?.message ?? "Campaign not found."}</p>
          </CardContent>
        </Card>
      </div>
    );
  }

  const res = campaignQuery.data;
  const content = typeof res.content === "string" ? JSON.parse(res.content) : res.content ?? {};

  return (
    <div>
      {backLink}

      <PageHeader
        title={res.name}
        description={res.subject}
        actions={
          <EditorForm
            campaignId={res.id}
            initialSubject={res.subject ?? ""}
            initialPreviewText={content.preview_text ?? ""}
            initialSenderName={res.sender_name ?? ""}
            initialSenderEmail={res.sender_email ?? ""}
            initialReplyTo={res.reply_to ?? ""}
            initialAudience={res.list_id ?? ""}
            initialTags=""
            initialBlocks={content.blocks ?? []}
          />
        }
      />

      <div className="mt-4 rounded-xl bg-slate-100 p-4 sm:p-8">
        <EmailPreview subject={res.subject} />
      </div>
    </div>
  );
}
