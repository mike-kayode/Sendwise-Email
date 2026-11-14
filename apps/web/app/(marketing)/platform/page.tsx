import type { Metadata } from "next";
import { PlatformPage } from "@/components/marketing/pages";
import { marketingMetadata } from "@/lib/marketing/metadata";

export const metadata: Metadata = marketingMetadata(
  "Platform",
  "Campaigns, audience management, templates, sending domains and delivery tracking in one workspace.",
  "/platform",
);

export default PlatformPage;
