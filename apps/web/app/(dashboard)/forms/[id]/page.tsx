"use client";

import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import Link from "next/link";
import { useParams } from "next/navigation";
import { ArrowLeft, Eye, Plus, Save, Trash2 } from "lucide-react";
import { PageHeader } from "@/components/page-header";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { apiFetch } from "@/lib/auth";

type FormField = {
  id?: string;
  field_type: string;
  label: string;
  placeholder?: string;
  required: boolean;
  position: number;
  custom_key?: string;
  options?: unknown;
};

type FormDetails = {
  id: string;
  name: string;
  slug: string;
  description: string;
  status: string;
  list_id: string;
  success_message: string;
  redirect_url: string;
  consent_text: string;
  spam_protection: boolean;
  double_opt_in: boolean;
  fields: FormField[];
  tag_ids: string[];
};

type AudienceList = { id: string; name: string };
type AudienceTag = { id: string; name: string; color: string };

const fieldTypes: [string, string][] = [
  ["email", "Email address"],
  ["first_name", "First name"],
  ["last_name", "Last name"],
  ["phone", "Phone"],
  ["company", "Company"],
  ["country", "Country"],
  ["custom", "Custom text"],
];

const inputClass = "mt-1 block w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm text-slate-900 shadow-sm focus:border-indigo-500 focus:outline-none focus:ring-2 focus:ring-indigo-100";

function formPayload(form: FormDetails) {
  return {
    name: form.name,
    description: form.description,
    list_id: form.list_id,
    success_message: form.success_message,
    redirect_url: form.redirect_url,
    consent_text: form.consent_text,
    spam_protection: form.spam_protection,
    double_opt_in: form.double_opt_in,
    tag_ids: form.tag_ids,
    fields: form.fields.map((field, position) => ({
      field_type: field.field_type,
      label: field.label,
      placeholder: field.placeholder ?? "",
      required: field.required,
      position,
      custom_key: field.custom_key ?? "",
      options: field.options ?? [],
    })),
  };
}

export default function FormEditorPage() {
  const { id } = useParams<{ id: string }>();
  const queryClient = useQueryClient();
  const [draft, setDraft] = useState<FormDetails | null>(null);
  const [error, setError] = useState("");
  const formQuery = useQuery({
    queryKey: ["form", id],
    queryFn: () => apiFetch<FormDetails>(`/forms/${id}`),
    enabled: Boolean(id),
  });
  const listsQuery = useQuery({
    queryKey: ["lists"],
    queryFn: async () => (await apiFetch<{ data: AudienceList[] }>("/lists")).data ?? [],
  });
  const tagsQuery = useQuery({
    queryKey: ["tags"],
    queryFn: async () => (await apiFetch<{ data: AudienceTag[] }>("/tags")).data ?? [],
  });

  useEffect(() => {
    if (formQuery.data) setDraft(formQuery.data);
  }, [formQuery.data]);

  const save = useMutation({
    mutationFn: () => {
      if (!draft) throw new Error("The form is not loaded.");
      return apiFetch<FormDetails>(`/forms/${id}`, {
        method: "PATCH",
        body: JSON.stringify(formPayload(draft)),
      });
    },
    onSuccess: (form) => {
      setDraft(form);
      setError("");
      queryClient.invalidateQueries({ queryKey: ["form", id] });
      queryClient.invalidateQueries({ queryKey: ["forms"] });
    },
    onError: (reason) => setError(reason instanceof Error ? reason.message : "Could not save the form."),
  });

  const changeStatus = useMutation({
    mutationFn: async () => {
      if (!draft) throw new Error("The form is not loaded.");
      await apiFetch(`/forms/${id}`, { method: "PATCH", body: JSON.stringify(formPayload(draft)) });
      return apiFetch(`/forms/${id}/${formQuery.data?.status === "published" ? "unpublish" : "publish"}`, { method: "POST" });
    },
    onSuccess: () => {
      setError("");
      queryClient.invalidateQueries({ queryKey: ["form", id] });
      queryClient.invalidateQueries({ queryKey: ["forms"] });
    },
    onError: (reason) => setError(reason instanceof Error ? reason.message : "Could not change the form status."),
  });

  const previewFields = useMemo(() => draft?.fields ?? [], [draft?.fields]);
  const update = <K extends keyof FormDetails>(key: K, value: FormDetails[K]) => {
    setDraft((current) => current ? { ...current, [key]: value } : current);
  };

  if (formQuery.isError) {
    return (
      <div>
        <PageHeader eyebrow="Marketing" title="Form builder" description="The form could not be loaded." />
        <p role="alert" className="text-sm text-rose-700">{formQuery.error instanceof Error ? formQuery.error.message : "Could not load the form."}</p>
        <button type="button" className="mt-3 text-sm text-indigo-700 underline" onClick={() => formQuery.refetch()}>Retry</button>
      </div>
    );
  }
  if (formQuery.isLoading || !draft) {
    return <PageHeader eyebrow="Marketing" title="Form builder" description="Loading form settings..." />;
  }

  return (
    <div>
      <PageHeader
        eyebrow="Marketing"
        title="Form builder"
        description={`Edit ${draft.name}. Changes appear in the preview as you work.`}
        actions={
          <div className="flex flex-wrap gap-2">
            <Link href="/forms" className="inline-flex h-9 items-center gap-1.5 rounded-lg px-3 text-sm text-slate-600 ring-1 ring-inset ring-slate-200 hover:bg-slate-50"><ArrowLeft className="h-4 w-4" /> Forms</Link>
            <Link href={`/forms/${id}/submissions`} className="inline-flex h-9 items-center rounded-lg px-3 text-sm text-slate-600 ring-1 ring-inset ring-slate-200 hover:bg-slate-50">Submissions</Link>
            <Link href={`/forms/${id}/embed`} className="inline-flex h-9 items-center rounded-lg px-3 text-sm text-slate-600 ring-1 ring-inset ring-slate-200 hover:bg-slate-50">Embed</Link>
            <button type="button" onClick={() => save.mutate()} disabled={save.isPending} className="inline-flex h-9 items-center gap-1.5 rounded-lg bg-slate-900 px-3 text-sm font-medium text-white hover:bg-slate-800 disabled:opacity-60"><Save className="h-4 w-4" /> {save.isPending ? "Saving..." : "Save"}</button>
            <button type="button" onClick={() => changeStatus.mutate()} disabled={changeStatus.isPending} className="inline-flex h-9 items-center gap-1.5 rounded-lg bg-indigo-600 px-3 text-sm font-medium text-white hover:bg-indigo-500 disabled:opacity-60">
              {changeStatus.isPending ? "Updating..." : draft.status === "published" ? "Unpublish" : "Publish"}
            </button>
          </div>
        }
      />
      {error && <p role="alert" className="mb-4 rounded-lg bg-rose-50 p-3 text-sm text-rose-700">{error}</p>}

      <div className="grid items-start gap-5 xl:grid-cols-[minmax(0,1.1fr)_minmax(360px,0.9fr)]">
        <div className="space-y-5">
          <Card>
            <CardHeader><CardTitle>Form details</CardTitle></CardHeader>
            <CardContent className="grid gap-4 sm:grid-cols-2">
              <label className="text-sm font-medium text-slate-700 sm:col-span-2">Name
                <input className={inputClass} value={draft.name} onChange={(event) => update("name", event.target.value)} />
              </label>
              <label className="text-sm font-medium text-slate-700 sm:col-span-2">Description
                <textarea className={inputClass} rows={2} value={draft.description} onChange={(event) => update("description", event.target.value)} />
              </label>
              <label className="text-sm font-medium text-slate-700">Add confirmed contacts to
                <select className={inputClass} value={draft.list_id} onChange={(event) => update("list_id", event.target.value)}>
                  <option value="">No list</option>
                  {(listsQuery.data ?? []).map((list) => <option key={list.id} value={list.id}>{list.name}</option>)}
                </select>
              </label>
              <div className="text-sm font-medium text-slate-700">Apply tags to confirmed contacts
                <div className="mt-2 max-h-32 space-y-2 overflow-y-auto rounded-lg border border-slate-200 p-3">
                  {tagsQuery.isError && <p role="alert" className="text-xs text-rose-700">Could not load tags. <button type="button" className="underline" onClick={() => tagsQuery.refetch()}>Retry</button></p>}
                  {!tagsQuery.isError && (tagsQuery.data ?? []).length === 0 && <p className="text-xs text-slate-500">Create tags in Audience to assign them to new subscribers.</p>}
                  {(tagsQuery.data ?? []).map((tag) => (
                    <label key={tag.id} className="flex items-center gap-2 text-sm font-normal">
                      <input type="checkbox" checked={draft.tag_ids.includes(tag.id)} onChange={(event) => update("tag_ids", event.target.checked ? [...draft.tag_ids, tag.id] : draft.tag_ids.filter((id) => id !== tag.id))} />
                      <span className="h-2.5 w-2.5 rounded-full" style={{ backgroundColor: tag.color }} />{tag.name}
                    </label>
                  ))}
                </div>
              </div>
              {listsQuery.isError && <p role="alert" className="text-xs text-rose-700 sm:col-span-2">Could not load lists. <button type="button" className="underline" onClick={() => listsQuery.refetch()}>Retry</button></p>}
              <label className="text-sm font-medium text-slate-700 sm:col-span-2">Success message
                <textarea className={inputClass} rows={2} value={draft.success_message} onChange={(event) => update("success_message", event.target.value)} />
              </label>
              <label className="text-sm font-medium text-slate-700 sm:col-span-2">Redirect URL (optional)
                <input className={inputClass} type="url" value={draft.redirect_url} onChange={(event) => update("redirect_url", event.target.value)} />
              </label>
              <label className="text-sm font-medium text-slate-700 sm:col-span-2">Consent text (leave blank to omit the consent checkbox)
                <input className={inputClass} value={draft.consent_text} onChange={(event) => update("consent_text", event.target.value)} />
              </label>
              <label className="flex items-center gap-2 text-sm text-slate-700"><input type="checkbox" checked={draft.spam_protection} onChange={(event) => update("spam_protection", event.target.checked)} /> Enable hidden honeypot protection</label>
              <label className="flex items-center gap-2 text-sm text-slate-700"><input type="checkbox" checked={draft.double_opt_in} onChange={(event) => update("double_opt_in", event.target.checked)} /> Require email confirmation before subscribing</label>
            </CardContent>
          </Card>

          <Card>
            <CardHeader className="flex-row items-center justify-between">
              <CardTitle>Fields</CardTitle>
            <button type="button" disabled={draft.fields.length >= 30} onClick={() => update("fields", [...draft.fields, { field_type: "custom", custom_key: `field_${draft.fields.length + 1}`, label: "New field", required: false, position: draft.fields.length }])} className="inline-flex items-center gap-1 text-sm font-medium text-indigo-700 disabled:opacity-50"><Plus className="h-4 w-4" /> Add field</button>
            </CardHeader>
            <CardContent className="space-y-4">
              {draft.fields.map((field, index) => (
                <div key={`${field.id ?? "new"}-${index}`} className="grid gap-3 rounded-lg border border-slate-200 p-4 sm:grid-cols-[1fr_1fr_auto]">
                  <label className="text-xs font-medium text-slate-600">Field type
                    <select className={inputClass} value={field.field_type} onChange={(event) => {
                      const fields = [...draft.fields];
                      const type = event.target.value;
                      fields[index] = { ...field, field_type: type, custom_key: type === "custom" ? (field.custom_key || `field_${index + 1}`) : "" };
                      if (type === "email") fields[index].required = true;
                      update("fields", fields);
                    }}>
                      {fieldTypes.filter(([value]) => value !== "email" || field.field_type === "email" || draft.fields.every((item, fieldIndex) => fieldIndex === index || item.field_type !== "email")).map(([value, label]) => <option key={value} value={value}>{label}</option>)}
                    </select>
                  </label>
                  <label className="text-xs font-medium text-slate-600">Label
                    <input className={inputClass} value={field.label} onChange={(event) => {
                      const fields = [...draft.fields];
                      fields[index] = { ...field, label: event.target.value };
                      update("fields", fields);
                    }} />
                  </label>
                  <button type="button" aria-label={`Remove ${field.label}`} disabled={field.field_type === "email"} onClick={() => update("fields", draft.fields.filter((_, fieldIndex) => fieldIndex !== index))} className="mt-5 rounded p-2 text-slate-500 hover:bg-rose-50 hover:text-rose-700 disabled:opacity-30"><Trash2 className="h-4 w-4" /></button>
                  <label className="text-xs font-medium text-slate-600 sm:col-span-2">Placeholder
                    <input className={inputClass} value={field.placeholder ?? ""} onChange={(event) => {
                      const fields = [...draft.fields];
                      fields[index] = { ...field, placeholder: event.target.value };
                      update("fields", fields);
                    }} />
                  </label>
                  {field.field_type === "custom" && (
                    <label className="text-xs font-medium text-slate-600">Custom key
                      <input className={inputClass} value={field.custom_key ?? ""} onChange={(event) => {
                        const fields = [...draft.fields];
                        fields[index] = { ...field, custom_key: event.target.value };
                        update("fields", fields);
                      }} />
                    </label>
                  )}
                  <label className="flex items-center gap-2 text-xs text-slate-700 sm:col-span-3"><input type="checkbox" checked={field.field_type === "email" || field.required} disabled={field.field_type === "email"} onChange={(event) => {
                    const fields = [...draft.fields];
                    fields[index] = { ...field, required: event.target.checked };
                    update("fields", fields);
                  }} /> Required field</label>
                </div>
              ))}
            </CardContent>
          </Card>
        </div>

        <Card className="xl:sticky xl:top-5">
          <CardHeader><CardTitle className="flex items-center gap-2"><Eye className="h-4 w-4" /> Live preview</CardTitle></CardHeader>
          <CardContent>
            <div className="rounded-xl border border-slate-200 bg-white p-6 shadow-sm">
              <h2 className="text-xl font-semibold text-slate-900">{draft.name || "Signup form"}</h2>
              {draft.description && <p className="mt-2 text-sm text-slate-600">{draft.description}</p>}
              <div className="mt-5 space-y-4">
                {previewFields.map((field, index) => (
                  <label key={`${field.field_type}-${index}`} className="block text-sm font-medium text-slate-700">
                    {field.label}{field.required && <span aria-hidden="true"> *</span>}
                    <input className={inputClass} placeholder={field.placeholder} disabled />
                  </label>
                ))}
                {draft.consent_text && <label className="flex items-start gap-2 text-xs text-slate-600"><input type="checkbox" disabled />{draft.consent_text}</label>}
                <div className="w-full rounded-lg bg-indigo-600 px-4 py-2.5 text-center text-sm font-medium text-white">Subscribe</div>
              </div>
              <p className="mt-4 text-xs text-slate-500">{draft.success_message}</p>
            </div>
            {draft.status === "published" && <Link className="mt-4 inline-block text-sm text-indigo-700 underline" target="_blank" href={`/f/${draft.slug}`}>Open hosted form</Link>}
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
