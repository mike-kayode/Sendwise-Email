import { apiFetch } from "@/lib/auth";
import { apiUrl } from "@/lib/api-url";

type ImportedAsset = { url: string };
const maxExternalImagesPerSave = 40;

function replaceImageSourceAttributes(source: string, replacements: Map<string, string>): string {
  return source.replace(/<img\b[^>]*>/gi, (tag) =>
    tag.replace(/(\s)src\s*=\s*(["'])(.*?)\2/i, (attribute, whitespace: string, quote: string, value: string) => {
      const replacement = replacements.get(value.trim());
      if (!replacement) return attribute;
      const escaped = replacement.replaceAll("&", "&amp;").replaceAll(quote, quote === '"' ? "&quot;" : "&#39;");
      return `${whitespace}src=${quote}${escaped}${quote}`;
    }),
  );
}

export async function rehostExternalImages(source: string): Promise<{
  html: string;
  warnings: string[];
  replacements: Record<string, string>;
}> {
  const parsed = new DOMParser().parseFromString(source, "text/html");
  const imageSources = [...new Set(
    Array.from(parsed.querySelectorAll("img[src]"))
      .map((image) => image.getAttribute("src")?.trim() ?? "")
      .filter(Boolean),
  )];
  const localAssetOrigins = new Set([
    window.location.origin,
    new URL(apiUrl("/api/v1/public/assets/preview"), window.location.href).origin,
  ]);
  const sources: string[] = [];
  const warnings: string[] = [];
  for (const imageSource of imageSources) {
    let parsedSource: URL;
    try {
      parsedSource = new URL(imageSource, window.location.href);
    } catch {
      warnings.push(imageSource);
      continue;
    }
    const isLocalAsset = localAssetOrigins.has(parsedSource.origin) &&
      (parsedSource.pathname.startsWith("/api/v1/public/assets/") ||
        parsedSource.pathname.startsWith("/template-assets/"));
    if (isLocalAsset) continue;
    if (parsedSource.protocol === "https:" || parsedSource.protocol === "http:") {
      sources.push(imageSource);
    } else {
      warnings.push(imageSource);
    }
  }
  const replacements = new Map<string, string>();
  warnings.push(...sources.slice(maxExternalImagesPerSave));

  for (let index = 0; index < Math.min(sources.length, maxExternalImagesPerSave); index += 5) {
    await Promise.all(sources.slice(index, Math.min(index + 5, maxExternalImagesPerSave)).map(async (imageSource) => {
      try {
        const asset = await apiFetch<ImportedAsset>("/assets/import", {
          method: "POST",
          body: JSON.stringify({ url: imageSource.startsWith("//") ? `https:${imageSource}` : imageSource }),
        });
        replacements.set(imageSource, asset.url);
      } catch {
        warnings.push(imageSource);
      }
    }));
  }

  if (replacements.size === 0) {
    return {
      html: source,
      warnings: imageSources.filter((imageSource) => warnings.includes(imageSource)),
      replacements: {},
    };
  }

  const orderedWarnings = imageSources.filter((imageSource) => warnings.includes(imageSource));
  return {
    html: replaceImageSourceAttributes(source, replacements),
    warnings: orderedWarnings,
    replacements: Object.fromEntries(replacements),
  };
}

export function rewriteAssetReferences(value: unknown, replacements: Record<string, string>): unknown {
  if (typeof value === "string") return replacements[value] ?? value;
  if (Array.isArray(value)) return value.map((item) => rewriteAssetReferences(item, replacements));
  if (typeof value === "object" && value !== null) {
    return Object.fromEntries(
      Object.entries(value).map(([key, item]) => [key, rewriteAssetReferences(item, replacements)]),
    );
  }
  return value;
}

export function hasComplianceContent(source: string): boolean {
  return /\b(unsubscribe|opt[\s-]?out|mailing address|postal address|street address)\b/i.test(source);
}
