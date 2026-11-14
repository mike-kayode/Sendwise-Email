export type Plan = {
  name: "Free" | "Starter" | "Business" | "Enterprise";
  audience: string;
  monthlyPrice: string;
  emails: string;
  features: string[];
  action: string;
  href: string;
  featured?: boolean;
};

export const plans: Plan[] = [
  {
    name: "Free",
    audience: "Try the platform with no monthly charge.",
    monthlyPrice: "0",
    emails: "3,000",
    features: ["3,000 emails per month", "Up to 100 emails per day", "Campaigns and transactional email", "REST API and SMTP"],
    action: "Start free",
    href: "/register",
  },
  {
    name: "Starter",
    audience: "For teams building their email program.",
    monthlyPrice: "5,000",
    emails: "10,000",
    features: ["10,000 emails per month", "Daily limits are managed per sending domain", "Campaigns and transactional email", "REST API and SMTP"],
    action: "Ask about Starter",
    href: "/contact",
  },
  {
    name: "Business",
    audience: "For growing teams sending at scale.",
    monthlyPrice: "15,000",
    emails: "50,000",
    features: ["50,000 emails per month", "Daily limits are managed per sending domain", "Campaigns and transactional email", "REST API and SMTP"],
    action: "Ask about Business",
    href: "/contact",
    featured: true,
  },
  {
    name: "Enterprise",
    audience: "For high-volume email programs.",
    monthlyPrice: "50,000",
    emails: "200,000",
    features: ["200,000 emails per month", "Daily limits are managed per sending domain", "Campaigns and transactional email", "REST API and SMTP"],
    action: "Contact sales",
    href: "/contact",
  },
];

export const planComparison = [
  ["Emails per month", "3,000", "10,000", "50,000", "200,000"],
  ["Daily sending limit", "Up to 100", "Managed per sending domain", "Managed per sending domain", "Managed per sending domain"],
  ["Campaigns and transactional email", "Included", "Included", "Included", "Included"],
  ["REST API and SMTP", "Included", "Included", "Included", "Included"],
] as const;

export const pricingFAQs = [
  {
    question: "What is included in the Free plan?",
    answer: "The Free plan includes up to 3,000 emails per month, with a daily limit of 100 emails.",
  },
  {
    question: "Can I use pay-as-you-go pricing?",
    answer: "Contact us to discuss usage-based pricing and the current terms for your sending volume.",
  },
  {
    question: "Do you offer yearly billing?",
    answer: "Only monthly prices are published. Contact us to discuss current annual billing availability before choosing a plan.",
  },
  {
    question: "Can I change plans later?",
    answer: "Contact Sendwise to discuss changing your plan and billing.",
  },
  {
    question: "Which payment methods do you accept?",
    answer: "Automatic billing is not configured in the app. Contact us to confirm the current payment and plan-activation process.",
  },
] as const;
