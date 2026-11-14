import type { MetadataRoute } from "next";
import { legalSlugs } from "@/content/legal/documents";

const siteURL = process.env.NEXT_PUBLIC_SITE_URL || "https://sendwise.ng";

export default function sitemap(): MetadataRoute.Sitemap {
  const paths = [
    "/",
    "/platform",
    "/developers",
    "/pricing",
    "/about",
    "/contact",
    ...legalSlugs.map((slug) => `/legal/${slug}`),
  ];
  return paths.map((path) => ({
    url: new URL(path, siteURL).toString(),
    changeFrequency: "monthly",
    priority: path === "/" ? 1 : 0.7,
  }));
}
