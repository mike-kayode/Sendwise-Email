"use client";

import { useState } from "react";
import Link from "next/link";
import { ArrowLeft, Eye, Save } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { GrapesEmailEditor } from "@/components/grapes-email-editor";
import { Button } from "@/components/ui/button";
import { Input, Select, Textarea } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import { apiFetch, type Campaign, type SenderIdentity } from "@/lib/auth";
import { blocksToHtml, type EmailDesignBlock } from "@/lib/email-html-importer";

type EmailDesignTheme = {
  background: string;
  content_background: string;
  text: string;
  heading: string;
  accent: string;
  button: string;
  link: string;
};

export default function EditorForm({
  initialCampaign,
  initialBlocks,
  initialProject,
  initialHtml,
}: {
  initialCampaign: Campaign;
  initialBlocks: EmailDesignBlock[];
  initialProject?: unknown;
  initialHtml: string;
}) {
  const initialDesign = initialCampaign.content ?? {};
  const [editorProject, setEditorProject] = useState<unknown>(initialProject);
  const [html, setHtml] = useState(initialHtml || blocksToHtml(initialBlocks));
  const [mode, setMode] = useState<"plain" | "html">(initialDesign.mode === "plain" ? "plain" : "html");
  const [textContent, setTextContent] = useState(typeof initialDesign.text_content === "string" ? initialDesign.text_content : "");
  const [theme, setTheme] = useState<EmailDesignTheme>({
    background: "#f1f5f9",
    content_background: "#ffffff",
    text: "#334155",
    heading: "#0f172a",
    accent: "#4f46e5",
    button: "#4f46e5",
    link: "#4f46e5",
    ...(initialDesign.theme && typeof initialDesign.theme === "object" ? initialDesign.theme : {}),
  });
  const [subject, setSubject] = useState(initialCampaign.subject ?? "");
  const [previewText, setPreviewText] = useState(initialCampaign.preview_text ?? "");
  const [senderIdentityID, setSenderIdentityID] = useState(initialCampaign.sender_identity_id ?? "");
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const identitiesQuery = useQuery({
    queryKey: ["sender-identities"],
    queryFn: async () => {
      const response = await apiFetch<{ data: SenderIdentity[] | null }>("/sender-identities");
      return response.data ?? [];
    },
  });
  const profileQuery = useQuery({
    queryKey: ["sender-profile"],
    queryFn: () => apiFetch<{ complete: boolean }>("/sender-profile"),
  });

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
          sender_identity_id: senderIdentityID,
          content: {
            version: 2,
            editor: "grapesjs",
            editor_project: editorProject,
            html,
            mode,
            text_content: textContent,
            theme,
          },
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

  if (profileQuery.isLoading) {
    return <p className="rounded-xl border border-slate-200 bg-white p-6 text-sm text-slate-600">Loading sender profile...</p>;
  }
  if (profileQuery.error) {
    return <p role="alert" className="rounded-xl border border-rose-200 bg-rose-50 p-6 text-sm text-rose-800">Could not load sender profile: {profileQuery.error.message}</p>;
  }
  if (!profileQuery.data?.complete) {
    return (
      <section className="rounded-xl border border-amber-200 bg-amber-50 p-6">
        <h1 className="text-lg font-semibold text-amber-950">Complete your sender profile</h1>
        <p className="mt-2 text-sm text-amber-900">
          Campaign editing and sending are locked until the company details, postal address, website, and support email are saved.
        </p>
        <Link href="/settings#sender-profile" className="mt-4 inline-flex text-sm font-medium text-amber-950 underline">
          Set up sender profile
        </Link>
      </section>
    );
  }

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
          <Button onClick={() => void save()} disabled={saving || !senderIdentityID}>
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
          From identity
          <Select
            className="mt-1"
            value={senderIdentityID}
            onChange={(event) => setSenderIdentityID(event.target.value)}
            disabled={identitiesQuery.isLoading}
          >
            <option value="">Choose a verified sender</option>
            {(identitiesQuery.data ?? []).filter((identity) => identity.verified).map((identity) => (
              <option key={identity.id} value={identity.id}>
                {identity.display_name} &lt;{identity.email}&gt;
              </option>
            ))}
          </Select>
        </label>
        <p className="self-end text-xs text-slate-500">The sender profile supplies the compliance footer and reply address.</p>
      </section>

      <label className="block max-w-sm text-sm font-medium text-slate-700">
        Content format
        <Select
          className="mt-1"
          value={mode}
          onChange={(event) => {
            const next = event.target.value as "plain" | "html";
            if (mode === "html" && next === "plain" && !window.confirm("Switching from HTML to plain text may lose layout and formatting. Continue?")) return;
            if (mode === "html" && next === "plain" && !textContent.trim()) {
              setTextContent(new DOMParser().parseFromString(html, "text/html").body.textContent?.trim() ?? "");
            }
            setMode(next);
            setSaved(false);
          }}
        >
          <option value="html">Visual HTML email</option>
          <option value="plain">Plain text</option>
        </Select>
      </label>
      {mode === "html" ? (
        <GrapesEmailEditor
          project={editorProject}
          html={html}
          onChange={(design, changed) => {
            setEditorProject(design.project);
            setHtml(design.html);
            if (changed) setSaved(false);
          }}
        />
      ) : (
        <label className="block text-sm font-medium text-slate-700">
          Plain-text message
          <Textarea
            rows={12}
            className="mt-1 font-mono"
            value={textContent}
            onChange={(event) => {
              setTextContent(event.target.value);
              setSaved(false);
            }}
          />
        </label>
      )}
    </div>
  );
}
