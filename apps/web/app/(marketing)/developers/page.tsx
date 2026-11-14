import type { Metadata } from "next";
import { DevelopersPage } from "@/components/marketing/pages";
import { marketingMetadata } from "@/lib/marketing/metadata";

export const metadata: Metadata = marketingMetadata(
  "Developers",
  "Integrate Sendwise transactional email with a REST API, SMTP, signed webhooks and delivery logs.",
  "/developers",
);

export default DevelopersPage;
