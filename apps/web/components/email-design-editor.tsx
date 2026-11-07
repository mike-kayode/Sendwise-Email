"use client";

import { ArrowDown, ArrowUp, Plus, Trash2 } from "lucide-react";
import { EmailPreview } from "@/components/email-preview";
import { Button } from "@/components/ui/button";
import { Input, Select, Textarea } from "@/components/ui/input";

export type EmailDesignBlock = {
  id: string;
  type: string;
  content: string;
  url?: string;
  src?: string;
  alt?: string;
  align?: string;
  size?: string;
};

const blockTypes = [
  ["heading", "Heading"],
  ["paragraph", "Paragraph"],
  ["button", "Button"],
  ["image", "Image"],
  ["divider", "Divider"],
  ["spacer", "Spacer"],
  ["footer", "Footer"],
] as const;

function createBlock(type: string): EmailDesignBlock {
  return {
    id: crypto.randomUUID(),
    type,
    content: type === "heading" ? "Your headline" : "",
    ...(type === "spacer" ? { size: "24" } : {}),
  };
}

export function EmailDesignEditor({
  blocks,
  onChange,
  subject,
  preheader,
}: {
  blocks: EmailDesignBlock[];
  onChange: (blocks: EmailDesignBlock[]) => void;
  subject: string;
  preheader?: string;
}) {
  const updateBlock = (index: number, patch: Partial<EmailDesignBlock>) => {
    onChange(blocks.map((block, itemIndex) => itemIndex === index ? { ...block, ...patch } : block));
  };

  const moveBlock = (index: number, direction: -1 | 1) => {
    const target = index + direction;
    if (target < 0 || target >= blocks.length) return;
    const next = [...blocks];
    [next[index], next[target]] = [next[target], next[index]];
    onChange(next);
  };

  return (
    <div className="grid gap-5 xl:grid-cols-2">
      <div className="space-y-4">
        <div className="flex flex-wrap gap-2">
          {blockTypes.map(([type, label]) => (
            <Button
              key={type}
              type="button"
              variant="secondary"
              size="sm"
              onClick={() => onChange([...blocks, createBlock(type)])}
            >
              <Plus className="h-3.5 w-3.5" /> Add {label.toLowerCase()}
            </Button>
          ))}
        </div>
        {blocks.length === 0 ? (
          <div className="rounded-lg border border-dashed border-slate-300 p-8 text-center text-sm text-slate-500">
            Add a content block to start designing this email.
          </div>
        ) : blocks.map((block, index) => (
          <section key={block.id} className="space-y-3 rounded-xl border border-slate-200 bg-white p-4">
            <div className="flex items-center justify-between">
              <h3 className="text-sm font-semibold capitalize text-slate-800">
                {index + 1}. {block.type}
              </h3>
              <div className="flex items-center gap-1">
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  className="h-8 w-8 px-0"
                  aria-label={`Move block ${index + 1} up`}
                  disabled={index === 0}
                  onClick={() => moveBlock(index, -1)}
                >
                  <ArrowUp className="h-4 w-4" />
                </Button>
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  className="h-8 w-8 px-0"
                  aria-label={`Move block ${index + 1} down`}
                  disabled={index === blocks.length - 1}
                  onClick={() => moveBlock(index, 1)}
                >
                  <ArrowDown className="h-4 w-4" />
                </Button>
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  className="h-8 w-8 px-0"
                  aria-label={`Remove block ${index + 1}`}
                  onClick={() => onChange(blocks.filter((_, itemIndex) => itemIndex !== index))}
                >
                  <Trash2 className="h-4 w-4 text-rose-600" />
                </Button>
              </div>
            </div>
            {block.type !== "divider" && block.type !== "spacer" ? (
              <label className="block text-xs font-medium text-slate-600">
                Content
                <Textarea
                  aria-label={`Block content ${index + 1}`}
                  className="mt-1"
                  rows={block.type === "heading" || block.type === "button" ? 2 : 4}
                  value={block.content}
                  onChange={(event) => updateBlock(index, { content: event.target.value })}
                />
              </label>
            ) : null}
            {block.type === "button" ? (
              <label className="block text-xs font-medium text-slate-600">
                Button URL
                <Input
                  aria-label={`Button URL ${index + 1}`}
                  className="mt-1"
                  type="url"
                  value={block.url ?? ""}
                  onChange={(event) => updateBlock(index, { url: event.target.value })}
                />
              </label>
            ) : null}
            {block.type === "image" ? (
              <div className="grid gap-3 sm:grid-cols-2">
                <label className="block text-xs font-medium text-slate-600">
                  Image URL
                  <Input
                    aria-label={`Image URL ${index + 1}`}
                    className="mt-1"
                    type="url"
                    value={block.src ?? ""}
                    onChange={(event) => updateBlock(index, { src: event.target.value })}
                  />
                </label>
                <label className="block text-xs font-medium text-slate-600">
                  Alt text
                  <Input
                    aria-label={`Image alt text ${index + 1}`}
                    className="mt-1"
                    value={block.alt ?? ""}
                    onChange={(event) => updateBlock(index, { alt: event.target.value })}
                  />
                </label>
              </div>
            ) : null}
            {block.type === "spacer" ? (
              <label className="block text-xs font-medium text-slate-600">
                Height (pixels)
                <Input
                  aria-label={`Spacer height ${index + 1}`}
                  className="mt-1"
                  type="number"
                  min={1}
                  max={320}
                  value={block.size ?? "24"}
                  onChange={(event) => updateBlock(index, { size: event.target.value })}
                />
              </label>
            ) : null}
            {block.type !== "divider" && block.type !== "spacer" ? (
              <label className="block text-xs font-medium text-slate-600">
                Alignment
                <Select
                  aria-label={`Block alignment ${index + 1}`}
                  className="mt-1"
                  value={block.align ?? "left"}
                  onChange={(event) => updateBlock(index, { align: event.target.value })}
                >
                  <option value="left">Left</option>
                  <option value="center">Center</option>
                  <option value="right">Right</option>
                </Select>
              </label>
            ) : null}
          </section>
        ))}
      </div>
      <div>
        <h2 className="mb-2 text-sm font-semibold text-slate-800">Live preview</h2>
        <EmailPreview
          subject={subject || "(no subject)"}
          preheader={preheader}
          blocks={blocks}
        />
      </div>
    </div>
  );
}
