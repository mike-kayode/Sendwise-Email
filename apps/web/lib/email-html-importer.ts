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
