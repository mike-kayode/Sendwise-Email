import type { MetadataRoute } from "next";

const siteURL = process.env.NEXT_PUBLIC_SITE_URL || "https://campaign.pipsleague.com";

export default function sitemap(): MetadataRoute.Sitemap {
  const paths = [
    "/",
    "/platform",
    "/developers",
    "/pricing",
    "/about",
    "/contact",
  ];
  return paths.map((path) => ({
    url: new URL(path, siteURL).toString(),
    changeFrequency: "monthly",
    priority: path === "/" ? 1 : 0.7,
  }));
}
