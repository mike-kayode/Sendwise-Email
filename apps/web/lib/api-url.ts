const API_BASE_URL = (process.env.NEXT_PUBLIC_API_URL ?? "").replace(/\/+$/, "");

export function apiUrl(path: string, baseUrl = API_BASE_URL): string {
  const base = baseUrl.replace(/\/+$/, "");
  // Same-origin by default: when no NEXT_PUBLIC_API_URL is set, use a
  // relative path so the browser resolves it against the current origin.
  const prefix = base === "" ? "" : base;
  return `${prefix}/${path.replace(/^\/+/, "")}`;
}
