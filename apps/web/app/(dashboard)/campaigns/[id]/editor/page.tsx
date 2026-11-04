"use client";

import Link from "next/link";
import type { Metadata } from "next";
import { useEffect, useState } from "react";
import { PageHeader } from "@/components/page-header";
import { AlignLeft, ArrowLeft, Eye, GripVertical, Image as ImageIcon, Minus, MousePointerClick, Plus, Send, Type } from "lucide-react";
import { EmailPreview } from "@/components/email-preview";
import { Card, CardHeader, CardTitle, CardDescription, CardContent } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input, Select } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";
import { apiFetch, type Campaign } from "@/lib/auth";
import { useToast } from "@/components/ui/use-toast";

export const metadata: Metadata = { title: "Campaign editor" };

const blockIcons: Record<string, React.ComponentType<{ className?: string }>> = {
  heading: Type,
  paragraph: AlignLeft,
  image: ImageIcon,
  button: MousePointerClick,
  divider: Minus,
  spacer: Minus,
  footer: AlignLeft,
};

export default async function CampaignEditorPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  const [campaign, setCampaign] = useState<Campaign | null>(null);
  const [blocks, setBlocks] = useState<any[]>([]);
  const [subject, setSubject] = useState("");
  const [previewText, setPreviewText] = useState("");
  const [senderName, setSenderName] = useState("");
  const [senderEmail, setSenderEmail] = useState("");
  const [replyTo, setReplyTo] = useState("");
  const [audience, setAudience] = useState("");
  const [tags, setTags] = useState("");
  const [trackOpens, setTrackOpens] = useState(true);
  const [trackClicks, setTrackClicks] = useState(true);
  const [addUnsubscribe, setAddUnsubscribe] = useState(true);
  const [saving, setSaving] = useState(false);
  const [loading, setLoading] = useState(true);
  const toast = useToast();

  useEffect(() => {
    loadCampaign();
  }, [id]);

  async function loadCampaign() {
    try {
      const c = await apiFetch<Campaign>(`/campaigns/${id}`);
      setCampaign(c);
      const parsed = typeof c.content === "string" ? JSON.parse(c.content) : c.content ?? {};
      setBlocks(parsed.blocks ?? []);
      setSubject(c.subject ?? "");
      setPreviewText(c.preview_text ?? "");
      setSenderName(c.sender_name ?? "");
      setSenderEmail(c.sender_email ?? "");
      setReplyTo(c.reply_to ?? "");
      setAudience(c.list_id ?? "");
      setTags("");
      setTrackOpens(c.track_opens ?? true);
      setTrackClicks(c.track_clicks ?? true);
      setAddUnsubscribe(true);
    } catch (err: any) {
      toast.toast({ title: "Unable to load campaign", description: err?.message ?? "Unknown error", variant: "destructive" });
    } finally {
      setLoading(false);
    }
  }

  async function handleSave() {
    if (!campaign) return;
    setSaving(true);
    try {
      const content = { version: 1, blocks };
      await apiFetch(`/campaigns/${campaign.id}`, {
        method: "PATCH",
        body: JSON.stringify({
          name: campaign.name,
          subject,
          preview_text: previewText,
          sender_name: senderName,
          sender_email: senderEmail,
          reply_to: replyTo,
          content,
          track_opens: trackOpens,
          track_clicks: trackClicks,
        }),
      });
      toast.toast({ title: "Saved", description: "Campaign changes saved." });
    } catch (err: any) {
      toast.toast({ title: "Failed to save", description: err?.message ?? "Unknown error", variant: "destructive" });
    } finally {
      setSaving(false);
    }
  }

  if (loading) {
    return (
      <div>
        <PageHeader title="Campaign editor" description="Loading campaign..." />
      </div>
    );
  }

  if (!campaign) {
    return (
      <div>
        <PageHeader title="Campaign editor" description="Campaign not found." />
      </div>
    );
  }

  return (
    <div>
      <div className="mb-4 flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
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
          <button
            onClick={handleSave}
            disabled={saving}
            className="inline-flex h-9 items-center gap-1.5 rounded-lg bg-indigo-600 px-4 text-sm font-medium text-white shadow-sm hover:bg-indigo-500 disabled:opacity-60"
          >
            {saving ? "Saving..." : <><Send className="h-4 w-4" /> Save</>}
          </button>
        </div>
      </div>

      <div className="grid gap-4 xl:grid-cols-[220px_minmax(0,1fr)_280px]">
        <Card className="h-fit">
          <CardHeader>
            <CardTitle>Blocks</CardTitle>
            <CardDescription>Drag to reorder</CardDescription>
          </CardHeader>
          <CardContent className="space-y-1.5">
            {blocks.map((block, index) => (
              <div
                key={block.id}
                className={cn(
                  "flex items-center gap-2 rounded-lg border px-2.5 py-2 text-sm",
                  index === 0
                    ? "border-indigo-200 bg-indigo-50 text-indigo-900"
                    : "border-transparent text-slate-600 hover:border-slate-200 hover:bg-slate-50",
                )}
              >
                <GripVertical className="h-3.5 w-3.5 shrink-0 text-slate-400" />
                <span className="truncate capitalize">{block.type}</span>
                <button
                  onClick={() => setBlocks(blocks.filter((_, i) => i !== index))}
                  className="rounded p-1 text-slate-400 hover:bg-slate-100 hover:text-slate-700 ml-auto"
                  aria-label="Remove block"
                >
                  <Minus className="h-3.5 w-3.5" />
                </button>
              </div>
            ))}
            <div className="pt-2">
              <button
                className="flex w-full items-center justify-center gap-1.5 rounded-lg border border-dashed border-slate-300 py-2 text-xs font-medium text-slate-600 hover:border-indigo-300 hover:text-indigo-600"
                onClick={() => setBlocks([...blocks, { id: Date.now().toString(), type: "paragraph", content: "New paragraph", align: "left" }])}
              >
                <Plus className="h-3.5 w-3.5" /> Add block
              </button>
            </div>
          </CardContent>
        </Card>

        <div>
          <div className="mb-3 grid gap-2 rounded-xl border border-slate-200 bg-white p-3 sm:grid-cols-2">
            <div>
              <label className="mb-1 block text-xs font-medium text-slate-500">Subject</label>
              <Input value={subject} onChange={(e) => setSubject(e.target.value)} />
            </div>
            <div>
              <label className="mb-1 block text-xs font-medium text-slate-500">Preview text</label>
              <Input value={previewText} onChange={(e) => setPreviewText(e.target.value)} />
            </div>
          </div>
          <div className="rounded-xl bg-slate-100 p-4 sm:p-8">
            <EmailPreview subject={subject} />
          </div>
        </div>

        <div className="space-y-4">
          <Card>
            <CardHeader>
              <CardTitle>Settings</CardTitle>
            </CardHeader>
            <CardContent className="space-y-3">
              <div>
                <label className="mb-1 block text-xs font-medium text-slate-500">Sender</label>
                <Select value={senderName} onChange={(e) => setSenderName(e.target.value)}>
                  <option value={senderName}>{senderName} at {senderEmail}</option>
                </Select>
              </div>
              <div>
                <label className="mb-1 block text-xs font-medium text-slate-500">Reply-to</label>
                <Input value={replyTo} onChange={(e) => setReplyTo(e.target.value)} placeholder="support@yourdomain.com" />
              </div>
              <div>
                <label className="mb-1 block text-xs font-medium text-slate-500">Audience</label>
                <Select value={audience} onChange={(e) => setAudience(e.target.value)}>
                  <option value="">Select audience</option>
                </Select>
              </div>
              <div>
                <label className="mb-1 block text-xs font-medium text-slate-500">Tags</label>
                <Input value={tags} onChange={(e) => setTags(e.target.value)} placeholder="newsletter, product" />
              </div>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>Tracking</CardTitle>
              <CardDescription>Applied when the campaign sends</CardDescription>
            </CardHeader>
            <CardContent className="space-y-2.5 text-sm text-slate-700">
              <label className="flex items-center gap-2">
                <input type="checkbox" checked={trackOpens} onChange={(e) => setTrackOpens(e.target.checked)} /> Track opens
              </label>
              <label className="flex items-center gap-2">
                <input type="checkbox" checked={trackClicks} onChange={(e) => setTrackClicks(e.target.checked)} /> Track clicks
              </label>
              <label className="flex items-center gap-2">
                <input type="checkbox" checked={addUnsubscribe} onChange={(e) => setAddUnsubscribe(e.target.checked)} /> Add unsubscribe link
              </label>
              <p className="pt-1 text-xs text-slate-500">
                Unsubscribe and one-click headers are always included for compliance.
              </p>
            </CardContent>
          </Card>

          <Button onClick={handleSave} disabled={saving} className="w-full">
            {saving ? "Saving..." : "Save changes"}
          </Button>
        </div>
      </div>
    </div>
  );
}
