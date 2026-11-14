import type { Metadata } from "next";

const siteName = "Sendwise";
const siteURL = process.env.NEXT_PUBLIC_SITE_URL || "https://campaign.pipsleague.com";

export function marketingMetadata(title: string, description: string, path: string): Metadata {
  const canonical = path === "/" ? "/" : path.replace(/\/$/, "");
  return {
    title,
    description,
    alternates: { canonical },
    openGraph: {
      type: "website",
      siteName,
      title: `${title} · ${siteName}`,
      description,
      url: canonical,
    },
    twitter: {
      card: "summary",
      title: `${title} · ${siteName}`,
      description,
    },
    metadataBase: new URL(siteURL),
  };
}
