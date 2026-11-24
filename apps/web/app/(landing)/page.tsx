import type { Metadata } from "next";
import { AuthenticatedHomeRedirect } from "@/components/marketing/authenticated-home-redirect";
import { LandingPage } from "@/components/marketing/landing-v2/landing-page";
import { marketingMetadata } from "@/lib/marketing/metadata";

export const metadata: Metadata = marketingMetadata(
  "Automated marketing that fuels growth",
  "Simplify your journey to success - from exploring leads to turning them into loyal customers.",
  "/",
);

export default function MarketingHome() {
  return (
    <>
      <AuthenticatedHomeRedirect />
      <LandingPage />
    </>
  );
}
