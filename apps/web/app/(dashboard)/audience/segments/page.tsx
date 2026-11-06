"use client";

import { useQuery } from "@tanstack/react-query";
import { Filter, Plus, Trash2 } from "lucide-react";
import { useState } from "react";
import { PageHeader } from "@/components/page-header";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Table, Tbody, Td, Th, Thead, Tr } from "@/components/ui/table";
import { apiFetch, type Contact, type ListResponse } from "@/lib/auth";
import type { SegmentCondition, SegmentField, SegmentGroup, SegmentOperator, SegmentRules } from "@/lib/segment-rules";
import { formatNumber } from "@/lib/utils";
import { useToast } from "@/components/ui/use-toast";

type Segment = {
  id: string;
  name: string;
  description: string;
  rules: SegmentRules;
  contact_count: number;
  created_at: string;
};

type AudienceTag = { id: string; name: string };
type AudienceList = { id: string; name: string };
type Preview = { count: number; data: Contact[] };
type BuilderField = SegmentField | "custom_fields";

const initialGroup = (): SegmentGroup => ({ match: "AND", conditions: [] });

function operatorsFor(field: SegmentField): SegmentOperator[] {
  if (field === "created_at") return ["before", "after"];
  if (field === "tag" || field === "list") return ["equals", "not_equals"];
  return ["equals", "not_equals", "contains", "does_not_contain", "is_empty", "is_not_empty"];
}

function conditionLabel(condition: SegmentCondition): string {
  return `${condition.field} ${condition.operator.replaceAll("_", " ")}${condition.value ? ` "${condition.value}"` : ""}`;
}

export default function SegmentsPage() {
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [groups, setGroups] = useState<SegmentGroup[]>([initialGroup()]);
  const [activeGroup, setActiveGroup] = useState(0);
  const [field, setField] = useState<BuilderField>("tag");
  const [operator, setOperator] = useState<SegmentOperator>("equals");
  const [value, setValue] = useState("");
  const [customFieldKey, setCustomFieldKey] = useState("");
  const [preview, setPreview] = useState<Preview | null>(null);
  const [saving, setSaving] = useState(false);
  const { toast } = useToast();

  const segmentsQuery = useQuery({
    queryKey: ["segments"],
    queryFn: () => apiFetch<ListResponse<Segment>>("/segments?limit=50"),
  });
  const tagsQuery = useQuery({
    queryKey: ["tags"],
    queryFn: () => apiFetch<ListResponse<AudienceTag>>("/tags?limit=200"),
  });
  const listsQuery = useQuery({
    queryKey: ["lists"],
    queryFn: () => apiFetch<ListResponse<AudienceList>>("/lists?limit=200"),
  });

  const tags = tagsQuery.data?.data ?? [];
  const lists = listsQuery.data?.data ?? [];
  const valueRequired = operator !== "is_empty" && operator !== "is_not_empty";
  const rules: SegmentRules = { groups };

  const changeField = (next: BuilderField) => {
    setField(next);
    const nextOperator = operatorsFor(next === "custom_fields" ? "custom_fields.field" : next)[0];
    setOperator(nextOperator);
    setValue("");
    setPreview(null);
  };

  const addGroup = () => {
    setGroups((current) => [...current, initialGroup()]);
    setActiveGroup(groups.length);
    setPreview(null);
  };

  const addCondition = () => {
    const normalizedField = field === "custom_fields" ? `custom_fields.${customFieldKey.trim()}` : field;
    if (normalizedField.startsWith("custom_fields.") && !/^custom_fields\.[A-Za-z][A-Za-z0-9_]{0,62}$/.test(normalizedField)) {
      toast({ title: "Invalid custom field", description: "Use a field key beginning with a letter and containing only letters, numbers, or underscores.", variant: "destructive" });
      return;
    }
    if (valueRequired && !value.trim()) return;
    let normalizedValue = value.trim();
    if (field === "created_at" && valueRequired) {
      const timestamp = new Date(value);
      if (Number.isNaN(timestamp.getTime())) {
        toast({ title: "Invalid date", description: "Enter a valid date and time.", variant: "destructive" });
        return;
      }
      normalizedValue = timestamp.toISOString();
    }
    const condition: SegmentCondition = {
      field: normalizedField as SegmentField,
      operator,
      ...(valueRequired ? { value: normalizedValue } : {}),
    };
    setGroups((current) => current.map((group, index) =>
      index === activeGroup ? { ...group, conditions: [...group.conditions, condition] } : group,
    ));
    setValue("");
    setPreview(null);
  };

  const removeCondition = (groupIndex: number, conditionIndex: number) => {
    setGroups((current) => current.map((group, index) =>
      index === groupIndex ? { ...group, conditions: group.conditions.filter((_, itemIndex) => itemIndex !== conditionIndex) } : group,
    ));
    setPreview(null);
  };

  const previewRules = async () => {
    try {
      const result = await apiFetch<Preview>("/segments/preview", {
        method: "POST",
        body: JSON.stringify({ rules }),
      });
      setPreview(result);
    } catch (error) {
      toast({ title: "Preview failed", description: error instanceof Error ? error.message : "The segment rules could not be evaluated.", variant: "destructive" });
    }
  };

  const saveSegment = async () => {
    if (!name.trim() || groups.some((group) => group.conditions.length === 0) || saving) return;
    setSaving(true);
    try {
      await apiFetch("/segments", {
        method: "POST",
        body: JSON.stringify({ name: name.trim(), description: description.trim(), rules }),
      });
      setName("");
      setDescription("");
      setGroups([initialGroup()]);
      setActiveGroup(0);
      setPreview(null);
      await segmentsQuery.refetch();
      toast({ title: "Segment created", description: "Your saved segment is ready." });
    } catch (error) {
      toast({ title: "Could not save segment", description: error instanceof Error ? error.message : "The segment could not be created.", variant: "destructive" });
    } finally {
      setSaving(false);
    }
  };

  const deleteSegment = async (segment: Segment) => {
    if (!window.confirm(`Delete "${segment.name}"?`)) return;
    try {
      await apiFetch(`/segments/${segment.id}`, { method: "DELETE" });
      await segmentsQuery.refetch();
    } catch (error) {
      toast({ title: "Could not delete segment", description: error instanceof Error ? error.message : "The segment could not be deleted.", variant: "destructive" });
    }
  };

  const changeGroupMatch = (index: number, match: SegmentGroup["match"]) => {
    setGroups((current) => current.map((group, groupIndex) => groupIndex === index ? { ...group, match } : group));
    setPreview(null);
  };

  if (segmentsQuery.isLoading) {
    return <PageHeader eyebrow="Audience" title="Segments" description="Loading your segments..." />;
  }

  const segments = segmentsQuery.data?.data ?? [];

  return (
    <div>
      <PageHeader
        eyebrow="Audience"
        title="Segments"
        description="Build dynamic groups from contact fields, tags, and list membership."
        actions={<Button variant="secondary" onClick={() => document.getElementById("segment-name")?.focus()}><Plus className="h-4 w-4" />New segment</Button>}
      />

      {segmentsQuery.error ? (
        <div role="alert" className="mb-4 flex items-center justify-between rounded-xl border border-rose-200 bg-rose-50 px-4 py-3 text-sm text-rose-800">
          <span>Unable to load segments: {(segmentsQuery.error as Error).message}</span>
          <Button variant="secondary" onClick={() => void segmentsQuery.refetch()}>Retry</Button>
        </div>
      ) : null}

      <div className="grid gap-5 lg:grid-cols-3">
        <Card className="lg:col-span-2">
          <div className="border-b border-slate-100 px-5 py-4">
            <h2 className="text-sm font-semibold">Saved segments</h2>
            <p className="text-xs text-slate-500">Contact totals are evaluated from the current rules.</p>
          </div>
          <Table>
            <Thead><Tr><Th>Segment</Th><Th>Rules</Th><Th className="text-right">Contacts</Th><Th /></Tr></Thead>
            <Tbody>
              {segments.length === 0 ? (
                <Tr><Td colSpan={4} className="py-8 text-center text-slate-500">No segments yet. Build your first segment.</Td></Tr>
              ) : segments.map((segment) => (
                <Tr key={segment.id}>
                  <Td>
                    <div className="flex items-center gap-2.5">
                      <span className="flex h-8 w-8 items-center justify-center rounded-lg bg-indigo-50 text-indigo-600"><Filter className="h-3.5 w-3.5" /></span>
                      <div><p className="font-medium text-slate-900">{segment.name}</p><p className="text-xs text-slate-500">{segment.description || "—"}</p></div>
                    </div>
                  </Td>
                  <Td className="max-w-[260px] text-xs text-slate-600">
                    {(segment.rules?.groups ?? []).flatMap((group) => group.conditions.map(conditionLabel)).join(" · ") || "Invalid rules"}
                  </Td>
                  <Td className="text-right tabular-nums">{formatNumber(segment.contact_count)}</Td>
                  <Td className="text-right">
                    <button type="button" aria-label={`Delete ${segment.name}`} onClick={() => void deleteSegment(segment)} className="rounded p-1 text-slate-500 hover:bg-rose-50 hover:text-rose-700"><Trash2 className="h-4 w-4" /></button>
                  </Td>
                </Tr>
              ))}
            </Tbody>
          </Table>
        </Card>

        <Card className="h-fit">
          <CardContent className="space-y-4 p-5">
            <div>
              <h2 className="text-sm font-semibold">Build a segment</h2>
              <p className="mt-1 text-xs text-slate-500">Conditions within a group use its AND/OR rule. Groups are combined with AND.</p>
            </div>
            <Input id="segment-name" placeholder="Segment name" value={name} onChange={(event) => setName(event.target.value)} />
            <Input aria-label="Segment description" placeholder="Description (optional)" value={description} onChange={(event) => setDescription(event.target.value)} />
            {tagsQuery.error ? (
              <div role="alert" className="flex items-center justify-between text-xs text-rose-700">
                <span>Unable to load tags.</span><Button variant="secondary" size="sm" onClick={() => void tagsQuery.refetch()}>Retry</Button>
              </div>
            ) : null}
            {listsQuery.error ? (
              <div role="alert" className="flex items-center justify-between text-xs text-rose-700">
                <span>Unable to load lists.</span><Button variant="secondary" size="sm" onClick={() => void listsQuery.refetch()}>Retry</Button>
              </div>
            ) : null}

            {groups.map((group, groupIndex) => (
              <div key={groupIndex} className="space-y-2 rounded-lg border border-slate-200 p-3">
                <div className="flex items-center justify-between">
                  <span className="text-xs font-medium text-slate-600">Group {groupIndex + 1}</span>
                  {groups.length > 1 ? (
                    <button type="button" aria-label={`Remove group ${groupIndex + 1}`} className="text-xs text-rose-600" onClick={() => {
                      setGroups((current) => current.filter((_, index) => index !== groupIndex));
                      setActiveGroup((current) => current === groupIndex ? Math.max(0, groupIndex - 1) : current > groupIndex ? current - 1 : current);
                      setPreview(null);
                    }}>Remove group</button>
                  ) : null}
                  <button type="button" className={`text-xs ${activeGroup === groupIndex ? "font-semibold text-indigo-600" : "text-slate-500"}`} onClick={() => setActiveGroup(groupIndex)}>
                    {activeGroup === groupIndex ? "Editing" : "Add conditions here"}
                  </button>
                </div>
                <label className="flex items-center gap-2 text-xs text-slate-600">
                  Match conditions
                  <select value={group.match} onChange={(event) => changeGroupMatch(groupIndex, event.target.value as SegmentGroup["match"])} className="h-8 rounded border border-slate-200 bg-white px-2">
                    <option value="AND">ALL (AND)</option><option value="OR">ANY (OR)</option>
                  </select>
                </label>
                {group.conditions.map((condition, conditionIndex) => (
                  <div key={`${condition.field}-${conditionIndex}`} className="flex items-center justify-between gap-2 rounded bg-slate-50 px-2 py-1.5 text-xs">
                    <span className="truncate">{conditionLabel(condition)}</span>
                    <button type="button" aria-label={`Remove condition ${conditionLabel(condition)}`} onClick={() => removeCondition(groupIndex, conditionIndex)} className="text-slate-500">×</button>
                  </div>
                ))}
              </div>
            ))}

            <div className="space-y-2 rounded-lg bg-slate-50 p-3">
              <select aria-label="Condition field" value={field} onChange={(event) => changeField(event.target.value as BuilderField)} className="h-9 w-full rounded border border-slate-200 bg-white px-2 text-sm">
                <option value="email">Email</option>
                <option value="status">Status</option>
                <option value="tag">Tag</option>
                <option value="list">List</option>
                <option value="created_at">Created at</option>
                <option value="custom_fields">Custom field</option>
              </select>
              {field === "custom_fields" ? (
                <Input aria-label="Custom field key" placeholder="Custom field key (e.g. city)" value={customFieldKey} onChange={(event) => setCustomFieldKey(event.target.value)} />
              ) : null}
              {field === "tag" ? (
                <select aria-label="Condition value" value={value} onChange={(event) => setValue(event.target.value)} className="h-9 w-full rounded border border-slate-200 bg-white px-2 text-sm">
                  <option value="">Choose a tag</option>{tags.map((tag) => <option key={tag.id} value={tag.name}>{tag.name}</option>)}
                </select>
              ) : field === "list" ? (
                <select aria-label="Condition value" value={value} onChange={(event) => setValue(event.target.value)} className="h-9 w-full rounded border border-slate-200 bg-white px-2 text-sm">
                  <option value="">Choose a list</option>{lists.map((list) => <option key={list.id} value={list.name}>{list.name}</option>)}
                </select>
              ) : null}
              <select aria-label="Condition operator" value={operator} onChange={(event) => { setOperator(event.target.value as SegmentOperator); setValue(""); setPreview(null); }} className="h-9 w-full rounded border border-slate-200 bg-white px-2 text-sm">
                {operatorsFor(field === "custom_fields" ? "custom_fields.field" : field).map((item) => <option key={item} value={item}>{item.replaceAll("_", " ")}</option>)}
              </select>
              {valueRequired && field !== "tag" && field !== "list" ? (
                <Input
                  aria-label="Condition value"
                  type={field === "created_at" ? "datetime-local" : "text"}
                  value={value}
                  onChange={(event) => setValue(event.target.value)}
                  placeholder={field === "created_at" ? "" : "Value"}
                />
              ) : null}
              <Button variant="secondary" className="w-full" onClick={addCondition} disabled={valueRequired && !value.trim()}>Add condition</Button>
            </div>
            <div className="flex gap-2">
              <Button variant="secondary" className="flex-1" onClick={addGroup}>Add group</Button>
              <Button variant="secondary" className="flex-1" onClick={() => void previewRules()} disabled={groups.some((group) => group.conditions.length === 0)}>Preview</Button>
            </div>
            {preview ? (
              <div className="rounded-lg border border-indigo-100 bg-indigo-50 p-3 text-sm">
                <p className="font-medium text-indigo-900">Estimated {preview.count} contacts</p>
                {preview.data.length > 0 ? <ul className="mt-2 space-y-1 text-xs text-indigo-800">{preview.data.slice(0, 10).map((contact) => <li key={contact.id}>{contact.email}</li>)}</ul> : <p className="mt-1 text-xs text-indigo-700">No contacts match these rules.</p>}
              </div>
            ) : null}
            <Button className="w-full" onClick={() => void saveSegment()} disabled={!name.trim() || groups.some((group) => group.conditions.length === 0) || saving}>
              {saving ? "Saving..." : "Save segment"}
            </Button>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
