import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { LegalPage, getLegalSlugs } from "@/components/marketing/pages";
import { marketingMetadata } from "@/lib/marketing/metadata";
import { legalDocuments, type LegalSlug } from "@/content/legal/documents";

export function generateStaticParams() {
  return getLegalSlugs().map((slug) => ({ slug }));
}

export async function generateMetadata({ params }: { params: Promise<{ slug: string }> }): Promise<Metadata> {
  const { slug } = await params;
  if (!Object.hasOwn(legalDocuments, slug)) return {};
  const legalSlug = slug as LegalSlug;
  const document = legalDocuments[legalSlug];
  return marketingMetadata(document.title, document.intro, `/legal/${slug}`);
}

export default async function LegalDocumentPage({ params }: { params: Promise<{ slug: string }> }) {
  const { slug } = await params;
  if (!Object.hasOwn(legalDocuments, slug)) notFound();
  return <LegalPage slug={slug as LegalSlug} />;
}
