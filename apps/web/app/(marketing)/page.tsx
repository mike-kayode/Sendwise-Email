import type { Metadata } from "next";
import { HomePage } from "@/components/marketing/pages";
import { AuthenticatedHomeRedirect } from "@/components/marketing/authenticated-home-redirect";
import { marketingMetadata } from "@/lib/marketing/metadata";

export const metadata: Metadata = marketingMetadata(
  "Email that reaches the inbox",
  "Send campaigns and application email from your own domain with verified DNS and delivery tracking.",
  "/",
);

export default function MarketingHome() {
  return (
    <>
      <AuthenticatedHomeRedirect />
      <HomePage />
    </>
  );
}
