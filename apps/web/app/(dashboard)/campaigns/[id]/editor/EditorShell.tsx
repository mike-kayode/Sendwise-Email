"use client";

import Link from "next/link";
import { useQuery } from "@tanstack/react-query";
import { ArrowLeft } from "lucide-react";
import { PageHeader } from "@/components/page-header";
import { type EmailDesignBlock } from "@/components/email-design-editor";
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
  const content = typeof res.content === "string" ? JSON.parse(res.content) as unknown : res.content;
  const contentObject = content && typeof content === "object" ? content as Record<string, unknown> : {};
  const rawBlocks = Array.isArray(contentObject.blocks) ? contentObject.blocks : [];
  const blocks: EmailDesignBlock[] = rawBlocks.map((rawBlock, index) => {
    if (!rawBlock || typeof rawBlock !== "object") {
      return { id: `block-${index}`, type: "paragraph", content: "" };
    }
    const block = rawBlock as Record<string, unknown>;
    return {
      id: typeof block.id === "string" ? block.id : `block-${index}`,
      type: typeof block.type === "string" ? block.type : "paragraph",
      content: typeof block.content === "string" ? block.content : "",
      ...(typeof block.url === "string" ? { url: block.url } : {}),
      ...(typeof block.src === "string" ? { src: block.src } : {}),
      ...(typeof block.alt === "string" ? { alt: block.alt } : {}),
      ...(typeof block.align === "string" ? { align: block.align } : {}),
      ...(typeof block.size === "string" ? { size: block.size } : {}),
    };
  });

  return (
    <div>
      {backLink}

      <PageHeader
        title={res.name}
        description="Edit sender details, subject, and reusable email content."
      />
      <EditorForm initialCampaign={res} initialBlocks={blocks} />
    </div>
  );
}
