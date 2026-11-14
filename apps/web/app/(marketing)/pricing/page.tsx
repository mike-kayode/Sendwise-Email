import type { Metadata } from "next";
import { PricingPage } from "@/components/marketing/pages";
import { marketingMetadata } from "@/lib/marketing/metadata";

export const metadata: Metadata = marketingMetadata(
  "Pricing",
  "Compare Sendwise plans for campaign and transactional email.",
  "/pricing",
);

export default PricingPage;
