"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useQuery } from "@tanstack/react-query";
import { ArrowLeft, Check, Info } from "lucide-react";
import { PageHeader } from "@/components/page-header";
import { Card, CardHeader, CardTitle, CardDescription, CardContent } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input, Select } from "@/components/ui/input";
import { apiFetch, type Campaign, type SenderIdentity } from "@/lib/auth";
import { cn, formatNumber } from "@/lib/utils";
import { useToast } from "@/components/ui/use-toast";

type ContactList = {
  id: string;
  name: string;
  description: string;
  contact_count: number;
  created_at: string;
  updated_at: string;
};

type EmailTemplate = {
  id: string;
  name: string;
  design: unknown;
  sender_identity_id?: string;
};

const steps = [
  { number: 1, label: "Details", active: true },
  { number: 2, label: "Audience", active: false },
  { number: 3, label: "Content", active: false },
  { number: 4, label: "Review", active: false },
];

export default function NewCampaignPage() {
  const router = useRouter();
  const { toast } = useToast();

  const [name, setName] = useState("");
  const [subject, setSubject] = useState("");
  const [previewText, setPreviewText] = useState("");
  const [senderIdentityID, setSenderIdentityID] = useState("");
  const [listId, setSelectedListId] = useState<string | null>(null); // null → default to first list; "" → all contacts
  const [delivery, setDelivery] = useState<"draft" | "schedule">("draft");
  const [scheduledAt, setScheduledAt] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [templateID, setTemplateID] = useState("");

  useEffect(() => {
    setTemplateID(new URLSearchParams(window.location.search).get("template_id") ?? "");
  }, []);

  const templateQuery = useQuery({
    queryKey: ["template", templateID],
    queryFn: () => apiFetch<EmailTemplate>(`/templates/${templateID}`),
    enabled: Boolean(templateID),
  });

  useEffect(() => {
    if (templateQuery.data && !name) setName(`${templateQuery.data.name} campaign`);
  }, [templateQuery.data, name]);

  const listsQuery = useQuery({
    queryKey: ["lists"],
    queryFn: async () => {
      const res = await apiFetch<{ data: ContactList[] | null }>("/lists");
      return res.data ?? [];
    },
  });
  const identitiesQuery = useQuery({
    queryKey: ["sender-identities"],
    queryFn: async () => {
      const res = await apiFetch<{ data: SenderIdentity[] | null }>("/sender-identities");
      return res.data ?? [];
    },
  });
  const senderProfileQuery = useQuery({
    queryKey: ["sender-profile"],
    queryFn: () => apiFetch<{ complete: boolean }>("/sender-profile"),
  });

  const lists = listsQuery.data ?? [];
  const identities = identitiesQuery.data ?? [];
  const effectiveListId = listId === null ? (lists[0]?.id ?? "") : listId;
  const selectedSenderIdentityID =
    senderIdentityID ||
    templateQuery.data?.sender_identity_id ||
    identities.find((identity) => identity.is_default)?.id ||
    "";

  const canSubmit =
    name.trim().length > 0 &&
    !submitting &&
    Boolean(selectedSenderIdentityID) &&
    Boolean(senderProfileQuery.data?.complete) &&
    (!templateID || Boolean(templateQuery.data));
  const canSchedule =
    delivery === "schedule" && scheduledAt !== "" && new Date(scheduledAt).getTime() > Date.now();

  const createCampaign = async (): Promise<Campaign> => {
    const body: Record<string, unknown> = {
      name: name.trim(),
      subject: subject.trim(),
      preview_text: previewText.trim(),
      sender_identity_id: selectedSenderIdentityID,
    };
    if (templateID && templateQuery.data) {
      body.template_id = templateID;
      body.content = templateQuery.data.design;
    }
    if (effectiveListId) body.list_id = effectiveListId;
    return apiFetch<Campaign>("/campaigns", { method: "POST", body: JSON.stringify(body) });
  };

  const handleSaveDraft = async () => {
    if (!canSubmit) return;
    setSubmitting(true);
    try {
      const campaign = await createCampaign();
      if (delivery === "schedule" && canSchedule) {
        await apiFetch(`/campaigns/${campaign.id}/schedule`, {
          method: "POST",
          body: JSON.stringify({ scheduled_at: new Date(scheduledAt).toISOString() }),
        });
        toast({ title: "Campaign scheduled", description: `${campaign.name} will send at ${new Date(scheduledAt).toLocaleString()}.` });
        router.push("/campaigns");
        return;
      }
      toast({ title: "Draft saved", description: `${campaign.name} was created as a draft.` });
      router.push("/campaigns");
    } catch (err: any) {
      toast({ title: "Could not create campaign", description: err?.message ?? "Unknown error", variant: "destructive" });
    } finally {
      setSubmitting(false);
    }
  };

  const handleContinueToEditor = async () => {
    if (!canSubmit) return;
    setSubmitting(true);
    try {
      const campaign = await createCampaign();
      toast({ title: "Campaign created", description: "Add your content in the editor." });
      router.push(`/campaigns/${campaign.id}/editor`);
    } catch (err: any) {
      toast({ title: "Could not create campaign", description: err?.message ?? "Unknown error", variant: "destructive" });
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="mx-auto max-w-4xl">
      <Link href="/campaigns" className="mb-4 inline-flex items-center gap-1 text-sm text-slate-500 hover:text-slate-900">
        <ArrowLeft className="h-4 w-4" /> Back to campaigns
      </Link>

      <PageHeader
        title="Create a campaign"
        description="Set up the basics now. You can refine the content and audience before sending."
      />
      {templateID ? (
        <div className="mb-5 rounded-lg border border-indigo-100 bg-indigo-50 p-3 text-sm text-indigo-900">
          {templateQuery.isLoading
            ? "Loading selected template..."
            : templateQuery.error
              ? `Unable to load template: ${templateQuery.error.message}`
              : `Using template: ${templateQuery.data?.name ?? "Selected template"}`}
        </div>
      ) : null}

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
              <Input
                id="name"
                placeholder="e.g. November product digest"
                value={name}
                onChange={(e) => setName(e.target.value)}
              />
              <p className="mt-1 text-xs text-slate-500">Only your team will see this.</p>
            </div>
            <div>
              <label htmlFor="subject" className="mb-1.5 block text-sm font-medium text-slate-700">
                Subject line
              </label>
              <Input
                id="subject"
                placeholder="New: automations, segments, and faster imports"
                value={subject}
                onChange={(e) => setSubject(e.target.value)}
              />
              <p className="mt-1 flex items-center gap-1 text-xs text-slate-500">
                <Info className="h-3 w-3" /> Keep it under 50 characters for best open rates.
              </p>
            </div>
            <div>
              <label htmlFor="preheader" className="mb-1.5 block text-sm font-medium text-slate-700">
                Preview text
              </label>
              <Input
                id="preheader"
                placeholder="A short summary shown next to the subject."
                value={previewText}
                onChange={(e) => setPreviewText(e.target.value)}
              />
            </div>
            <div className="grid gap-4 sm:grid-cols-2">
              <div>
                <label htmlFor="sender-identity" className="mb-1.5 block text-sm font-medium text-slate-700">
                  From identity
                </label>
                <Select
                  id="sender-identity"
                  value={selectedSenderIdentityID}
                  onChange={(event) => setSenderIdentityID(event.target.value)}
                  disabled={identitiesQuery.isLoading || identities.length === 0}
                >
                  <option value="">Choose a verified sender</option>
                  {identities.filter((identity) => identity.verified).map((identity) => (
                    <option key={identity.id} value={identity.id}>
                      {identity.display_name} &lt;{identity.email}&gt;{identity.is_default ? " — default" : ""}
                    </option>
                  ))}
                </Select>
                {identities.length === 0 ? (
                  <Link href="/settings#sending" className="mt-1 block text-xs font-medium text-indigo-600 hover:text-indigo-500">
                    Create a sender identity in Settings
                  </Link>
                ) : null}
                {!senderProfileQuery.data?.complete ? (
                  <Link href="/settings#sender-profile" className="mt-2 block text-xs font-medium text-amber-700 hover:text-amber-800">
                    Complete the sender profile in Settings before continuing
                  </Link>
                ) : null}
              </div>
            </div>
            <div>
              <p className="text-xs text-slate-500">
                Postal address, support contact, and unsubscribe details are added from your sender profile.
              </p>
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
              {listsQuery.isLoading ? (
                <p className="text-sm text-slate-500">Loading lists...</p>
              ) : lists.length === 0 ? (
                <div className="text-sm text-slate-500">
                  <p>No lists yet.</p>
                  <Link href="/audience/lists" className="mt-1 block text-xs font-medium text-indigo-600 hover:text-indigo-500">
                    Create an audience list
                  </Link>
                </div>
              ) : (
                lists.slice(0, 5).map((list) => (
                  <label
                    key={list.id}
                    className={cn(
                      "flex cursor-pointer items-start gap-3 rounded-lg border p-3",
                      effectiveListId === list.id
                        ? "border-indigo-200 bg-indigo-50/50"
                        : "border-slate-200 hover:bg-slate-50",
                    )}
                  >
                    <input
                      type="radio"
                      name="audience"
                      checked={effectiveListId === list.id}
                      onChange={() => setSelectedListId(list.id)}
                      className="mt-1"
                    />
                    <span>
                      <span className="block text-sm font-medium text-slate-900">{list.name}</span>
                      <span className="block text-xs text-slate-500">{formatNumber(list.contact_count)} contacts</span>
                    </span>
                  </label>
                ))
              )}
              <button
                type="button"
                onClick={() => setSelectedListId("")}
                className={cn(
                  "block w-full rounded-lg border p-3 text-left text-sm",
                  effectiveListId === ""
                    ? "border-indigo-200 bg-indigo-50/50 font-medium text-slate-900"
                    : "border-slate-200 text-slate-600 hover:bg-slate-50",
                )}
              >
                All subscribed contacts
                <span className="block text-xs font-normal text-slate-500">Send to everyone who is subscribed</span>
              </button>
              <Link href="/audience/segments" className="block pt-1 text-xs font-medium text-indigo-600 hover:text-indigo-500">
                Use a saved segment instead
              </Link>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>Delivery</CardTitle>
              <CardDescription>Start as a draft or schedule the send.</CardDescription>
            </CardHeader>
            <CardContent className="space-y-3">
              <label className="flex items-center gap-2 text-sm text-slate-700">
                <input
                  type="radio"
                  name="delivery"
                  checked={delivery === "draft"}
                  onChange={() => setDelivery("draft")}
                />{" "}
                Draft — finish in the editor
              </label>
              <label className="flex items-center gap-2 text-sm text-slate-700">
                <input
                  type="radio"
                  name="delivery"
                  checked={delivery === "schedule"}
                  onChange={() => setDelivery("schedule")}
                />{" "}
                Schedule for later
              </label>
              <Input
                type="datetime-local"
                disabled={delivery !== "schedule"}
                value={scheduledAt}
                onChange={(e) => setScheduledAt(e.target.value)}
              />
              <p className="text-xs text-slate-500">Times shown in your local timezone.</p>
            </CardContent>
          </Card>
        </div>
      </div>

      <div className="mt-6 flex items-center justify-between">
        <Link href="/campaigns" className="text-sm font-medium text-slate-600 hover:text-slate-900">
          Cancel
        </Link>
        <div className="flex gap-2">
          <Button variant="secondary" onClick={handleSaveDraft} disabled={!canSubmit}>
            {delivery === "schedule" && canSchedule ? "Create & schedule" : "Save draft"}
          </Button>
          <Button onClick={handleContinueToEditor} disabled={!canSubmit}>
            Continue to editor
          </Button>
        </div>
      </div>
    </div>
  );
}
