"use client";

import { useEffect, useState } from "react";
import { ArrowDown, ArrowUp, Plus, Trash2 } from "lucide-react";
import { EmailPreview } from "@/components/email-preview";
import { Button } from "@/components/ui/button";
import { Input, Select, Textarea } from "@/components/ui/input";
import { apiFetch } from "@/lib/auth";
import { blocksToHtml, importEmailHtml, type EmailDesignBlock } from "@/lib/email-html-importer";

export type { EmailDesignBlock } from "@/lib/email-html-importer";

export type EmailDesignTheme = {
  background: string;
  content_background: string;
  text: string;
  heading: string;
  accent: string;
  button: string;
  link: string;
};

const defaultTheme: EmailDesignTheme = {
  background: "#f1f5f9",
  content_background: "#ffffff",
  text: "#334155",
  heading: "#0f172a",
  accent: "#4f46e5",
  button: "#4f46e5",
  link: "#4f46e5",
};

const blockTypes = [
  ["header", "Header / logo"],
  ["heading", "Heading"],
  ["paragraph", "Text"],
  ["image", "Image"],
  ["button", "Button"],
  ["divider", "Divider"],
  ["spacer", "Spacer"],
  ["columns-2", "2 columns"],
  ["columns-3", "3 columns"],
  ["quote", "Quote / testimonial"],
  ["social", "Social links"],
  ["footer", "Footer"],
] as const;

function createBlock(type: string): EmailDesignBlock {
  return {
    id: crypto.randomUUID(),
    type,
    content: type === "heading" ? "Your headline" : type === "paragraph" ? "Write your message here." : "",
    ...(type === "spacer" ? { size: "24" } : {}),
    ...(type === "header" ? { src: "/template-assets/neutral-logo.svg", alt: "Newsletter" } : {}),
    ...(type === "image" ? { src: "/template-assets/neutral-banner.svg", alt: "" } : {}),
    ...(type === "columns-2" ? { columns: [[{ id: crypto.randomUUID(), type: "paragraph", content: "First column" }], [{ id: crypto.randomUUID(), type: "paragraph", content: "Second column" }]] } : {}),
    ...(type === "columns-3" ? { columns: [1, 2, 3].map((number) => [{ id: crypto.randomUUID(), type: "paragraph", content: `Column ${number}` }]) } : {}),
  };
}

export function EmailDesignEditor({
  blocks,
  onChange,
  subject,
  preheader,
  mode,
  onModeChange,
  textContent,
  onTextContentChange,
  theme,
  onThemeChange,
}: {
  blocks: EmailDesignBlock[];
  onChange: (blocks: EmailDesignBlock[]) => void;
  subject: string;
  preheader?: string;
  mode: "plain" | "html";
  onModeChange: (mode: "plain" | "html") => void;
  textContent: string;
  onTextContentChange: (value: string) => void;
  theme: EmailDesignTheme;
  onThemeChange: (theme: EmailDesignTheme) => void;
}) {
  const [tab, setTab] = useState<"blocks" | "code">("blocks");
  const [previewViewport, setPreviewViewport] = useState<"desktop" | "mobile">("desktop");
  const [htmlCode, setHtmlCode] = useState("");
  const [draggedIndex, setDraggedIndex] = useState<number | null>(null);
  const [importError, setImportError] = useState<string | null>(null);
  const [uploadingImages, setUploadingImages] = useState<Record<string, boolean>>({});
  const [uploadErrors, setUploadErrors] = useState<Record<string, string>>({});

  useEffect(() => {
    if (tab === "code") setHtmlCode(blocksToHtml(blocks));
  }, [blocks, tab]);

  const updateBlock = (index: number, patch: Partial<EmailDesignBlock>) => {
    onChange(blocks.map((block, itemIndex) => itemIndex === index ? { ...block, ...patch } : block));
  };

  const updateColumnBlock = (
    blockIndex: number,
    columnIndex: number,
    itemIndex: number,
    patch: Partial<EmailDesignBlock>,
  ) => {
    const block = blocks[blockIndex];
    if (!block?.columns) return;
    const columns = block.columns.map((column, currentColumn) =>
      currentColumn !== columnIndex
        ? column
        : column.map((item, currentItem) =>
            currentItem === itemIndex ? { ...item, ...patch } : item,
          ),
    );
    updateBlock(blockIndex, { columns });
  };

  const uploadColumnImage = async (
    blockIndex: number,
    columnIndex: number,
    itemIndex: number,
    itemID: string,
    file: File,
  ) => {
    setUploadingImages((current) => ({ ...current, [itemID]: true }));
    setUploadErrors((current) => ({ ...current, [itemID]: "" }));
    try {
      const body = new FormData();
      body.append("file", file);
      const asset = await apiFetch<{ url: string }>("/assets", { method: "POST", body });
      updateColumnBlock(blockIndex, columnIndex, itemIndex, { src: asset.url });
    } catch (error) {
      setUploadErrors((current) => ({
        ...current,
        [itemID]: error instanceof Error ? error.message : "The image could not be uploaded.",
      }));
    } finally {
      setUploadingImages((current) => ({ ...current, [itemID]: false }));
    }
  };

  const moveBlock = (index: number, direction: -1 | 1) => {
    const target = index + direction;
    if (target < 0 || target >= blocks.length) return;
    const next = [...blocks];
    [next[index], next[target]] = [next[target], next[index]];
    onChange(next);
  };

  const reorder = (from: number, to: number) => {
    if (from === to || from < 0 || to < 0 || from >= blocks.length || to >= blocks.length) return;
    const next = [...blocks];
    const [item] = next.splice(from, 1);
    next.splice(to, 0, item);
    onChange(next);
  };

  const uploadImage = async (index: number, blockID: string, file: File) => {
    setUploadingImages((current) => ({ ...current, [blockID]: true }));
    setUploadErrors((current) => ({ ...current, [blockID]: "" }));
    try {
      const body = new FormData();
      body.append("file", file);
      const asset = await apiFetch<{ url: string }>("/assets", { method: "POST", body });
      updateBlock(index, { src: asset.url });
    } catch (error) {
      setUploadErrors((current) => ({
        ...current,
        [blockID]: error instanceof Error ? error.message : "The image could not be uploaded.",
      }));
    } finally {
      setUploadingImages((current) => ({ ...current, [blockID]: false }));
    }
  };

  return (
    <div className="space-y-5">
      <div className="grid gap-4 rounded-xl border border-slate-200 bg-white p-4 md:grid-cols-2">
        <label className="text-sm font-medium text-slate-700">
          Template mode
          <Select aria-label="Template mode" className="mt-1" value={mode} onChange={(event) => onModeChange(event.target.value as "plain" | "html")}>
            <option value="plain">Plain text</option>
            <option value="html">HTML</option>
          </Select>
        </label>
        <div>
          <h2 className="mb-2 text-sm font-medium text-slate-700">Email theme</h2>
          <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
            {(Object.keys(defaultTheme) as (keyof EmailDesignTheme)[]).map((key) => (
              <label key={key} className="text-xs capitalize text-slate-600">
                {key.replaceAll("_", " ")}
                <div className="mt-1 flex gap-1">
                  <Input aria-label={`${key} color picker`} type="color" className="h-9 w-10 p-1" value={theme[key]} onChange={(event) => onThemeChange({ ...theme, [key]: event.target.value })} />
                  <Input aria-label={`${key} color hex`} className="min-w-0 px-2 font-mono text-xs" value={theme[key]} onChange={(event) => onThemeChange({ ...theme, [key]: event.target.value })} />
                </div>
              </label>
            ))}
          </div>
        </div>
      </div>

      {mode === "plain" ? (
        <label className="block text-sm font-medium text-slate-700">
          Plain-text message
          <Textarea aria-label="Plain-text message" rows={12} className="mt-1 font-mono" value={textContent} onChange={(event) => onTextContentChange(event.target.value)} />
        </label>
      ) : (
        <>
          <div className="flex gap-2">
            <Button type="button" variant={tab === "blocks" ? "primary" : "secondary"} onClick={() => setTab("blocks")}>Blocks</Button>
            <Button type="button" variant={tab === "code" ? "primary" : "secondary"} onClick={() => setTab("code")}>HTML code</Button>
          </div>
          {tab === "code" ? (
            <div className="space-y-3">
              <label className="block text-sm font-medium text-slate-700">
                Import an HTML export
                <Input
                  aria-label="Import HTML file"
                  className="mt-1"
                  type="file"
                  accept=".html,.htm,text/html"
                  onChange={(event) => {
                    const file = event.currentTarget.files?.[0];
                    if (!file) return;
                    void file.text().then(setHtmlCode).catch((error: unknown) => {
                      setImportError(error instanceof Error ? error.message : "Could not read the HTML file.");
                    });
                    setImportError(null);
                  }}
                />
              </label>
              <label className="block text-sm font-medium text-slate-700">
                HTML source
                <Textarea aria-label="HTML source" rows={18} className="mt-1 font-mono text-xs" value={htmlCode} onChange={(event) => setHtmlCode(event.target.value)} />
              </label>
              {importError ? <p role="alert" className="text-sm text-rose-700">{importError}</p> : null}
              <Button type="button" variant="secondary" onClick={() => {
                onChange(importEmailHtml(htmlCode));
                setTab("blocks");
              }}>Convert HTML to editable blocks</Button>
            </div>
          ) : (
            <>
              <div className="flex flex-wrap gap-2">
                {blockTypes.map(([type, label]) => (
                  <Button key={type} type="button" variant="secondary" size="sm" onClick={() => onChange([...blocks, createBlock(type)])}>
                    <Plus className="h-3.5 w-3.5" /> Add {label.toLowerCase()}
                  </Button>
                ))}
              </div>
              {blocks.length === 0 ? (
                <div className="rounded-lg border border-dashed border-slate-300 p-8 text-center text-sm text-slate-500">
                  Add a content block to start designing this email.
                </div>
              ) : blocks.map((block, index) => (
                <section
                  key={block.id}
                  draggable
                  onDragStart={(event) => {
                    setDraggedIndex(index);
                    event.dataTransfer.effectAllowed = "move";
                  }}
                  onDragOver={(event) => event.preventDefault()}
                  onDrop={(event) => {
                    event.preventDefault();
                    if (draggedIndex !== null) reorder(draggedIndex, index);
                    setDraggedIndex(null);
                  }}
                  onDragEnd={() => setDraggedIndex(null)}
                  onKeyDown={(event) => {
                    if (event.altKey && event.key === "ArrowUp") {
                      event.preventDefault();
                      moveBlock(index, -1);
                    }
                    if (event.altKey && event.key === "ArrowDown") {
                      event.preventDefault();
                      moveBlock(index, 1);
                    }
                  }}
                  tabIndex={0}
                  aria-label={`Block ${index + 1}, ${block.type}. Use Alt and arrow keys to reorder.`}
                  className="space-y-3 rounded-xl border border-slate-200 bg-white p-4"
                >
                  <div className="flex items-center justify-between">
                    <h3 className="text-sm font-semibold capitalize text-slate-800">{index + 1}. {block.type.replaceAll("-", " ")}</h3>
                    <div className="flex items-center gap-1">
                      <Button type="button" variant="ghost" size="sm" className="h-8 w-8 px-0" aria-label={`Move block ${index + 1} up`} disabled={index === 0} onClick={() => moveBlock(index, -1)}><ArrowUp className="h-4 w-4" /></Button>
                      <Button type="button" variant="ghost" size="sm" className="h-8 w-8 px-0" aria-label={`Move block ${index + 1} down`} disabled={index === blocks.length - 1} onClick={() => moveBlock(index, 1)}><ArrowDown className="h-4 w-4" /></Button>
                      <Button type="button" variant="ghost" size="sm" className="h-8 w-8 px-0" aria-label={`Remove block ${index + 1}`} onClick={() => onChange(blocks.filter((_, itemIndex) => itemIndex !== index))}><Trash2 className="h-4 w-4 text-rose-600" /></Button>
                    </div>
                  </div>
                  {block.type !== "divider" && block.type !== "spacer" && block.type !== "image" && !block.type.startsWith("columns-") ? (
                    <label className="block text-xs font-medium text-slate-600">
                      {block.type === "custom_html" ? "Custom HTML (sanitized on send)" : "Content"}
                      <Textarea aria-label={`Block content ${index + 1}`} className="mt-1" rows={block.type === "heading" || block.type === "button" ? 2 : 4} value={block.content} onChange={(event) => updateBlock(index, { content: event.target.value })} />
                    </label>
                  ) : null}
                  {block.type === "button" || block.type === "social" ? (
                    <label className="block text-xs font-medium text-slate-600">
                      Link URL
                      <Input aria-label={`Button URL ${index + 1}`} className="mt-1" type="url" value={block.url ?? ""} onChange={(event) => updateBlock(index, { url: event.target.value })} />
                    </label>
                  ) : null}
                  {block.type === "image" || block.type === "header" ? (
                    <div className="grid gap-3 sm:grid-cols-2">
                      <label className="block text-xs font-medium text-slate-600">
                        Sendwise-hosted image path
                        <Input aria-label={`Image URL ${index + 1}`} className="mt-1" value={block.src ?? ""} onChange={(event) => updateBlock(index, { src: event.target.value })} />
                        <span className="mt-1 block">Upload an image to Sendwise storage. External image URLs are not used in sent messages.</span>
                        <Input
                          aria-label={`Upload image ${index + 1}`}
                          className="mt-2"
                          type="file"
                          accept="image/png,image/jpeg,image/gif"
                          disabled={uploadingImages[block.id] ?? false}
                          onChange={(event) => {
                            const file = event.currentTarget.files?.[0];
                            if (file) void uploadImage(index, block.id, file);
                            event.currentTarget.value = "";
                          }}
                        />
                        {uploadingImages[block.id] ? <span role="status">Uploading image…</span> : null}
                        {uploadErrors[block.id] ? <span role="alert" className="mt-1 block text-rose-700">{uploadErrors[block.id]}</span> : null}
                      </label>
                      <label className="block text-xs font-medium text-slate-600">
                        Alt text
                        <Input aria-label={`Image alt text ${index + 1}`} className="mt-1" value={block.alt ?? ""} onChange={(event) => updateBlock(index, { alt: event.target.value })} />
                      </label>
                    </div>
                  ) : null}
                  {block.type === "spacer" ? (
                    <label className="block text-xs font-medium text-slate-600">
                      Height (pixels)
                      <Input aria-label={`Spacer height ${index + 1}`} className="mt-1" type="number" min={1} max={320} value={block.size ?? "24"} onChange={(event) => updateBlock(index, { size: event.target.value })} />
                    </label>
                  ) : null}
                  {block.type === "columns-2" || block.type === "columns-3" ? (
                    <div className="grid gap-3 md:grid-cols-2">
                      {(block.columns ?? []).map((column, columnIndex) => (
                        <div key={`${block.id}-column-${columnIndex}`} className="space-y-3 rounded-lg border border-slate-200 p-3">
                          <h4 className="text-xs font-semibold text-slate-700">Column {columnIndex + 1}</h4>
                          {column.map((item, itemIndex) => {
                            const itemID = `${block.id}-${columnIndex}-${item.id}`;
                            return (
                              <div key={item.id} className="space-y-2 rounded border border-slate-100 p-2">
                                <Select
                                  aria-label={`Column block type ${index + 1}-${columnIndex + 1}-${itemIndex + 1}`}
                                  value={item.type}
                                  onChange={(event) => updateColumnBlock(index, columnIndex, itemIndex, { type: event.target.value })}
                                >
                                  <option value="heading">Heading</option>
                                  <option value="paragraph">Text</option>
                                  <option value="button">Button</option>
                                  <option value="image">Image</option>
                                  <option value="spacer">Spacer</option>
                                  <option value="social">Social links</option>
                                  <option value="custom_html">Custom HTML</option>
                                </Select>
                                {item.type !== "image" && item.type !== "spacer" ? (
                                  <Textarea
                                    aria-label={`Column content ${index + 1}-${columnIndex + 1}-${itemIndex + 1}`}
                                    rows={2}
                                    value={item.content}
                                    onChange={(event) => updateColumnBlock(index, columnIndex, itemIndex, { content: event.target.value })}
                                  />
                                ) : null}
                                {item.type === "button" || item.type === "social" ? (
                                  <Input
                                    aria-label={`Column link ${index + 1}-${columnIndex + 1}-${itemIndex + 1}`}
                                    type="url"
                                    value={item.url ?? ""}
                                    onChange={(event) => updateColumnBlock(index, columnIndex, itemIndex, { url: event.target.value })}
                                  />
                                ) : null}
                                {item.type === "image" ? (
                                  <>
                                    <Input
                                      aria-label={`Column image path ${index + 1}-${columnIndex + 1}-${itemIndex + 1}`}
                                      value={item.src ?? ""}
                                      onChange={(event) => updateColumnBlock(index, columnIndex, itemIndex, { src: event.target.value })}
                                    />
                                    <Input
                                      aria-label={`Upload column image ${index + 1}-${columnIndex + 1}-${itemIndex + 1}`}
                                      type="file"
                                      accept="image/png,image/jpeg,image/gif"
                                      disabled={uploadingImages[itemID] ?? false}
                                      onChange={(event) => {
                                        const file = event.currentTarget.files?.[0];
                                        if (file) void uploadColumnImage(index, columnIndex, itemIndex, itemID, file);
                                        event.currentTarget.value = "";
                                      }}
                                    />
                                    {uploadErrors[itemID] ? <p role="alert" className="text-xs text-rose-700">{uploadErrors[itemID]}</p> : null}
                                  </>
                                ) : null}
                                {item.type === "spacer" ? (
                                  <Input
                                    aria-label={`Column spacer height ${index + 1}-${columnIndex + 1}-${itemIndex + 1}`}
                                    type="number"
                                    min={1}
                                    max={320}
                                    value={item.size ?? "24"}
                                    onChange={(event) => updateColumnBlock(index, columnIndex, itemIndex, { size: event.target.value })}
                                  />
                                ) : null}
                                <Button
                                  type="button"
                                  variant="ghost"
                                  size="sm"
                                  onClick={() => updateBlock(index, {
                                    columns: (block.columns ?? []).map((items, currentColumn) =>
                                      currentColumn === columnIndex ? items.filter((_, currentItem) => currentItem !== itemIndex) : items,
                                    ),
                                  })}
                                >
                                  Remove column block
                                </Button>
                              </div>
                            );
                          })}
                          <Button
                            type="button"
                            variant="secondary"
                            size="sm"
                            onClick={() => updateBlock(index, {
                              columns: (block.columns ?? []).map((items, currentColumn) =>
                                currentColumn === columnIndex
                                  ? [...items, { id: crypto.randomUUID(), type: "paragraph", content: "Write your message here." }]
                                  : items,
                              ),
                            })}
                          >
                            Add text to column
                          </Button>
                        </div>
                      ))}
                    </div>
                  ) : null}
                  {block.type !== "divider" && block.type !== "spacer" ? (
                    <div className="grid gap-3 sm:grid-cols-3">
                      <label className="block text-xs font-medium text-slate-600">
                        Alignment
                        <Select aria-label={`Block alignment ${index + 1}`} className="mt-1" value={block.align ?? "left"} onChange={(event) => updateBlock(index, { align: event.target.value })}>
                          <option value="left">Left</option><option value="center">Center</option><option value="right">Right</option>
                        </Select>
                      </label>
                      <label className="block text-xs font-medium text-slate-600">
                        Background override
                        <Input aria-label={`Block background ${index + 1}`} className="mt-1 h-9" type="color" value={block.bg_color ?? "#ffffff"} onChange={(event) => updateBlock(index, { bg_color: event.target.value })} />
                      </label>
                      <label className="block text-xs font-medium text-slate-600">
                        Text override
                        <Input aria-label={`Block text color ${index + 1}`} className="mt-1 h-9" type="color" value={block.text_color ?? "#334155"} onChange={(event) => updateBlock(index, { text_color: event.target.value })} />
                      </label>
                    </div>
                  ) : null}
                </section>
              ))}
            </>
          )}
        </>
      )}
      <div>
        <div className="mb-2 flex items-center justify-between">
          <h2 className="text-sm font-semibold text-slate-800">Live preview</h2>
          <div className="flex gap-1">
            <Button type="button" size="sm" variant={previewViewport === "desktop" ? "primary" : "secondary"} onClick={() => setPreviewViewport("desktop")}>Desktop</Button>
            <Button type="button" size="sm" variant={previewViewport === "mobile" ? "primary" : "secondary"} onClick={() => setPreviewViewport("mobile")}>Mobile</Button>
          </div>
        </div>
        <EmailPreview subject={subject || "(no subject)"} preheader={preheader} blocks={blocks} theme={theme} textContent={mode === "plain" ? textContent : undefined} viewport={previewViewport} />
      </div>
    </div>
  );
}
