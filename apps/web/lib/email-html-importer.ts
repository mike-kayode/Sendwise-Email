export type EmailDesignBlock = {
  id: string;
  type: string;
  content: string;
  url?: string;
  src?: string;
  alt?: string;
  align?: string;
  size?: string;
  columns?: EmailDesignBlock[][];
  bg_color?: string;
  text_color?: string;
};

const neutralImage = "/template-assets/neutral-banner.svg";

export function importEmailHtml(source: string): EmailDesignBlock[] {
  const normalized = source
    .replace(/\bFlowi\b/gi, "{{organization}}")
    .replace(/https?:\/\/[^"'()\s;]*(?:cloudfront\.net|amazonaws\.com)[^"'()\s;]*/gi, neutralImage)
    .replace(/((?:src|background)\s*=\s*["'])[^"']*(?:cloudfront\.net|amazonaws\.com)[^"']*(["'])/gi, `$1${neutralImage}$2`);
  const document = new DOMParser().parseFromString(normalized, "text/html");

  const parseChildren = (parent: ParentNode): EmailDesignBlock[] => {
    const blocks: EmailDesignBlock[] = [];
    for (const node of Array.from(parent.childNodes)) {
      if (node.nodeType !== Node.ELEMENT_NODE) continue;
      const element = node as HTMLElement;
      const tag = element.tagName.toLowerCase();
      const id = crypto.randomUUID();
      const content = element.textContent?.trim() ?? "";
      const align = element.getAttribute("align") ?? element.style.textAlign ?? "left";
      if (tag === "table") {
        const rows = Array.from(element.querySelectorAll(":scope > tbody > tr, :scope > tr"));
        for (const row of rows) {
          const cells = Array.from(row.querySelectorAll(":scope > td, :scope > th"));
          if (cells.length === 2 || cells.length === 3) {
            blocks.push({
              id,
              type: `columns-${cells.length}`,
              content: "",
              columns: cells.map((cell) => parseChildren(cell)),
            });
          } else {
            blocks.push(...parseChildren(row));
          }
        }
        if (rows.length === 0) blocks.push(...parseChildren(element));
        continue;
      }
      if (/^h[1-6]$/.test(tag)) {
        blocks.push({ id, type: "heading", content, align });
      } else if (tag === "p") {
        blocks.push({ id, type: "paragraph", content, align });
      } else if (tag === "img") {
        const src = element.getAttribute("src") ?? "";
        blocks.push({
          id,
          type: "image",
          content: "",
          src: /^(?:https?:)?\/\//i.test(src) ? neutralImage : src || neutralImage,
          alt: element.getAttribute("alt") ?? "",
          align,
        });
      } else if (tag === "a" && (element.getAttribute("role") === "button" || /button/i.test(element.className))) {
        blocks.push({ id, type: "button", content, url: element.getAttribute("href") ?? "", align });
      } else if (tag === "a") {
        const imageAlt = element.querySelector("img")?.getAttribute("alt") ?? "";
        const socialLabel = element.getAttribute("aria-label") ?? imageAlt;
        if (/facebook|instagram|linkedin|twitter|youtube|tiktok|threads/i.test(socialLabel)) {
          blocks.push({ id, type: "social", content: socialLabel, url: element.getAttribute("href") ?? "", align });
        } else {
          const children = parseChildren(element);
          if (children.length) blocks.push(...children);
          else if (content) blocks.push({ id, type: "custom_html", content: element.outerHTML });
        }
      } else if (tag === "hr") {
        blocks.push({ id, type: "divider", content: "" });
      } else if (tag === "br" || (tag === "div" && element.style.height)) {
        blocks.push({ id, type: "spacer", content: "", size: element.style.height?.replace(/[^\d]/g, "") || "24" });
      } else if (tag === "blockquote") {
        blocks.push({ id, type: "quote", content });
      } else if (tag === "div" || tag === "section" || tag === "ul" || tag === "ol") {
        const children = parseChildren(element);
        if (children.length) blocks.push(...children);
        else if (content) blocks.push({ id, type: "custom_html", content: element.outerHTML });
      } else if (content || element.outerHTML) {
        blocks.push({ id, type: "custom_html", content: element.outerHTML });
      }
    }
    return blocks;
  };

  return parseChildren(document.body);
}

export function blocksToHtml(blocks: EmailDesignBlock[]): string {
  return blocks.map((block) => {
    const content = escapeHtml(block.content);
    switch (block.type) {
      case "heading": return `<h1>${content}</h1>`;
      case "paragraph": return `<p>${content}</p>`;
      case "button": return `<a role="button" href="${escapeHtml(block.url ?? "")}">${content}</a>`;
      case "image": return `<img src="${escapeHtml(block.src ?? "")}" alt="${escapeHtml(block.alt ?? "")}">`;
      case "header":
      case "logo": return `<img src="${escapeHtml(block.src ?? "")}" alt="${escapeHtml(block.alt ?? "")}">`;
      case "divider": return "<hr>";
      case "spacer": return `<div style="height:${escapeHtml(block.size ?? "24")}px"></div>`;
      case "custom_html": return block.content;
      case "columns-2":
      case "columns-3":
        return `<table><tr>${(block.columns ?? []).map((column) => `<td>${blocksToHtml(column)}</td>`).join("")}</tr></table>`;
      case "quote": return `<blockquote>${content}</blockquote>`;
      case "social": return `<a href="${escapeHtml(block.url ?? "")}">${content}</a>`;
      case "footer": return `<p>${content}</p>`;
      default: return `<p>${content}</p>`;
    }
  }).join("\n");
}

function escapeHtml(value: string): string {
  return value.replace(/[&<>"']/g, (character) => ({
    "&": "&amp;",
    "<": "&lt;",
    ">": "&gt;",
    "\"": "&quot;",
    "'": "&#39;",
  })[character] ?? character);
}
