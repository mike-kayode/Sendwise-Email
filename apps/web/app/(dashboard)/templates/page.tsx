"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Loader2, LayoutTemplate, Plus, Search, Sparkles } from "lucide-react";
import { PageHeader } from "@/components/page-header";
import { Card, CardContent } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog";
import { Input, Select } from "@/components/ui/input";
import { apiFetch, type Campaign } from "@/lib/auth";
import { cn, formatDate } from "@/lib/utils";
import { useToast } from "@/components/ui/use-toast";

type Template = {
  id: string;
  name: string;
  category: string;
  design: unknown;
  created_at: string;
  updated_at: string;
};

const templateCategories = ["general", "newsletter", "lifecycle", "promotion", "events"];
const accents = ["#4f46e5", "#0891b2", "#b45309", "#059669", "#e11d48", "#7c3aed"];
const variants = ["digest", "welcome", "launch", "plain"] as const;

function TemplateThumb({ accent, variant }: { accent: string; variant: string }) {
  return (
    <div className="flex h-44 flex-col gap-2 overflow-hidden rounded-lg border border-slate-200 bg-white p-4">
      <div className="h-2 w-10 rounded-full" style={{ backgroundColor: accent }} />
      {variant === "digest" ? (
        <>
          <div className="h-2.5 w-3/4 rounded bg-slate-800" />
          <div className="h-14 rounded" style={{ backgroundColor: `${accent}22` }} />
          <div className="h-1.5 w-full rounded bg-slate-200" />
          <div className="h-1.5 w-5/6 rounded bg-slate-200" />
          <div className="mt-auto h-5 w-20 rounded" style={{ backgroundColor: accent }} />
        </>
      ) : variant === "welcome" ? (
        <>
          <div className="h-2.5 w-2/3 rounded bg-slate-800" />
          <div className="h-1.5 w-full rounded bg-slate-200" />
          <div className="h-1.5 w-4/6 rounded bg-slate-200" />
          <div className="mt-2 h-10 rounded" style={{ backgroundColor: `${accent}18` }} />
          <div className="mt-auto h-5 w-24 rounded" style={{ backgroundColor: accent }} />
        </>
      ) : variant === "launch" ? (
        <>
          <div className="h-16 rounded" style={{ backgroundColor: `${accent}25` }} />
          <div className="h-2.5 w-3/4 rounded bg-slate-800" />
          <div className="h-1.5 w-full rounded bg-slate-200" />
          <div className="mt-auto flex gap-2">
            <div className="h-5 w-20 rounded" style={{ backgroundColor: accent }} />
            <div className="h-5 w-16 rounded border border-slate-200" />
          </div>
        </>
      ) : (
        <>
          <div className="h-2.5 w-1/2 rounded bg-slate-800" />
          <div className="h-1.5 w-full rounded bg-slate-200" />
          <div className="h-1.5 w-11/12 rounded bg-slate-200" />
          <div className="h-1.5 w-4/5 rounded bg-slate-200" />
          <div className="mt-auto h-5 w-20 rounded" style={{ backgroundColor: accent }} />
        </>
      )}
    </div>
  );
}

export default function TemplatesPage() {
  const router = useRouter();
  const queryClient = useQueryClient();
  const { toast } = useToast();

  const [search, setSearch] = useState("");
  const [category, setCategory] = useState("All");
  const [isNewTemplateOpen, setIsNewTemplateOpen] = useState(false);
  const [newName, setNewName] = useState("");
  const [newCategory, setNewCategory] = useState("general");
  const [creating, setCreating] = useState(false);
  const [usingId, setUsingId] = useState<string | null>(null);

  const templatesQuery = useQuery({
    queryKey: ["templates"],
    queryFn: async () => apiFetch<{ data: Template[] }>("/templates"),
  });

  const templates = templatesQuery.data?.data ?? [];
  const categoryChips = ["All", ...Array.from(new Set(templates.map((t) => t.category)))];

  const filteredTemplates = templates.filter((t) => {
    const matchesCategory = category === "All" || t.category.toLowerCase() === category.toLowerCase();
    const needle = search.toLowerCase();
    const matchesSearch =
      t.name.toLowerCase().includes(needle) || t.category.toLowerCase().includes(needle);
    return matchesCategory && matchesSearch;
  });

  const closeNewTemplate = (next: boolean) => {
    setIsNewTemplateOpen(next);
    if (!next) {
      setNewName("");
      setNewCategory("general");
    }
  };

  const handleCreateTemplate = async () => {
    if (!newName.trim() || creating) return;
    setCreating(true);
    try {
      const template = await apiFetch<Template>("/templates", {
        method: "POST",
        body: JSON.stringify({ name: newName.trim(), category: newCategory }),
      });
      await queryClient.invalidateQueries({ queryKey: ["templates"] });
      toast({
        title: "Template created",
        description: `“${template.name}” is ready. Use it to start a campaign.`,
      });
      closeNewTemplate(false);
    } catch (err: any) {
      toast({
        title: "Could not create template",
        description: err?.message ?? "Unknown error",
        variant: "destructive",
      });
    } finally {
      setCreating(false);
    }
  };

  const handleUseTemplate = async (template: Template) => {
    if (usingId) return;
    setUsingId(template.id);
    try {
      const campaign = await apiFetch<Campaign>("/campaigns", {
        method: "POST",
        body: JSON.stringify({
          name: template.name,
          template_id: template.id,
          content: template.design ?? { version: 1, blocks: [] },
        }),
      });
      toast({
        title: "Campaign created",
        description: "Add your subject line and content in the editor.",
      });
      router.push(`/campaigns/${campaign.id}/editor`);
    } catch (err: any) {
      toast({
        title: "Could not use template",
        description: err?.message ?? "Unknown error",
        variant: "destructive",
      });
      setUsingId(null);
    }
  };

  if (templatesQuery.isLoading) {
    return (
      <div className="space-y-6">
        <PageHeader
          eyebrow="Marketing"
          title="Templates"
          description="Loading your templates..."
        />
      </div>
    );
  }

  return (
    <div>
      <PageHeader
        eyebrow="Marketing"
        title="Templates"
        description="Reusable email designs your whole team can start from."
        actions={
          <button
            className="inline-flex h-9 items-center gap-1.5 rounded-lg bg-indigo-600 px-4 text-sm font-medium text-white shadow-sm hover:bg-indigo-500"
            onClick={() => setIsNewTemplateOpen(true)}
          >
            <Plus className="h-4 w-4" /> New template
          </button>
        }
      />

      {templatesQuery.error ? (
        <div className="mb-4 rounded-xl border border-rose-200 bg-rose-50 px-4 py-3 text-sm text-rose-800">
          Unable to load templates: {(templatesQuery.error as Error).message}
        </div>
      ) : null}

      <div className="mb-5 flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div className="flex flex-wrap gap-1">
          {categoryChips.map((item) => (
            <button
              key={item}
              onClick={() => setCategory(item)}
              className={cn(
                "rounded-lg px-3 py-1.5 text-xs font-medium",
                category === item
                  ? "bg-slate-900 text-white"
                  : "text-slate-600 hover:bg-white",
              )}
            >
              {item.charAt(0).toUpperCase() + item.slice(1)}
            </button>
          ))}
        </div>
        <div className="relative">
          <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-slate-400" />
          <input
            placeholder="Search templates"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="h-9 w-full rounded-lg border-0 bg-white pl-9 pr-3 text-sm ring-1 ring-inset ring-slate-200 placeholder:text-slate-400 focus:ring-2 focus:ring-inset focus:ring-indigo-500 sm:w-64"
          />
        </div>
      </div>

      <div className="grid gap-5 sm:grid-cols-2 xl:grid-cols-3">
        {filteredTemplates.length === 0 ? (
          <div className="col-span-3 flex flex-col items-center justify-center py-12 text-center">
            <LayoutTemplate className="mb-3 h-8 w-8 text-slate-300" />
            <p className="text-sm text-slate-500">
              {templates.length === 0
                ? "No templates yet. Create one or start from scratch."
                : "No templates match your filters."}
            </p>
          </div>
        ) : (
          filteredTemplates.map((template, index) => (
            <Card key={template.id}>
              <CardContent className="p-4">
                <TemplateThumb
                  accent={accents[index % accents.length]}
                  variant={variants[index % variants.length]}
                />
                <div className="mt-3 flex items-start justify-between gap-2">
                  <div>
                    <p className="text-sm font-semibold text-slate-900">{template.name}</p>
                    <p className="mt-0.5 text-xs text-slate-500">
                      Updated {formatDate(template.updated_at)} · {template.category}
                    </p>
                  </div>
                </div>
                <div className="mt-3">
                  <button
                    onClick={() => void handleUseTemplate(template)}
                    disabled={usingId !== null}
                    className="inline-flex h-8 w-full items-center justify-center gap-1.5 rounded-lg bg-slate-900 text-xs font-medium text-white hover:bg-slate-800 disabled:opacity-60"
                  >
                    {usingId === template.id ? (
                      <>
                        <Loader2 className="h-3.5 w-3.5 animate-spin" /> Creating campaign...
                      </>
                    ) : (
                      "Use template"
                    )}
                  </button>
                </div>
              </CardContent>
            </Card>
          ))
        )}

        <button
          onClick={() => setIsNewTemplateOpen(true)}
          className="flex min-h-[280px] flex-col items-center justify-center gap-2 rounded-xl border border-dashed border-slate-300 bg-white/60 p-6 text-center hover:border-indigo-300 hover:bg-white"
        >
          <span className="flex h-10 w-10 items-center justify-center rounded-lg bg-indigo-50 text-indigo-600">
            <Plus className="h-5 w-5" />
          </span>
          <span className="text-sm font-medium text-slate-900">Start from scratch</span>
          <span className="max-w-[180px] text-xs text-slate-500">
            Create a blank template, then use it to build a campaign in the editor.
          </span>
        </button>
      </div>

      <div className="mt-5 flex items-center justify-between rounded-xl border border-slate-200 bg-white px-5 py-4">
        <div className="flex items-center gap-3">
          <Badge variant="indigo">
            <Sparkles className="h-3 w-3" /> Tip
          </Badge>
          <p className="text-sm text-slate-600">
            Templates use the same block format as campaigns, so previews stay consistent.
          </p>
        </div>
      </div>

      <Dialog open={isNewTemplateOpen} onOpenChange={closeNewTemplate}>
        <DialogContent>
          <DialogTitle className="text-base font-semibold text-slate-900">New template</DialogTitle>
          <DialogDescription>
            Give your template a name and category. You can design its content when you use it in a
            campaign.
          </DialogDescription>
          <div className="space-y-3">
            <div>
              <label htmlFor="template-name" className="mb-1.5 block text-sm font-medium text-slate-700">
                Template name <span className="text-rose-500">*</span>
              </label>
              <Input
                id="template-name"
                placeholder="e.g. Monthly newsletter"
                value={newName}
                onChange={(e) => setNewName(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter") void handleCreateTemplate();
                }}
              />
            </div>
            <div>
              <label htmlFor="template-category" className="mb-1.5 block text-sm font-medium text-slate-700">
                Category
              </label>
              <Select
                id="template-category"
                value={newCategory}
                onChange={(e) => setNewCategory(e.target.value)}
              >
                {templateCategories.map((item) => (
                  <option key={item} value={item}>
                    {item.charAt(0).toUpperCase() + item.slice(1)}
                  </option>
                ))}
              </Select>
            </div>
          </div>
          <div className="flex items-center justify-end gap-2">
            <Button variant="secondary" onClick={() => closeNewTemplate(false)} disabled={creating}>
              Cancel
            </Button>
            <Button onClick={() => void handleCreateTemplate()} disabled={!newName.trim() || creating}>
              {creating ? "Creating..." : "Create template"}
            </Button>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  );
}
