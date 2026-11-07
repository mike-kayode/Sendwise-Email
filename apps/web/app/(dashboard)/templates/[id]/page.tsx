"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { useParams, useRouter } from "next/navigation";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, Save, Trash2 } from "lucide-react";
import { EmailDesignEditor, type EmailDesignBlock } from "@/components/email-design-editor";
import { PageHeader } from "@/components/page-header";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input, Select } from "@/components/ui/input";
import { apiFetch } from "@/lib/auth";
import { useToast } from "@/components/ui/use-toast";

type Template = {
  id: string;
  name: string;
  category: string;
  design: unknown;
};

const categories = ["general", "newsletter", "lifecycle", "promotion", "events"];

function readBlocks(design: unknown): EmailDesignBlock[] {
  const parsed = typeof design === "string" ? JSON.parse(design) as unknown : design;
  if (!parsed || typeof parsed !== "object" || !("blocks" in parsed) || !Array.isArray(parsed.blocks)) {
    return [];
  }
  return parsed.blocks.map((block, index) => {
    if (!block || typeof block !== "object") {
      return { id: `block-${index}`, type: "paragraph", content: "" };
    }
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
    };
  });
}

export default function TemplateEditorPage() {
  const { id } = useParams<{ id: string }>();
  const router = useRouter();
  const queryClient = useQueryClient();
  const { toast } = useToast();
  const [name, setName] = useState("");
  const [category, setCategory] = useState("general");
  const [blocks, setBlocks] = useState<EmailDesignBlock[]>([]);
  const [saving, setSaving] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const templateQuery = useQuery({
    queryKey: ["template", id],
    queryFn: () => apiFetch<Template>(`/templates/${id}`),
  });

  useEffect(() => {
    const template = templateQuery.data;
    if (!template) return;
    setName(template.name);
    setCategory(template.category);
    setBlocks(readBlocks(template.design));
  }, [templateQuery.data]);

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
          design: { version: 1, blocks },
        }),
      });
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
            <Input aria-label="Template name" className="mt-1" value={name} onChange={(event) => setName(event.target.value)} />
          </label>
          <label className="text-sm font-medium text-slate-700">
            Category
            <Select aria-label="Template category" className="mt-1" value={category} onChange={(event) => setCategory(event.target.value)}>
              {categories.map((item) => <option key={item} value={item}>{item}</option>)}
            </Select>
          </label>
        </CardContent>
      </Card>
      <EmailDesignEditor
        blocks={blocks}
        onChange={setBlocks}
        subject={templateQuery.data.name}
      />
    </div>
  );
}
