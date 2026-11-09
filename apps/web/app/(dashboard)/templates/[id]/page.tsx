"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { useParams, useRouter } from "next/navigation";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, Redo2, Save, Trash2, Undo2 } from "lucide-react";
import { EmailDesignEditor, type EmailDesignBlock, type EmailDesignTheme } from "@/components/email-design-editor";
import { PageHeader } from "@/components/page-header";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input, Select } from "@/components/ui/input";
import { apiFetch, type SenderIdentity } from "@/lib/auth";
import { useToast } from "@/components/ui/use-toast";

type Template = {
  id: string;
  name: string;
  category: string;
  design: unknown;
  mode?: "plain" | "html";
  text_content?: string;
  theme?: EmailDesignTheme;
  sender_identity_id?: string | null;
};

const categories = ["general", "newsletter", "lifecycle", "promotion", "events"];
const defaultTheme: EmailDesignTheme = {
  background: "#f1f5f9",
  content_background: "#ffffff",
  text: "#334155",
  heading: "#0f172a",
  accent: "#4f46e5",
  button: "#4f46e5",
  link: "#4f46e5",
};

function readDesign(template: Template) {
  const parsed = typeof template.design === "string" ? JSON.parse(template.design) as unknown : template.design;
  const design = parsed && typeof parsed === "object" ? parsed as Record<string, unknown> : {};
  const blocks = Array.isArray(design.blocks) ? design.blocks : [];
  const designTheme = design.theme && typeof design.theme === "object" ? design.theme as Partial<EmailDesignTheme> : {};
  const templateTheme = template.theme && typeof template.theme === "object" ? template.theme : {};
  const theme = { ...defaultTheme, ...designTheme, ...templateTheme };
  return {
    blocks: blocks.map((block, index) => {
      if (!block || typeof block !== "object") return { id: `block-${index}`, type: "paragraph", content: "" };
      const item = block as Record<string, unknown>;
      return {
        id: typeof item.id === "string" ? item.id : `block-${index}`,
        type: typeof item.type === "string" ? item.type : "paragraph",
        content: typeof item.content === "string" ? item.content : "",
        ...(typeof item.url === "string" ? { url: item.url } : {}),
        ...(typeof item.src === "string" ? { src: item.src } : {}),
        ...(typeof item.alt === "string" ? { alt: item.alt } : {}),
        ...(typeof item.align === "string" ? { align: item.align } : {}),
        ...(typeof item.size === "string" ? { size: item.size } : {}),
        ...(typeof item.bg_color === "string" ? { bg_color: item.bg_color } : {}),
        ...(typeof item.text_color === "string" ? { text_color: item.text_color } : {}),
        ...(Array.isArray(item.columns) ? { columns: item.columns as EmailDesignBlock[][] } : {}),
      };
    }) as EmailDesignBlock[],
    mode: template.mode ?? (design.mode === "plain" ? "plain" : "html"),
    textContent: template.text_content ?? (typeof design.text_content === "string" ? design.text_content : ""),
    theme,
  };
}

type DesignSnapshot = {
  blocks: EmailDesignBlock[];
  mode: "plain" | "html";
  textContent: string;
  theme: EmailDesignTheme;
};

function snapshot(blocks: EmailDesignBlock[], mode: "plain" | "html", textContent: string, theme: EmailDesignTheme): DesignSnapshot {
  return { blocks, mode, textContent, theme };
}

export default function TemplateEditorPage() {
  const { id } = useParams<{ id: string }>();
  const router = useRouter();
  const queryClient = useQueryClient();
  const { toast } = useToast();
  const [name, setName] = useState("");
  const [category, setCategory] = useState("general");
  const [blocks, setBlocks] = useState<EmailDesignBlock[]>([]);
  const [mode, setMode] = useState<"plain" | "html">("html");
  const [textContent, setTextContent] = useState("");
  const [theme, setTheme] = useState<EmailDesignTheme>(defaultTheme);
  const [senderIdentityID, setSenderIdentityID] = useState("");
  const [saving, setSaving] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [testRecipient, setTestRecipient] = useState("");
  const [sendingTest, setSendingTest] = useState(false);
  const [testStatus, setTestStatus] = useState<string | null>(null);
  const [dirty, setDirty] = useState(false);
  const [past, setPast] = useState<DesignSnapshot[]>([]);
  const [future, setFuture] = useState<DesignSnapshot[]>([]);
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
    setBlocks(design.blocks);
    setMode(design.mode);
    setTextContent(design.textContent);
    setTheme(design.theme);
    setSenderIdentityID(template.sender_identity_id ?? "");
    setDirty(false);
  }, [templateQuery.data]);

  const recordEdit = () => {
    setPast((items) => [...items.slice(-49), snapshot(blocks, mode, textContent, theme)]);
    setFuture([]);
    setDirty(true);
  };
  const updateBlocks = (next: EmailDesignBlock[]) => {
    recordEdit();
    setBlocks(next);
  };
  const updateTheme = (next: EmailDesignTheme) => {
    recordEdit();
    setTheme(next);
  };
  const updateTextContent = (next: string) => {
    recordEdit();
    setTextContent(next);
  };
  const changeMode = (next: "plain" | "html") => {
    if (next === mode) return;
    if (mode === "html" && next === "plain") {
      if (!window.confirm("Switching from HTML to plain text may lose layout and formatting. Continue?")) return;
      if (!textContent.trim()) {
        setTextContent(blocks.map((block) => block.content).filter(Boolean).join("\n\n"));
      }
    }
    recordEdit();
    setMode(next);
  };
  const undo = () => {
    const previous = past[past.length - 1];
    if (!previous) return;
    setFuture((items) => [snapshot(blocks, mode, textContent, theme), ...items]);
    setPast((items) => items.slice(0, -1));
    setBlocks(previous.blocks);
    setMode(previous.mode);
    setTextContent(previous.textContent);
    setTheme(previous.theme);
    setDirty(true);
  };
  const redo = () => {
    const next = future[0];
    if (!next) return;
    setPast((items) => [...items, snapshot(blocks, mode, textContent, theme)]);
    setFuture((items) => items.slice(1));
    setBlocks(next.blocks);
    setMode(next.mode);
    setTextContent(next.textContent);
    setTheme(next.theme);
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
          design: { version: 1, mode, text_content: textContent, theme, blocks },
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
      toast({ title: "Test email accepted", description: `Stalwart accepted the test message for ${testRecipient.trim()}.` });
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
  }, [dirty, blocks, mode, textContent, theme, name, category, saving, templateQuery.data]);

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
        description="Edit the content blocks, their order, and the live email preview."
        actions={
          <div className="flex gap-2">
            <Button type="button" variant="ghost" aria-label="Undo" onClick={undo} disabled={!past.length || saving}><Undo2 className="h-4 w-4" /></Button>
            <Button type="button" variant="ghost" aria-label="Redo" onClick={redo} disabled={!future.length || saving}><Redo2 className="h-4 w-4" /></Button>
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
        </CardContent>
      </Card>
      <Card className="mb-5">
        <CardContent className="flex flex-col gap-3 p-5 sm:flex-row sm:items-end">
          <label className="flex-1 text-sm font-medium text-slate-700">
            Test recipient
            <Input
              aria-label="Test recipient"
              type="email"
              className="mt-1"
              value={testRecipient}
              onChange={(event) => setTestRecipient(event.target.value)}
            />
          </label>
          <Button type="button" variant="secondary" onClick={() => void sendTestEmail()} disabled={sendingTest || !testRecipient.trim()}>
            {sendingTest ? "Sending test..." : "Send test email"}
          </Button>
          {testStatus ? <p role="status" className="text-sm text-slate-600">{testStatus}</p> : null}
        </CardContent>
      </Card>
      <EmailDesignEditor
        blocks={blocks}
        onChange={updateBlocks}
        subject={templateQuery.data.name}
        mode={mode}
        onModeChange={changeMode}
        textContent={textContent}
        onTextContentChange={updateTextContent}
        theme={theme}
        onThemeChange={updateTheme}
      />
      <p className="mt-3 text-xs text-slate-500" role="status">{saving ? "Autosaving…" : dirty ? "Unsaved changes" : "All changes saved"}</p>
    </div>
  );
}
