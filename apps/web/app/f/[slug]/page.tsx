"use client";

import { useEffect, useState } from "react";
import type { FormEvent } from "react";
import { useParams } from "next/navigation";
import { apiUrl } from "@/lib/api-url";

type PublicField = {
  id: string;
  field_type: string;
  label: string;
  placeholder?: string;
  required: boolean;
  custom_key?: string;
};

type PublicForm = {
  name: string;
  slug: string;
  description: string;
  success_message: string;
  redirect_url: string;
  consent_text: string;
  spam_protection: boolean;
  double_opt_in: boolean;
  fields: PublicField[];
};

type FormResponse = { data: PublicForm };

function fieldKey(field: PublicField) {
  return field.field_type === "custom" ? field.custom_key ?? "" : field.field_type;
}

export default function HostedFormPage() {
  const { slug } = useParams<{ slug: string }>();
  const [form, setForm] = useState<PublicForm | null>(null);
  const [values, setValues] = useState<Record<string, string>>({});
  const [consent, setConsent] = useState(false);
  const [website, setWebsite] = useState("");
  const [loading, setLoading] = useState(true);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");

  useEffect(() => {
    let active = true;
    fetch(apiUrl(`/api/v1/public/forms/${encodeURIComponent(slug)}`), { cache: "no-store", credentials: "omit" })
      .then(async (response) => {
        if (!response.ok) throw new Error(response.status === 404 ? "This form is not available." : "Could not load this form.");
        return response.json() as Promise<FormResponse>;
      })
      .then((response) => {
        if (active) setForm(response.data);
      })
      .catch((reason: unknown) => {
        if (active) setError(reason instanceof Error ? reason.message : "Could not load this form.");
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => { active = false; };
  }, [slug]);

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!form) return;
    setSubmitting(true);
    setError("");
    try {
      const response = await fetch(apiUrl(`/api/v1/public/forms/${encodeURIComponent(form.slug)}/submit`), {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "omit",
        body: JSON.stringify({ data: values, consent, website }),
      });
      const result = await response.json() as {
        status?: string;
        success_message?: string;
        message?: string;
        redirect_url?: string;
        error?: { message?: string };
      };
      if (!response.ok) throw new Error(result.error?.message ?? "Could not submit the form.");
      if (result.redirect_url) {
        window.location.assign(result.redirect_url);
        return;
      }
      setMessage(result.message ?? result.success_message ?? form.success_message);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Could not submit the form.");
    } finally {
      setSubmitting(false);
    }
  };

  if (loading) {
    return <main className="mx-auto mt-20 max-w-lg px-5 text-center text-sm text-slate-500">Loading form...</main>;
  }
  if (!form) {
    return <main className="mx-auto mt-20 max-w-lg px-5 text-center"><h1 className="text-xl font-semibold text-slate-900">Form unavailable</h1><p role="alert" className="mt-2 text-sm text-rose-700">{error}</p></main>;
  }

  return (
    <main className="mx-auto my-12 max-w-xl px-5">
      <section className="rounded-2xl border border-slate-200 bg-white p-7 shadow-sm sm:p-9">
        <h1 className="text-2xl font-semibold tracking-tight text-slate-950">{form.name}</h1>
        {form.description && <p className="mt-2 text-sm leading-6 text-slate-600">{form.description}</p>}
        {message ? (
          <div role="status" className="mt-6 rounded-lg bg-emerald-50 p-4 text-sm text-emerald-800">{message}</div>
        ) : (
          <form onSubmit={submit} className="mt-6 space-y-4">
            {form.fields.map((field) => {
              const key = fieldKey(field);
              return (
                <label key={field.id} className="block text-sm font-medium text-slate-700">
                  {field.label}{field.required && <span aria-hidden="true"> *</span>}
                  <input
                    type={field.field_type === "email" ? "email" : field.field_type === "phone" ? "tel" : "text"}
                    name={key}
                    autoComplete={field.field_type === "email" ? "email" : field.field_type === "first_name" ? "given-name" : field.field_type === "last_name" ? "family-name" : "off"}
                    required={field.required}
                    placeholder={field.placeholder}
                    value={values[key] ?? ""}
                    onChange={(event) => setValues((current) => ({ ...current, [key]: event.target.value }))}
                    className="mt-1 block w-full rounded-lg border border-slate-300 bg-white px-3 py-2.5 text-sm text-slate-900 shadow-sm focus:border-indigo-500 focus:outline-none focus:ring-2 focus:ring-indigo-100"
                  />
                </label>
              );
            })}
            {form.spam_protection !== false && (
              <div aria-hidden="true" className="absolute -left-[10000px] top-auto h-px w-px overflow-hidden">
                <label>Leave this field empty<input tabIndex={-1} autoComplete="off" value={website} onChange={(event) => setWebsite(event.target.value)} /></label>
              </div>
            )}
            {form.consent_text && (
              <label className="flex items-start gap-2 text-sm leading-5 text-slate-600">
                <input type="checkbox" required checked={consent} onChange={(event) => setConsent(event.target.checked)} className="mt-1" />
                <span>{form.consent_text}</span>
              </label>
            )}
            {error && <p role="alert" className="rounded-lg bg-rose-50 p-3 text-sm text-rose-700">{error}</p>}
            <button type="submit" disabled={submitting} className="w-full rounded-lg bg-indigo-600 px-4 py-2.5 text-sm font-semibold text-white hover:bg-indigo-500 disabled:opacity-60">
              {submitting ? "Submitting..." : form.double_opt_in ? "Continue" : "Subscribe"}
            </button>
            {form.double_opt_in && <p className="text-xs text-slate-500">You will receive an email with a link to confirm your subscription.</p>}
          </form>
        )}
      </section>
    </main>
  );
}
