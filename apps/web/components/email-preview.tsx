"use client";

import { useEffect, useState } from "react";
import { apiUrl } from "@/lib/api-url";

type EmailBlock = {
  id: string;
  type: string;
  content: string;
  url?: string;
  src?: string;
  alt?: string;
  columns?: EmailBlock[][];
  bg_color?: string;
  text_color?: string;
};

type EmailTheme = {
  background: string;
  content_background: string;
  text: string;
  heading: string;
  accent: string;
  button: string;
  link: string;
};

const defaultTheme: EmailTheme = {
  background: "#f1f5f9",
  content_background: "#ffffff",
  text: "#334155",
  heading: "#0f172a",
  accent: "#4f46e5",
  button: "#4f46e5",
  link: "#4f46e5",
};

const allowedHTMLTags = new Set([
  "a", "b", "blockquote", "br", "caption", "center", "col", "colgroup",
  "div", "em", "h1", "h2", "h3", "h4", "h5", "h6", "hr", "i", "li",
  "ol", "p", "span", "strong", "table", "tbody", "td", "th", "thead",
  "tr", "u", "ul",
]);
const droppedHTMLTags = new Set([
  "audio", "base", "button", "embed", "form", "iframe", "input", "link",
  "meta", "object", "script", "style", "svg", "template", "video",
]);
const allowedHTMLAttributes = new Set([
  "align", "border", "cellpadding", "cellspacing", "colspan", "height",
  "role", "rowspan", "valign", "width",
]);
const allowedCSSProperties = new Set([
  "background-color", "border", "border-collapse", "border-radius", "color",
  "display", "font-family", "font-size", "font-weight", "height", "line-height",
  "margin", "max-width", "padding", "text-align", "text-decoration",
  "vertical-align", "width",
]);

function renderableImportedHTML(source: string): string {
  const parsed = new DOMParser().parseFromString(source, "text/html");
  const sanitizeNode = (node: ChildNode) => {
    if (!(node instanceof Element)) return;
    const tag = node.tagName.toLowerCase();
    if (droppedHTMLTags.has(tag)) {
      node.remove();
      return;
    }

    for (const attribute of Array.from(node.attributes)) {
      if (attribute.name === "style") {
        const style = document.createElement("span").style;
        style.cssText = attribute.value;
        const safeStyle = Array.from(style).flatMap((property) => {
          const value = style.getPropertyValue(property);
          return allowedCSSProperties.has(property) && !/url\s*\(|expression\s*\(|javascript:/i.test(value)
            ? [`${property}: ${value}`]
            : [];
        }).join("; ");
        if (safeStyle) node.setAttribute("style", safeStyle);
        else node.removeAttribute("style");
        continue;
      }

      const isAnchorAttribute = tag === "a" && (attribute.name === "href" || attribute.name === "title");
      if (!allowedHTMLAttributes.has(attribute.name) && !isAnchorAttribute) {
        node.removeAttribute(attribute.name);
      } else if (attribute.name === "href") {
        const href = attribute.value.trim();
        if (!/^(https?:|mailto:)/i.test(href)) node.removeAttribute(attribute.name);
      }
    }

    for (const child of Array.from(node.childNodes)) sanitizeNode(child);
    if (!allowedHTMLTags.has(tag)) node.replaceWith(...Array.from(node.childNodes));
  };

  for (const child of Array.from(parsed.body.childNodes)) sanitizeNode(child);
  return parsed.body.innerHTML;
}

function importedHTMLPreviewDocument(source: string): string {
  const safeHTML = renderableImportedHTML(source);
  return `<!doctype html><html><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; img-src 'none'; font-src 'none'; connect-src 'none'; form-action 'none'; base-uri 'none'; script-src 'none'"><meta name="viewport" content="width=device-width, initial-scale=1"><style>html,body{margin:0;padding:0;background:#fff;color:#334155;font:14px/1.6 Arial,sans-serif}body{box-sizing:border-box;padding:16px;overflow-wrap:anywhere}a{pointer-events:none}</style></head><body>${safeHTML}</body></html>`;
}

const emptyPreviewDocument = "<!doctype html><html><head><meta charset=\"utf-8\"></head><body></body></html>";

export function EmailPreview({
  blocks = [],
  subject = "A better way to stay in touch",
  preheader = "Automations, faster imports, and a refreshed template library.",
  theme = defaultTheme,
  textContent,
  viewport = "desktop",
}: {
  blocks?: EmailBlock[];
  subject?: string;
  preheader?: string;
  theme?: EmailTheme;
  textContent?: string;
  viewport?: "desktop" | "mobile";
}) {
  const [customHTMLPreviews, setCustomHTMLPreviews] = useState<Record<string, string>>({});

  useEffect(() => {
    setCustomHTMLPreviews(Object.fromEntries(
      blocks
        .filter((block) => block.type === "custom_html")
        .map((block) => [block.id, importedHTMLPreviewDocument(block.content)]),
    ));
  }, [blocks]);

  return (
    <div className={`mx-auto overflow-hidden rounded-xl border border-slate-200 bg-white ${viewport === "mobile" ? "max-w-[360px]" : "max-w-[760px]"}`}>
      <div className="border-b border-slate-100 bg-slate-50 px-4 py-2.5">
        <p className="truncate text-xs text-slate-500">
          <span className="font-medium text-slate-700">Subject:</span> {subject}
        </p>
        <p className="mt-0.5 truncate text-[11px] text-slate-400">{preheader}</p>
      </div>
      <div className="px-8 py-8" style={{ backgroundColor: theme.background }}>
        <div className="email-canvas mx-auto max-w-md space-y-3 p-6 text-sm" style={{ backgroundColor: theme.content_background, color: theme.text }}>
          {textContent !== undefined ? <p className="whitespace-pre-wrap">{textContent}</p> : null}
          {blocks.map((block) => {
            if (block.type === "heading") {
              return <h1 key={block.id} style={{ color: block.text_color ?? theme.heading, backgroundColor: block.bg_color }}>{block.content}</h1>;
            }
            if (block.type === "paragraph" || block.type === "text") {
              return <p key={block.id} style={{ color: block.text_color ?? theme.text, backgroundColor: block.bg_color }}>{block.content}</p>;
            }
            if (block.type === "button") {
              return (
                <p key={block.id} className="my-5" style={{ textAlign: "center" }}>
                  <span className="inline-block rounded-lg px-5 py-2.5 text-sm font-medium text-white" style={{ backgroundColor: block.bg_color ?? theme.button }}>
                    {block.content}
                  </span>
                </p>
              );
            }
            if (block.type === "header" || block.type === "logo" || block.type === "image") {
              const isBundledAsset = block.src?.startsWith("/template-assets/");
              const isUploadedAsset = block.src?.startsWith("/api/v1/public/assets/");
              return isBundledAsset || isUploadedAsset
                ? <img key={block.id} src={isUploadedAsset ? apiUrl(block.src ?? "") : block.src} alt={block.alt ?? ""} className="mx-auto max-h-40 max-w-full" />
                : <div key={block.id} className="rounded bg-slate-100 p-4 text-center text-xs text-slate-500">Image asset must be hosted by Sendwise</div>;
            }
            if (block.type === "divider") {
              return <hr key={block.id} className="my-6 border-slate-200" />;
            }
            if (block.type === "spacer") {
              return <div key={block.id} style={{ height: `${Math.min(320, Math.max(1, Number(block.content) || 24))}px` }} />;
            }
            if (block.type === "footer") {
              return (
                <p key={block.id} className="mt-8 border-t border-slate-100 pt-4 text-[11px] text-slate-400">
                  {block.content}
                </p>
              );
            }
            if (block.type === "columns-2" || block.type === "columns-3") {
              return <div key={block.id} className="grid gap-3" style={{ gridTemplateColumns: `repeat(${block.columns?.length ?? 2}, minmax(0, 1fr))` }}>
                {(block.columns ?? []).map((column, index) => <div key={`${block.id}-${index}`} className="space-y-2">{column.map((item) => <p key={item.id}>{item.content}</p>)}</div>)}
              </div>;
            }
            if (block.type === "quote" || block.type === "testimonial") {
              return <blockquote key={block.id} className="border-l-4 pl-4 italic" style={{ borderColor: theme.accent }}>{block.content}</blockquote>;
            }
            if (block.type === "social") return <p key={block.id} style={{ color: theme.link }}>{block.content} · social link</p>;
            if (block.type === "custom_html") {
              return (
                <iframe
                  key={block.id}
                  title="HTML content preview"
                  sandbox=""
                  srcDoc={customHTMLPreviews[block.id] ?? emptyPreviewDocument}
                  className="min-h-[320px] w-full rounded border border-slate-200 bg-white"
                />
              );
            }
            return null;
          })}
        </div>
      </div>
    </div>
  );
}
