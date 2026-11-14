import type { Metadata } from "next";
import { ContactPage } from "@/components/marketing/pages";
import { marketingMetadata } from "@/lib/marketing/metadata";

export const metadata: Metadata = marketingMetadata(
  "Contact",
  "Contact the Sendwise team about plans, migration, domain verification or transactional email.",
  "/contact",
);

export default ContactPage;
