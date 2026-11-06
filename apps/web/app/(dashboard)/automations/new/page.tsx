"use client";

import { useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useQuery } from "@tanstack/react-query";
import { ArrowLeft, Sparkles } from "lucide-react";
import { PageHeader } from "@/components/page-header";
import { Card, CardHeader, CardTitle, CardDescription, CardContent } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { apiFetch } from "@/lib/auth";
import { useToast } from "@/components/ui/use-toast";

type ContactList = { id: string; name: string; description?: string };
type Template = { id: string; name: string; category?: string };

export default function NewAutomationPage() {
  const router = useRouter();
  const { toast } = useToast();
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [listId, setListId] = useState("");
  const [templateId, setTemplateId] = useState("");
  const [triggerType, setTriggerType] = useState("contact_added_to_list");
  const [submitting, setSubmitting] = useState(false);

  const listsQuery = useQuery({
    queryKey: ["lists"],
    queryFn: async () => {
      const res = await apiFetch<{ data: ContactList[] | null }>("/lists");
      return res.data ?? [];
    },
  });

  const templatesQuery = useQuery({
    queryKey: ["templates"],
    queryFn: async () => {
      const res = await apiFetch<{ data: Template[] | null }>("/templates");
      return res.data ?? [];
    },
  });

  const lists = listsQuery.data ?? [];
  const templates = templatesQuery.data ?? [];

  const handleCreate = async () => {
    if (!name.trim()) {
      toast({ title: "Name required", description: "Give your automation a name.", variant: "destructive" });
      return;
    }
    if (!templateId) {
      toast({ title: "Template required", description: "Choose a template for the email step.", variant: "destructive" });
      return;
    }

    setSubmitting(true);
    try {
      const trigger: Record<string, unknown> = { type: triggerType };
      if (triggerType === "contact_added_to_list" && listId) trigger.list_id = listId;

      const body = {
        name: name.trim(),
        description: description.trim(),
        status: "draft",
        trigger,
        steps: [
          {
            position: 0,
            step_type: "send_email",
            config: {
              template_id: templateId,
            },
          },
        ],
      };

      const automation = await apiFetch<{ id: string }>("/automations", {
        method: "POST",
        body: JSON.stringify(body),
      });

      toast({ title: "Automation created", description: `${name.trim()} is ready to activate.` });
      router.push(`/automations/${automation.id}`);
    } catch (err: any) {
      toast({ title: "Could not create automation", description: err?.message ?? "Unknown error", variant: "destructive" });
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="mx-auto max-w-3xl">
      <Link href="/automations" className="mb-4 inline-flex items-center gap-1 text-sm text-slate-500 hover:text-slate-900">
        <ArrowLeft className="h-4 w-4" /> Back to automations
      </Link>

      <PageHeader
        eyebrow="Marketing"
        title="New automation"
        description="Set up a simple trigger and a single email step to get started."
      />

      <Card>
        <CardHeader>
          <CardTitle>Automation basics</CardTitle>
          <CardDescription>Build a minimal real automation flow that sends one email when a contact matches the trigger.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-6">
          <div>
            <label htmlFor="name" className="mb-1.5 block text-sm font-medium text-slate-700">Name</label>
            <input
              id="name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              className="w-full rounded-md border border-slate-200 bg-white px-3 py-2 text-sm text-slate-900 shadow-sm outline-none ring-0 focus:border-indigo-500"
              placeholder="Welcome sequence"
            />
          </div>

          <div>
            <label htmlFor="description" className="mb-1.5 block text-sm font-medium text-slate-700">Description</label>
            <textarea
              id="description"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              className="min-h-24 w-full rounded-md border border-slate-200 bg-white px-3 py-2 text-sm text-slate-900 shadow-sm outline-none ring-0 focus:border-indigo-500"
              placeholder="Triggered when a contact joins a list..."
            />
          </div>

          <div className="grid gap-4 md:grid-cols-2">
            <div>
              <label htmlFor="trigger" className="mb-1.5 block text-sm font-medium text-slate-700">Trigger</label>
              <select
                id="trigger"
                value={triggerType}
                onChange={(e) => setTriggerType(e.target.value)}
                className="w-full rounded-md border border-slate-200 bg-white px-3 py-2 text-sm text-slate-900 shadow-sm outline-none ring-0 focus:border-indigo-500"
              >
                <option value="contact_added_to_list">Contact added to list</option>
              </select>
            </div>

            <div>
              <label htmlFor="list" className="mb-1.5 block text-sm font-medium text-slate-700">List</label>
              <select
                id="list"
                value={listId}
                onChange={(e) => setListId(e.target.value)}
                className="w-full rounded-md border border-slate-200 bg-white px-3 py-2 text-sm text-slate-900 shadow-sm outline-none ring-0 focus:border-indigo-500"
              >
                <option value="">Select a list</option>
                {lists.map((list) => (
                  <option key={list.id} value={list.id}>{list.name}</option>
                ))}
              </select>
            </div>
          </div>

          <div>
            <label htmlFor="template" className="mb-1.5 block text-sm font-medium text-slate-700">Email template</label>
            <select
              id="template"
              value={templateId}
              onChange={(e) => setTemplateId(e.target.value)}
              className="w-full rounded-md border border-slate-200 bg-white px-3 py-2 text-sm text-slate-900 shadow-sm outline-none ring-0 focus:border-indigo-500"
            >
              <option value="">Choose a template</option>
              {templates.map((template) => (
                <option key={template.id} value={template.id}>{template.name}</option>
              ))}
            </select>
          </div>

          <div className="rounded-lg border border-indigo-100 bg-indigo-50 p-3 text-sm text-indigo-900">
            <div className="mb-1 flex items-center gap-2 font-medium">
              <Sparkles className="h-4 w-4" /> Ready to send
            </div>
            <p className="text-indigo-700">This automation will create a draft flow; you can activate it from the detail page after it is created.</p>
          </div>

          <div className="flex items-center justify-end gap-3">
            <Link href="/automations" className="text-sm font-medium text-slate-600 hover:text-slate-900">Cancel</Link>
            <Button onClick={handleCreate} disabled={submitting || !name.trim() || !templateId}>
              {submitting ? "Creating..." : "Create automation"}
            </Button>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}
