type EmailBlock = {
  id: string;
  type: string;
  content: string;
  url?: string;
  src?: string;
};

const defaultTemplateBlocks: EmailBlock[] = [
  { id: "heading-1", type: "heading", content: "A better way to stay in touch" },
  { id: "paragraph-1", type: "paragraph", content: "Automations, faster imports, and a refreshed template library make your next send easier to launch and easier to trust." },
  { id: "button-1", type: "button", content: "View updates" },
  { id: "paragraph-2", type: "paragraph", content: "Keep your audience moving with smarter sequencing, clearer reporting, and a more consistent brand experience." },
  { id: "divider-1", type: "divider", content: "" },
  { id: "footer-1", type: "footer", content: "Acme Studio · 12 Marina Road, Lagos · Unsubscribe" },
];

export function EmailPreview({
  blocks = defaultTemplateBlocks,
  subject = "A better way to stay in touch",
  preheader = "Automations, faster imports, and a refreshed template library.",
}: {
  blocks?: EmailBlock[];
  subject?: string;
  preheader?: string;
}) {
  return (
    <div className="overflow-hidden rounded-xl border border-slate-200 bg-white">
      <div className="border-b border-slate-100 bg-slate-50 px-4 py-2.5">
        <p className="truncate text-xs text-slate-500">
          <span className="font-medium text-slate-700">Subject:</span> {subject}
        </p>
        <p className="mt-0.5 truncate text-[11px] text-slate-400">{preheader}</p>
      </div>
      <div className="bg-white px-8 py-8">
        <div className="email-canvas mx-auto max-w-md text-sm text-slate-700">
          {blocks.map((block) => {
            if (block.type === "heading") {
              return <h1 key={block.id}>{block.content}</h1>;
            }
            if (block.type === "paragraph") {
              return <p key={block.id}>{block.content}</p>;
            }
            if (block.type === "button") {
              return (
                <p key={block.id} className="my-5">
                  <span className="inline-block rounded-lg bg-indigo-600 px-5 py-2.5 text-sm font-medium text-white">
                    {block.content}
                  </span>
                </p>
              );
            }
            if (block.type === "divider") {
              return <hr key={block.id} className="my-6 border-slate-200" />;
            }
            if (block.type === "spacer") {
              return <div key={block.id} className="h-6" />;
            }
            if (block.type === "image") {
              return (
                <div
                  key={block.id}
                  className="mb-5 flex h-40 items-center justify-center rounded-lg bg-gradient-to-br from-indigo-100 via-slate-50 to-cyan-100 text-xs font-medium text-indigo-400"
                >
                  {block.content}
                </div>
              );
            }
            if (block.type === "footer") {
              return (
                <p key={block.id} className="mt-8 border-t border-slate-100 pt-4 text-[11px] text-slate-400">
                  {block.content}
                </p>
              );
            }
            return null;
          })}
        </div>
      </div>
      <div className="border-t border-slate-100 bg-slate-50 px-4 py-2.5 text-center text-[11px] text-slate-400">
        Acme Studio · 12 Marina Road, Lagos · Unsubscribe
      </div>
    </div>
  );
}
