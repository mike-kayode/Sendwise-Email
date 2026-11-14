import type { Metadata } from "next";
import { AboutPage } from "@/components/marketing/pages";
import { marketingMetadata } from "@/lib/marketing/metadata";

export const metadata: Metadata = marketingMetadata(
  "About",
  "Learn about Sendwise's approach to email infrastructure, deliverability and sender reputation.",
  "/about",
);

export default AboutPage;
