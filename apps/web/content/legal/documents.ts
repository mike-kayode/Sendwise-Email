export const legalDocuments = {
  privacy: {
    slug: "privacy",
    label: "Privacy",
    title: "Privacy policy",
    intro: "How Sendwise collects, uses and protects personal information.",
    sections: [
      "Information we collect",
      "How we use information",
      "Information in your workspace",
      "Sharing and processors",
      "Retention and deletion",
      "Your rights",
      "Contact",
    ],
  },
  terms: {
    slug: "terms",
    label: "Terms",
    title: "Terms of service",
    intro: "The agreement between you and Sendwise when you use the platform.",
    sections: [
      "Your account",
      "Plans, billing and refunds",
      "Your content and data",
      "Service availability",
      "Suspension and termination",
      "Liability",
      "Changes to these terms",
    ],
  },
  "acceptable-use": {
    slug: "acceptable-use",
    label: "Acceptable use",
    title: "Acceptable use policy",
    intro: "What you may and may not send with Sendwise.",
    sections: [
      "Permission and consent",
      "Prohibited content",
      "List quality and bounce limits",
      "Complaint thresholds",
      "Enforcement",
      "Reporting abuse",
    ],
  },
  "data-protection": {
    slug: "data-protection",
    label: "Data protection",
    title: "Data protection (NDPR)",
    intro: "How Sendwise handles personal data under applicable data protection law.",
    sections: [
      "Roles: controller and processor",
      "Lawful basis and consent",
      "Security measures",
      "Cross-border transfers",
      "Data subject requests",
      "Breach notification",
    ],
  },
} as const;

export type LegalSlug = keyof typeof legalDocuments;
export const legalSlugs = Object.keys(legalDocuments) as LegalSlug[];
export const legalTextPlaceholder = "[LEGAL TEXT: written or reviewed by your lawyer]";
