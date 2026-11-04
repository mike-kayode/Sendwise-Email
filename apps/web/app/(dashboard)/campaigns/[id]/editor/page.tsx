import Link from "next/link";
import type { Metadata } from "next";
import {
  AlignLeft,
  ArrowLeft,
  Eye,
  GripVertical,
  Image as ImageIcon,
  Minus,
  MousePointerClick,
  Plus,
  Send,
  Type,
} from "lucide-react";
import { EmailPreview } from "@/components/email-preview";
import { Card, CardHeader, CardTitle, CardDescription, CardContent } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input, Select } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";

const campaigns = [
  { id: "cmp_01", name: "Spring launch", subject: "Launch day is here", audience: "Newsletter subscribers" },
  { id: "cmp_02", name: "Product digest", subject: "This week’s product updates", audience: "Product users" },
  { id: "cmp_03", name: "Lifecycle refresh", subject: "Your onboarding checklist", audience: "Trial accounts" },
];

const templateBlocks = [
  { id: "b-1", type: "heading", content: "Hi there," },
  { id: "b-2", type: "paragraph", content: "Here’s what’s new this week." },
  { id: "b-3", type: "button", content: "Read now" },
  { id: "b-4", type: "divider", content: "" },
  { id: "b-5", type: "footer", content: "Thanks for reading," },
];

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
  const campaign = campaigns.find((item) => item.id === id) ?? campaigns[0];

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
              <Badge variant="neutral">Draft</Badge>
            </div>
            <p className="text-xs text-slate-500">Saved just now · all changes stored</p>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <Link
            href={`/campaigns/${campaign.id}/preview`}
            className="inline-flex h-9 items-center gap-1.5 rounded-lg bg-white px-4 text-sm font-medium text-slate-900 ring-1 ring-inset ring-slate-200 hover:bg-slate-50"
          >
            <Eye className="h-4 w-4" /> Preview
          </Link>
          <Link
            href={`/campaigns/${campaign.id}/preview`}
            className="inline-flex h-9 items-center gap-1.5 rounded-lg bg-indigo-600 px-4 text-sm font-medium text-white shadow-sm hover:bg-indigo-500"
          >
            <Send className="h-4 w-4" /> Continue
          </Link>
        </div>
      </div>

      <div className="grid gap-4 xl:grid-cols-[220px_minmax(0,1fr)_280px]">
        <Card className="h-fit">
          <CardHeader>
            <CardTitle>Blocks</CardTitle>
            <CardDescription>Drag to reorder</CardDescription>
          </CardHeader>
          <CardContent className="space-y-1.5">
            {templateBlocks.map((block, index) => {
              const Icon = blockIcons[block.type] ?? AlignLeft;
              return (
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
                  <Icon className="h-3.5 w-3.5 shrink-0" />
                  <span className="truncate capitalize">{block.type}</span>
                </div>
              );
            })}
            <div className="pt-2">
              <button className="flex w-full items-center justify-center gap-1.5 rounded-lg border border-dashed border-slate-300 py-2 text-xs font-medium text-slate-600 hover:border-indigo-300 hover:text-indigo-600">
                <Plus className="h-3.5 w-3.5" /> Add block
              </button>
            </div>
          </CardContent>
        </Card>

        <div>
          <div className="mb-3 grid gap-2 rounded-xl border border-slate-200 bg-white p-3 sm:grid-cols-2">
            <div>
              <label className="mb-1 block text-xs font-medium text-slate-500">Subject</label>
              <Input defaultValue={campaign.subject} />
            </div>
            <div>
              <label className="mb-1 block text-xs font-medium text-slate-500">Preview text</label>
              <Input defaultValue="Automations, faster imports, and a refreshed template library." />
            </div>
          </div>
          <div className="rounded-xl bg-slate-100 p-4 sm:p-8">
            <EmailPreview subject={campaign.subject} />
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
                <Select defaultValue="hello">
                  <option value="hello">Acme Studio &lt;hello@acme.studio&gt;</option>
                  <option value="news">Acme Studio &lt;news@acme.studio&gt;</option>
                </Select>
              </div>
              <div>
                <label className="mb-1 block text-xs font-medium text-slate-500">Reply-to</label>
                <Input defaultValue="support@acme.studio" />
              </div>
              <div>
                <label className="mb-1 block text-xs font-medium text-slate-500">Audience</label>
                <Select defaultValue="newsletter">
                  <option value="newsletter">Newsletter subscribers · 18,420</option>
                  <option value="customers">Customers · 6,420</option>
                  <option value="trials">Trial accounts · 1,280</option>
                </Select>
              </div>
              <div>
                <label className="mb-1 block text-xs font-medium text-slate-500">Tags</label>
                <Input defaultValue="newsletter, product" />
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
                <input type="checkbox" defaultChecked /> Track opens
              </label>
              <label className="flex items-center gap-2">
                <input type="checkbox" defaultChecked /> Track clicks
              </label>
              <label className="flex items-center gap-2">
                <input type="checkbox" defaultChecked /> Add unsubscribe link
              </label>
              <p className="pt-1 text-xs text-slate-500">
                Unsubscribe and one-click headers are always included for compliance.
              </p>
            </CardContent>
          </Card>

          <Button className="w-full">Save changes</Button>
        </div>
      </div>
    </div>
  );
}
