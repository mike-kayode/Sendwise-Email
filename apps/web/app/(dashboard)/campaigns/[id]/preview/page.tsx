import Link from "next/link";
import type { Metadata } from "next";
import { ArrowLeft, Monitor, Send, Smartphone } from "lucide-react";
import { EmailPreview } from "@/components/email-preview";
import { PageHeader } from "@/components/page-header";
import { Card, CardHeader, CardTitle, CardDescription, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";

const campaigns = [
  { id: "cmp_01", name: "Spring launch", subject: "Launch day is here", audience: "Newsletter subscribers" },
  { id: "cmp_02", name: "Product digest", subject: "This week’s product updates", audience: "Product users" },
  { id: "cmp_03", name: "Lifecycle refresh", subject: "Your onboarding checklist", audience: "Trial accounts" },
];

export const metadata: Metadata = { title: "Preview campaign" };

export default async function CampaignPreviewPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  const campaign = campaigns.find((item) => item.id === id) ?? campaigns[0];

  return (
    <div>
      <Link href={`/campaigns/${campaign.id}/editor`} className="mb-4 inline-flex items-center gap-1 text-sm text-slate-500 hover:text-slate-900">
        <ArrowLeft className="h-4 w-4" /> Back to editor
      </Link>

      <PageHeader
        eyebrow={campaign.name}
        title="Preview & test"
        description="Check how the email renders and send a test to your inbox before scheduling."
        actions={
          <Link
            href={`/campaigns/${campaign.id}`}
            className="inline-flex h-9 items-center gap-1.5 rounded-lg bg-indigo-600 px-4 text-sm font-medium text-white shadow-sm hover:bg-indigo-500"
          >
            <Send className="h-4 w-4" /> Schedule campaign
          </Link>
        }
      />

      <div className="grid gap-5 lg:grid-cols-3">
        <div className="lg:col-span-2">
          <div className="mb-3 flex items-center justify-between">
            <div className="flex rounded-lg bg-white p-1 ring-1 ring-inset ring-slate-200">
              <span className="inline-flex items-center gap-1.5 rounded-md bg-slate-900 px-3 py-1.5 text-xs font-medium text-white">
                <Monitor className="h-3.5 w-3.5" /> Desktop
              </span>
              <span className="inline-flex items-center gap-1.5 rounded-md px-3 py-1.5 text-xs font-medium text-slate-600">
                <Smartphone className="h-3.5 w-3.5" /> Mobile
              </span>
            </div>
            <span className="text-xs text-slate-500">{campaign.audience}</span>
          </div>
          <div className="rounded-xl bg-slate-100 p-4 sm:p-8">
            <div className="mx-auto max-w-2xl">
              <EmailPreview subject={campaign.subject} />
            </div>
          </div>
        </div>

        <div className="space-y-5">
          <Card>
            <CardHeader>
              <CardTitle>Send a test</CardTitle>
              <CardDescription>Test sends use live merge data and tracking.</CardDescription>
            </CardHeader>
            <CardContent className="space-y-3">
              <Input type="email" defaultValue="kola@acme.studio" />
              <Button className="w-full">Send test email</Button>
              <p className="text-xs text-slate-500">
                Delivery may take up to a minute. Check spam if it does not arrive.
              </p>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>Pre-send checklist</CardTitle>
            </CardHeader>
            <CardContent className="space-y-2.5 text-sm">
              {[
                { label: "Subject line is set", done: true },
                { label: "Audience has 18,420 contacts", done: true },
                { label: "Unsubscribe link present", done: true },
                { label: "Sending domain verified", done: false },
                { label: "Plain-text version generated", done: true },
              ].map((item) => (
                <div key={item.label} className="flex items-center gap-2">
                  <span
                    className={
                      item.done
                        ? "flex h-4 w-4 items-center justify-center rounded-full bg-emerald-500 text-[9px] text-white"
                        : "flex h-4 w-4 items-center justify-center rounded-full bg-amber-400 text-[9px] text-white"
                    }
                  >
                    {item.done ? "✓" : "!"}
                  </span>
                  <span className={item.done ? "text-slate-600" : "text-amber-700"}>{item.label}</span>
                </div>
              ))}
              <Link href="/domains" className="block pt-1 text-xs font-medium text-indigo-600 hover:text-indigo-500">
                Review sending domains
              </Link>
            </CardContent>
          </Card>
        </div>
      </div>
    </div>
  );
}
