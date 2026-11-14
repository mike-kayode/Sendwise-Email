export type BillingPeriod = "Monthly" | "Yearly";

export type Plan = {
  name: "Starter" | "Growth" | "Scale";
  audience: string;
  monthlyPrice: string;
  yearlyPrice: string;
  emails: string;
  domains: string;
  logs: string;
  features: string[];
  action: string;
  href: string;
  featured?: boolean;
};

export const plans: Plan[] = [
  {
    name: "Starter",
    audience: "[WHO IT IS FOR]",
    monthlyPrice: "[PRICE]",
    yearlyPrice: "[PRICE]",
    emails: "[X]",
    domains: "[X]",
    logs: "[X]",
    features: ["[X] emails per month", "[X] sending domains", "REST API and SMTP", "Email logs for [X] days", "Community support"],
    action: "Start with Starter",
    href: "/register",
  },
  {
    name: "Growth",
    audience: "[WHO IT IS FOR]",
    monthlyPrice: "[PRICE]",
    yearlyPrice: "[PRICE]",
    emails: "[X]",
    domains: "[X]",
    logs: "[X]",
    features: ["[X] emails per month", "[X] sending domains", "Webhooks and templates", "Email logs for [X] days", "Priority support"],
    action: "Start with Growth",
    href: "/register",
    featured: true,
  },
  {
    name: "Scale",
    audience: "[WHO IT IS FOR]",
    monthlyPrice: "[PRICE]",
    yearlyPrice: "[PRICE]",
    emails: "[X]",
    domains: "Unlimited",
    logs: "[X]",
    features: ["[X] emails per month", "Unlimited sending domains", "Dedicated sending options", "Extended log retention", "Onboarding and account review"],
    action: "Talk to sales",
    href: "/contact",
  },
];

export const planComparison = [
  ["Monthly emails", "[X]", "[X]", "[X]"],
  ["Sending domains", "[X]", "[X]", "Unlimited"],
  ["Sender addresses", "[X]", "[X]", "Unlimited"],
  ["Campaign editor and templates", "Included", "Included", "Included"],
  ["REST API and SMTP", "Included", "Included", "Included"],
  ["Webhooks", "—", "Included", "Included"],
  ["Suppression list and bounce handling", "Included", "Included", "Included"],
  ["Log retention", "[X] days", "[X] days", "[X] days"],
  ["Support", "Community", "Priority", "Dedicated"],
] as const;

export const pricingFAQs = [
  {
    question: "What counts as one email?",
    answer: "[DEFINE: one message to one recipient, including each recipient of a batch]",
  },
  {
    question: "What happens if I go over my monthly limit?",
    answer: "[DEFINE: overage pricing, or sending pauses until the next period]",
  },
  {
    question: "Can I change plans later?",
    answer: "[DEFINE: upgrade and downgrade rules and how billing is prorated]",
  },
  {
    question: "Which payment methods do you accept?",
    answer: "[LIST: cards, bank transfer and any local options you support]",
  },
] as const;
