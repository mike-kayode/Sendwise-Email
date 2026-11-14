"use client";

import { useRouter } from "next/navigation";
import { useRef } from "react";
import { legalDocuments, legalSlugs, type LegalSlug } from "@/content/legal/documents";

export function LegalTabs({ activeSlug }: { activeSlug: LegalSlug }) {
  const router = useRouter();
  const tabs = useRef<Array<HTMLButtonElement | null>>([]);

  function onKeyDown(event: React.KeyboardEvent<HTMLButtonElement>, index: number) {
    const delta = event.key === "ArrowRight" ? 1 : event.key === "ArrowLeft" ? -1 : 0;
    if (!delta) return;
    event.preventDefault();
    const next = (index + delta + legalSlugs.length) % legalSlugs.length;
    tabs.current[next]?.focus();
    router.push(`/legal/${legalSlugs[next]}`);
  }

  return (
    <div className="tabs-light legal-tabs" role="tablist" aria-label="Legal documents">
      {legalSlugs.map((slug, index) => {
        const document = legalDocuments[slug];
        const selected = slug === activeSlug;
        return (
          <button
            ref={(node) => { tabs.current[index] = node; }}
            key={slug}
            id={`legal-tab-${slug}`}
            type="button"
            role="tab"
            aria-selected={selected}
            aria-controls="legal-document-panel"
            tabIndex={selected ? 0 : -1}
            className={`tl${selected ? " on" : ""}`}
            onClick={() => router.push(`/legal/${slug}`)}
            onKeyDown={(event) => onKeyDown(event, index)}
          >
            {document.label}
          </button>
        );
      })}
    </div>
  );
}
