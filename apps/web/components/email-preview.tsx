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
            if (block.type === "custom_html") return <pre key={block.id} className="overflow-auto whitespace-pre-wrap rounded bg-slate-50 p-2 text-xs">{block.content}</pre>;
            return null;
          })}
        </div>
      </div>
    </div>
  );
}
