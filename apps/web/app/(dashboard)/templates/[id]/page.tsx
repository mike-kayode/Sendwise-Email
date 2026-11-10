"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { useParams, useRouter } from "next/navigation";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, Save, Trash2 } from "lucide-react";
import { GrapesEmailEditor, type GrapesEmailDesign } from "@/components/grapes-email-editor";
import { PageHeader } from "@/components/page-header";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input, Select, Textarea } from "@/components/ui/input";
import { apiFetch, type SenderIdentity } from "@/lib/auth";
import { blocksToHtml, type EmailDesignBlock } from "@/lib/email-html-importer";
import { useToast } from "@/components/ui/use-toast";

type Template = {
  id: string;
  name: string;
  category: string;
  design: unknown;
  mode?: "plain" | "html";
  text_content?: string;
  theme?: Record<string, string>;
  sender_identity_id?: string | null;
};

const categories = ["general", "newsletter", "lifecycle", "promotion", "events"];
const defaultTheme = {
  background: "#f1f5f9",
  content_background: "#ffffff",
  text: "#334155",
  heading: "#0f172a",
  accent: "#4f46e5",
  button: "#4f46e5",
  link: "#4f46e5",
};

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function readDesign(template: Template) {
  let parsed: unknown = template.design;
  if (typeof parsed === "string") {
    try {
      parsed = JSON.parse(parsed) as unknown;
    } catch {
      parsed = {};
    }
  }
  const design = isRecord(parsed) ? parsed : {};
  const blocks = Array.isArray(design.blocks) ? design.blocks as EmailDesignBlock[] : [];
  const html = typeof design.html === "string" ? design.html : blocksToHtml(blocks);
  const project = design.editor_project;
  const theme = {
    ...defaultTheme,
    ...(isRecord(design.theme) ? design.theme : {}),
    ...(template.theme ?? {}),
  };
  return {
    html,
    project,
    mode: template.mode ?? (design.mode === "plain" ? "plain" : "html"),
    textContent: template.text_content ?? (typeof design.text_content === "string" ? design.text_content : ""),
    theme,
  };
}

export default function TemplateEditorPage() {
  const { id } = useParams<{ id: string }>();
  const router = useRouter();
  const queryClient = useQueryClient();
  const { toast } = useToast();
  const [name, setName] = useState("");
  const [category, setCategory] = useState("general");
  const [html, setHtml] = useState("");
  const [editorProject, setEditorProject] = useState<unknown>(null);
  const [mode, setMode] = useState<"plain" | "html">("html");
  const [textContent, setTextContent] = useState("");
  const [theme, setTheme] = useState(defaultTheme);
  const [senderIdentityID, setSenderIdentityID] = useState("");
  const [saving, setSaving] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [testRecipient, setTestRecipient] = useState("");
  const [sendingTest, setSendingTest] = useState(false);
  const [testStatus, setTestStatus] = useState<string | null>(null);
  const [dirty, setDirty] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const templateQuery = useQuery({
    queryKey: ["template", id],
    queryFn: () => apiFetch<Template>(`/templates/${id}`),
  });
  const identitiesQuery = useQuery({
    queryKey: ["sender-identities"],
    queryFn: async () => (await apiFetch<{ data: SenderIdentity[] | null }>("/sender-identities")).data ?? [],
  });

  useEffect(() => {
    const template = templateQuery.data;
    if (!template) return;
    setName(template.name);
    setCategory(template.category);
    const design = readDesign(template);
    setHtml(design.html);
    setEditorProject(design.project);
    setMode(design.mode);
    setTextContent(design.textContent);
    setTheme(design.theme);
    setSenderIdentityID(template.sender_identity_id ?? "");
    setDirty(false);
  }, [templateQuery.data]);

  const handleEditorChange = (next: GrapesEmailDesign, changed: boolean) => {
    setEditorProject(next.project);
    setHtml(next.html);
    if (changed) setDirty(true);
  };

  const changeMode = (next: "plain" | "html") => {
    if (next === mode) return;
    if (mode === "html" && next === "plain") {
      if (!window.confirm("Switching from HTML to plain text may lose layout and formatting. Continue?")) return;
      if (!textContent.trim()) {
        setTextContent(new DOMParser().parseFromString(html, "text/html").body.textContent?.trim() ?? "");
      }
    }
    setMode(next);
    setDirty(true);
  };

  const save = async () => {
    if (saving) return;
    setSaving(true);
    setError(null);
    try {
      await apiFetch<Template>(`/templates/${id}`, {
        method: "PATCH",
        body: JSON.stringify({
          name,
          category,
          design: {
            version: 2,
            editor: "grapesjs",
            editor_project: editorProject,
            html,
            mode,
            text_content: textContent,
            theme,
          },
          mode,
          text_content: textContent,
          theme,
          sender_identity_id: senderIdentityID,
        }),
      });
      setDirty(false);
      await queryClient.invalidateQueries({ queryKey: ["templates"] });
      await queryClient.invalidateQueries({ queryKey: ["template", id] });
      toast({ title: "Template saved", description: "Your design is ready to use in a campaign." });
    } catch (saveError) {
      const message = saveError instanceof Error ? saveError.message : "Could not save this template.";
      setError(message);
      toast({ title: "Could not save template", description: message, variant: "destructive" });
    } finally {
      setSaving(false);
    }
  };

  const sendTestEmail = async () => {
    if (!testRecipient.trim() || sendingTest) return;
    setSendingTest(true);
    setTestStatus(null);
    try {
      const result = await apiFetch<{ status: string }>(`/templates/${id}/test`, {
        method: "POST",
        body: JSON.stringify({ recipient: testRecipient.trim(), sender_identity_id: senderIdentityID }),
      });
      setTestStatus(result.status);
      toast({ title: "Test email accepted", description: `The relay accepted the test message for ${testRecipient.trim()}.` });
    } catch (sendError) {
      const message = sendError instanceof Error ? sendError.message : "The test email could not be sent.";
      setTestStatus(message);
      toast({ title: "Could not send test email", description: message, variant: "destructive" });
    } finally {
      setSendingTest(false);
    }
  };

  useEffect(() => {
    if (!dirty || !templateQuery.data || saving) return;
    const timeout = window.setTimeout(() => void save(), 1800);
    return () => window.clearTimeout(timeout);
  }, [dirty, html, editorProject, mode, textContent, theme, name, category, senderIdentityID, saving, templateQuery.data]);

  const remove = async () => {
    if (!templateQuery.data || deleting) return;
    if (!window.confirm(`Delete "${templateQuery.data.name}"? Campaigns already created from it will not be affected.`)) return;
    setDeleting(true);
    setError(null);
    try {
      await apiFetch(`/templates/${id}`, { method: "DELETE" });
      await queryClient.invalidateQueries({ queryKey: ["templates"] });
      router.push("/templates");
    } catch (deleteError) {
      const message = deleteError instanceof Error ? deleteError.message : "Could not delete this template.";
      setError(message);
      setDeleting(false);
    }
  };

  if (templateQuery.isLoading) {
    return <PageHeader eyebrow="Templates" title="Template editor" description="Loading template..." />;
  }
  if (templateQuery.error || !templateQuery.data) {
    return (
      <div>
        <Link href="/templates" className="mb-4 inline-flex items-center gap-1 text-sm text-slate-500 hover:text-slate-900">
          <ArrowLeft className="h-4 w-4" /> Back to templates
        </Link>
        <PageHeader eyebrow="Templates" title="Template editor" description={templateQuery.error?.message ?? "Template not found."} />
      </div>
    );
  }

  return (
    <div>
      <Link href="/templates" className="mb-4 inline-flex items-center gap-1 text-sm text-slate-500 hover:text-slate-900">
        <ArrowLeft className="h-4 w-4" /> Back to templates
      </Link>
      <PageHeader
        eyebrow="Templates"
        title="Design template"
        description="Build responsive email layouts, import existing HTML, and save the editable project."
        actions={
          <div className="flex gap-2">
            <Button type="button" variant="secondary" onClick={() => void remove()} disabled={deleting || saving}>
              <Trash2 className="h-4 w-4" /> {deleting ? "Deleting..." : "Delete"}
            </Button>
            <Button type="button" onClick={() => void save()} disabled={saving || !name.trim()}>
              <Save className="h-4 w-4" /> {saving ? "Saving..." : "Save template"}
            </Button>
          </div>
        }
      />
      {error ? <p role="alert" className="mb-4 rounded-lg bg-rose-50 p-3 text-sm text-rose-800">{error}</p> : null}
      <Card className="mb-5">
        <CardContent className="grid gap-4 p-5 sm:grid-cols-2">
          <label className="text-sm font-medium text-slate-700">
            Template name
            <Input aria-label="Template name" className="mt-1" value={name} onChange={(event) => { setName(event.target.value); setDirty(true); }} />
          </label>
          <label className="text-sm font-medium text-slate-700">
            Category
            <Select aria-label="Template category" className="mt-1" value={category} onChange={(event) => { setCategory(event.target.value); setDirty(true); }}>
              {categories.map((item) => <option key={item} value={item}>{item}</option>)}
            </Select>
          </label>
          <label className="text-sm font-medium text-slate-700">
            From identity
            <Select
              aria-label="Template sender identity"
              className="mt-1"
              value={senderIdentityID}
              disabled={identitiesQuery.isLoading}
              onChange={(event) => { setSenderIdentityID(event.target.value); setDirty(true); }}
            >
              <option value="">Choose when creating a campaign</option>
              {(identitiesQuery.data ?? []).filter((identity) => identity.verified).map((identity) => (
                <option key={identity.id} value={identity.id}>{identity.display_name} &lt;{identity.email}&gt;</option>
              ))}
            </Select>
          </label>
          <label className="text-sm font-medium text-slate-700">
            Content format
            <Select aria-label="Template mode" className="mt-1" value={mode} onChange={(event) => changeMode(event.target.value as "plain" | "html")}>
              <option value="html">Visual HTML email</option>
              <option value="plain">Plain text</option>
            </Select>
          </label>
        </CardContent>
      </Card>
      {mode === "plain" ? (
        <label className="mb-5 block text-sm font-medium text-slate-700">
          Plain-text message
          <Textarea aria-label="Plain-text message" rows={12} className="mt-1 font-mono" value={textContent} onChange={(event) => { setTextContent(event.target.value); setDirty(true); }} />
        </label>
      ) : (
        <GrapesEmailEditor project={editorProject} html={html} onChange={handleEditorChange} />
      )}
      <Card className="mt-5">
        <CardContent className="flex flex-col gap-3 p-5 sm:flex-row sm:items-end">
          <label className="flex-1 text-sm font-medium text-slate-700">
            Test recipient
            <Input
              aria-label="Test recipient"
              type="email"
              className="mt-1"
              value={testRecipient}
              onChange={(event) => setTestRecipient(event.target.value)}
              placeholder="you@example.com"
            />
          </label>
          <Button
            type="button"
            variant="secondary"
            onClick={() => void sendTestEmail()}
            disabled={!testRecipient.trim() || sendingTest || saving || dirty}
          >
            {sendingTest ? "Sending test..." : "Send test email"}
          </Button>
          {testStatus ? <p role="status" className="text-sm text-slate-600">{testStatus}</p> : null}
        </CardContent>
      </Card>
      <p className="mt-3 text-xs text-slate-500" role="status">{saving ? "Autosaving…" : dirty ? "Unsaved changes" : "All changes saved"}</p>
    </div>
  );
}
