import Link from "next/link";
import type { Metadata } from "next";
import { ArrowLeft, Check, Info } from "lucide-react";
import { PageHeader } from "@/components/page-header";
import { Card, CardHeader, CardTitle, CardDescription, CardContent } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input, Select, Textarea } from "@/components/ui/input";
import { cn } from "@/lib/utils";

const lists = [
  { id: "list-1", name: "Newsletter subscribers", contacts: 18420 },
  { id: "list-2", name: "Product users", contacts: 6420 },
  { id: "list-3", name: "Trial accounts", contacts: 1280 },
];

export const metadata: Metadata = { title: "New campaign" };

const steps = [
  { number: 1, label: "Details", active: true },
  { number: 2, label: "Audience", active: false },
  { number: 3, label: "Content", active: false },
  { number: 4, label: "Review", active: false },
];

export default function NewCampaignPage() {
  return (
    <div className="mx-auto max-w-4xl">
      <Link href="/campaigns" className="mb-4 inline-flex items-center gap-1 text-sm text-slate-500 hover:text-slate-900">
        <ArrowLeft className="h-4 w-4" /> Back to campaigns
      </Link>

      <PageHeader
        title="Create a campaign"
        description="Set up the basics now. You can refine the content and audience before sending."
      />

      <ol className="mb-6 flex items-center gap-2 text-sm">
        {steps.map((step, index) => (
          <li key={step.number} className="flex flex-1 items-center gap-2">
            <span
              className={cn(
                "flex h-7 w-7 shrink-0 items-center justify-center rounded-full text-xs font-semibold",
                step.active
                  ? "bg-indigo-600 text-white"
                  : "bg-white text-slate-400 ring-1 ring-inset ring-slate-200",
              )}
            >
              {step.active ? step.number : <Check className="h-3.5 w-3.5" />}
            </span>
            <span className={cn("hidden sm:inline", step.active ? "font-medium text-slate-900" : "text-slate-500")}>
              {step.label}
            </span>
            {index < steps.length - 1 ? <span className="h-px flex-1 bg-slate-200" /> : null}
          </li>
        ))}
      </ol>

      <div className="grid gap-5 lg:grid-cols-3">
        <Card className="lg:col-span-2">
          <CardHeader>
            <CardTitle>Campaign details</CardTitle>
            <CardDescription>Name your campaign and write the subject line.</CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <div>
              <label htmlFor="name" className="mb-1.5 block text-sm font-medium text-slate-700">
                Internal name
              </label>
              <Input id="name" placeholder="e.g. November product digest" defaultValue="" />
              <p className="mt-1 text-xs text-slate-500">Only your team will see this.</p>
            </div>
            <div>
              <label htmlFor="subject" className="mb-1.5 block text-sm font-medium text-slate-700">
                Subject line
              </label>
              <Input id="subject" placeholder="New: automations, segments, and faster imports" />
              <p className="mt-1 flex items-center gap-1 text-xs text-slate-500">
                <Info className="h-3 w-3" /> Keep it under 50 characters for best open rates.
              </p>
            </div>
            <div>
              <label htmlFor="preheader" className="mb-1.5 block text-sm font-medium text-slate-700">
                Preview text
              </label>
              <Input id="preheader" placeholder="A short summary shown next to the subject." />
            </div>
            <div>
              <label htmlFor="sender" className="mb-1.5 block text-sm font-medium text-slate-700">
                Sender
              </label>
              <Select id="sender" defaultValue="hello@acme.studio">
                <option value="hello@acme.studio">Acme Studio &lt;hello@acme.studio&gt;</option>
                <option value="news@acme.studio">Acme Studio &lt;news@acme.studio&gt;</option>
                <option value="kola@acme.studio">Kola &lt;kola@acme.studio&gt;</option>
              </Select>
            </div>
            <div>
              <label htmlFor="notes" className="mb-1.5 block text-sm font-medium text-slate-700">
                Internal notes <span className="font-normal text-slate-400">(optional)</span>
              </label>
              <Textarea id="notes" rows={3} placeholder="Context for your team..." />
            </div>
          </CardContent>
        </Card>

        <div className="space-y-5">
          <Card>
            <CardHeader>
              <CardTitle>Audience</CardTitle>
              <CardDescription>Who should receive this?</CardDescription>
            </CardHeader>
            <CardContent className="space-y-2">
              {lists.slice(0, 3).map((list, index) => (
                <label
                  key={list.id}
                  className={cn(
                    "flex cursor-pointer items-start gap-3 rounded-lg border p-3",
                    index === 0 ? "border-indigo-200 bg-indigo-50/50" : "border-slate-200 hover:bg-slate-50",
                  )}
                >
                  <input type="radio" name="audience" defaultChecked={index === 0} className="mt-1" />
                  <span>
                    <span className="block text-sm font-medium text-slate-900">{list.name}</span>
                    <span className="block text-xs text-slate-500">{list.contacts.toLocaleString()} contacts</span>
                  </span>
                </label>
              ))}
              <Link href="/audience/segments" className="block pt-1 text-xs font-medium text-indigo-600 hover:text-indigo-500">
                Use a saved segment instead
              </Link>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>Delivery</CardTitle>
              <CardDescription>Send now or schedule it.</CardDescription>
            </CardHeader>
            <CardContent className="space-y-3">
              <label className="flex items-center gap-2 text-sm text-slate-700">
                <input type="radio" name="delivery" defaultChecked /> Send immediately
              </label>
              <label className="flex items-center gap-2 text-sm text-slate-700">
                <input type="radio" name="delivery" /> Schedule
              </label>
              <Input type="datetime-local" disabled />
              <p className="text-xs text-slate-500">Times shown in your local timezone (WAT).</p>
            </CardContent>
          </Card>
        </div>
      </div>

      <div className="mt-6 flex items-center justify-between">
        <Link href="/campaigns" className="text-sm font-medium text-slate-600 hover:text-slate-900">
          Cancel
        </Link>
        <div className="flex gap-2">
          <Button variant="secondary">Save draft</Button>
          <Link
            href="/campaigns/cmp_05/editor"
            className="inline-flex h-9 items-center gap-1.5 rounded-lg bg-indigo-600 px-4 text-sm font-medium text-white shadow-sm hover:bg-indigo-500"
          >
            Continue to editor
          </Link>
        </div>
      </div>
    </div>
  );
}
