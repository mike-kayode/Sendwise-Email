import type { MetadataRoute } from "next";

const siteURL = process.env.NEXT_PUBLIC_SITE_URL || "https://campaign.pipsleague.com";

export default function robots(): MetadataRoute.Robots {
  return {
    rules: { userAgent: "*", allow: "/" },
    sitemap: new URL("/sitemap.xml", siteURL).toString(),
  };
}
