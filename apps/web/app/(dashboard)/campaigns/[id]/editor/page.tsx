import type { Metadata } from "next";
import Link from "next/link";
import { PageHeader } from "@/components/page-header";
import { EmailPreview } from "@/components/email-preview";
import dynamic from "next/dynamic";
import { ArrowLeft } from "lucide-react";
import { apiFetch, type Campaign } from "@/lib/auth";

export const metadata: Metadata = { title: "Campaign editor" };

const EditorForm = dynamic(() => import("./EditorForm"), { ssr: false });

export default async function CampaignEditorPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  const res = await apiFetch<Campaign>(`/campaigns/${id}`);
  const content = typeof res.content === "string" ? JSON.parse(res.content) : res.content ?? {};

  return (
    <div>
      <Link href={`/campaigns/${id}`} className="mb-4 inline-flex items-center gap-1 text-sm text-slate-500 hover:text-slate-900">
        <ArrowLeft className="h-4 w-4" /> Back to campaigns
      </Link>

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
