"use client";

import { useState } from "react";
import { Send } from "lucide-react";
import Link from "next/link";
import { ArrowLeft } from "lucide-react";
import { apiFetch, type Campaign } from "@/lib/auth";
import { Card, CardHeader, CardTitle, CardDescription, CardContent } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";

type Block = {
  id: string;
  type: string;
  content: string;
  align: string;
};

type EditorFormProps = {
  campaignId: string;
  initialSubject: string;
  initialPreviewText: string;
  initialSenderName: string;
  initialSenderEmail: string;
  initialSenderPostalAddress: string;
  initialReplyTo: string;
  initialAudience: string;
  initialTags: string;
  initialBlocks: Block[];
};

export default function EditorForm({
  campaignId,
  initialSubject,
  initialPreviewText,
  initialSenderName,
  initialSenderEmail,
  initialSenderPostalAddress,
  initialReplyTo,
  initialAudience,
  initialTags,
  initialBlocks,
}: EditorFormProps) {
  const [campaign, setCampaign] = useState<Campaign | null>(null);
  const [blocks, setBlocks] = useState<Block[]>(initialBlocks);
  const [subject, setSubject] = useState(initialSubject);
  const [previewText, setPreviewText] = useState(initialPreviewText);
  const [senderName, setSenderName] = useState(initialSenderName);
  const [senderEmail, setSenderEmail] = useState(initialSenderEmail);
  const [senderPostalAddress, setSenderPostalAddress] = useState(initialSenderPostalAddress);
  const [replyTo, setReplyTo] = useState(initialReplyTo);
  const [audience, setAudience] = useState(initialAudience);
  const [tags, setTags] = useState(initialTags);
  const [trackOpens, setTrackOpens] = useState(true);
  const [trackClicks, setTrackClicks] = useState(true);
  const [addUnsubscribe, setAddUnsubscribe] = useState(true);
  const [saving, setSaving] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const loadCampaign = async () => {
    try {
      const res = await apiFetch<Campaign>(`/campaigns/${campaignId}`);
      const parsed = typeof res.content === "string" ? JSON.parse(res.content) : res.content ?? {};
      setCampaign({ ...res, content: parsed });
      setSubject(res.subject ?? "");
      setPreviewText(parsed.preview_text ?? "");
      setSenderName(res.sender_name ?? "");
      setSenderEmail(res.sender_email ?? "");
      setSenderPostalAddress(res.sender_postal_address ?? "");
      setReplyTo(res.reply_to ?? "");
      setAudience(res.list_id ?? "");
      setBlocks(parsed.blocks ?? []);
      setError(null);
    } catch (err: any) {
      setError(err?.message ?? "Failed to load campaign");
    } finally {
      setLoading(false);
    }
  };

  const handleSave = async () => {
    if (saving) return;
    setSaving(true);
    setError(null);
    try {
      const content = { version: 1, blocks };
      await apiFetch(`/campaigns/${campaignId}`, {
        method: "PATCH",
        body: JSON.stringify({
          name: campaign?.name ?? "",
          subject,
          preview_text: previewText,
          sender_name: senderName,
          sender_email: senderEmail,
          sender_postal_address: senderPostalAddress,
          reply_to: replyTo,
          content,
          track_opens: trackOpens,
          track_clicks: trackClicks,
        }),
      });
      setError(null);
    } catch (err: any) {
      setError(err?.message ?? "Failed to save campaign");
    } finally {
      setSaving(false);
    }
  };

  if (loading) {
    return <div className="p-4 text-sm text-slate-500">Loading campaign...</div>;
  }

  if (error) {
    return <div className="p-4 text-sm text-rose-600">{error}</div>;
  }

  if (!campaign) {
    return <div className="p-4 text-sm text-slate-500">Campaign not found</div>;
  }

  return (
    <div className="space-y-3">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div className="flex items-center gap-3">
          <Link href={`/campaigns/${campaign.id}`} className="rounded-lg p-1.5 text-slate-500 hover:bg-slate-100">
            <ArrowLeft className="h-4 w-4" />
          </Link>
          <div>
            <div className="flex items-center gap-2">
              <h1 className="text-lg font-semibold text-slate-900">{campaign.name}</h1>
              <Badge variant="neutral">{campaign.status}</Badge>
            </div>
            <p className="text-xs text-slate-500">Saved just now · all changes stored</p>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <Button onClick={handleSave} disabled={saving} className="w-full sm:w-auto">
            {saving ? "Saving..." : <><Send className="h-4 w-4" /> Save</>}
          </Button>
        </div>
      </div>
      <label className="block text-xs font-medium text-slate-600">
        Sender postal address
        <Input
          className="mt-1"
          value={senderPostalAddress}
          onChange={(event) => setSenderPostalAddress(event.target.value)}
          placeholder="Your organization's postal address"
        />
      </label>
    </div>
  );
}
