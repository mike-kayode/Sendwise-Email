"use client";

import { useState } from "react";
import Link from "next/link";
import { ArrowLeft, Eye, Save } from "lucide-react";
import { EmailDesignEditor, type EmailDesignBlock } from "@/components/email-design-editor";
import { Button } from "@/components/ui/button";
import { Input, Textarea } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import { apiFetch, type Campaign } from "@/lib/auth";

export default function EditorForm({
  initialCampaign,
  initialBlocks,
}: {
  initialCampaign: Campaign;
  initialBlocks: EmailDesignBlock[];
}) {
  const [blocks, setBlocks] = useState(initialBlocks);
  const [subject, setSubject] = useState(initialCampaign.subject ?? "");
  const [previewText, setPreviewText] = useState(initialCampaign.preview_text ?? "");
  const [senderName, setSenderName] = useState(initialCampaign.sender_name ?? "");
  const [senderEmail, setSenderEmail] = useState(initialCampaign.sender_email ?? "");
  const [senderPostalAddress, setSenderPostalAddress] = useState(initialCampaign.sender_postal_address ?? "");
  const [replyTo, setReplyTo] = useState(initialCampaign.reply_to ?? "");
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const save = async () => {
    if (saving) return;
    setSaving(true);
    setSaved(false);
    setError(null);
    try {
      await apiFetch(`/campaigns/${initialCampaign.id}`, {
        method: "PATCH",
        body: JSON.stringify({
          name: initialCampaign.name,
          subject,
          preview_text: previewText,
          sender_name: senderName,
          sender_email: senderEmail,
          sender_postal_address: senderPostalAddress,
          reply_to: replyTo,
          content: { version: 1, blocks },
          track_opens: initialCampaign.track_opens,
          track_clicks: initialCampaign.track_clicks,
        }),
      });
      setSaved(true);
    } catch (saveError) {
      setError(saveError instanceof Error ? saveError.message : "Could not save this campaign.");
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="space-y-5">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div className="flex items-center gap-3">
          <Link href="/campaigns" aria-label="Back to campaigns" className="rounded-lg p-1.5 text-slate-500 hover:bg-slate-100">
            <ArrowLeft className="h-4 w-4" />
          </Link>
          <div>
            <div className="flex items-center gap-2">
              <h1 className="text-lg font-semibold text-slate-900">{initialCampaign.name}</h1>
              <Badge variant="neutral">{initialCampaign.status}</Badge>
            </div>
            <p aria-live="polite" className="text-xs text-slate-500">
              {saved ? "All changes saved." : "Edit campaign details and email content."}
            </p>
          </div>
        </div>
        <div className="flex gap-2">
          <Link href={`/campaigns/${initialCampaign.id}/preview`} className="inline-flex h-9 items-center gap-2 rounded-lg border border-slate-200 bg-white px-4 text-sm font-medium text-slate-700 hover:bg-slate-50">
            <Eye className="h-4 w-4" /> Preview & send
          </Link>
          <Button onClick={() => void save()} disabled={saving}>
            <Save className="h-4 w-4" /> {saving ? "Saving..." : "Save"}
          </Button>
        </div>
      </div>

      {error ? <p role="alert" className="rounded-lg bg-rose-50 p-3 text-sm text-rose-800">{error}</p> : null}

      <section className="grid gap-4 rounded-xl border border-slate-200 bg-white p-5 sm:grid-cols-2">
        <label className="text-sm font-medium text-slate-700">
          Subject
          <Input className="mt-1" value={subject} onChange={(event) => setSubject(event.target.value)} />
        </label>
        <label className="text-sm font-medium text-slate-700">
          Preview text
          <Input className="mt-1" value={previewText} onChange={(event) => setPreviewText(event.target.value)} />
        </label>
        <label className="text-sm font-medium text-slate-700">
          Sender name
          <Input className="mt-1" value={senderName} onChange={(event) => setSenderName(event.target.value)} />
        </label>
        <label className="text-sm font-medium text-slate-700">
          Sender email
          <Input className="mt-1" type="email" value={senderEmail} onChange={(event) => setSenderEmail(event.target.value)} />
        </label>
        <label className="text-sm font-medium text-slate-700">
          Sender postal address
          <Textarea className="mt-1" rows={3} value={senderPostalAddress} onChange={(event) => setSenderPostalAddress(event.target.value)} />
        </label>
        <label className="text-sm font-medium text-slate-700">
          Reply-to
          <Input className="mt-1" type="email" value={replyTo} onChange={(event) => setReplyTo(event.target.value)} />
        </label>
      </section>

      <EmailDesignEditor
        blocks={blocks}
        onChange={setBlocks}
        subject={subject}
        preheader={previewText}
      />
    </div>
  );
}
