export type BillingPeriod = "Monthly" | "Yearly";

export type Plan = {
  name: "Free" | "Starter" | "Business" | "Enterprise";
  audience: string;
  monthlyPrice: string;
  yearlyPrice?: string;
  emails: string;
  dailyEmails: string;
  features: string[];
  action: string;
  href: string;
  featured?: boolean;
};

export const plans: Plan[] = [
  {
    name: "Free",
    audience: "Try the platform with no monthly charge.",
    monthlyPrice: "₦3,000",
    yearlyPrice: "₦3,000",
    emails: "3,000",
    dailyEmails: "100",
    features: ["3,000 emails per month", "Up to 100 emails per day", "Campaigns and transactional email", "REST API and SMTP"],
    action: "Start free",
    href: "/register",
  },
  {
    name: "Starter",
    audience: "For teams building their email program.",
    monthlyPrice: "₦10,000",
    emails: "10,000",
    dailyEmails: "₦5,000",
    features: ["10,000 emails per month", "Campaigns and transactional email", "REST API and SMTP"],
    action: "Choose Starter",
    href: "/register",
  },
  {
    name: "Business",
    audience: "For growing teams sending at scale.",
    monthlyPrice: "₦50,000",
    emails: "50,000",
    dailyEmails: "₦15,000",
    features: ["50,000 emails per month", "Campaigns and transactional email", "REST API and SMTP"],
    action: "Choose Business",
    href: "/register",
    featured: true,
  },
  {
    name: "Enterprise",
    audience: "For high-volume email programs.",
    monthlyPrice: "₦200,000",
    emails: "200,000",
    dailyEmails: "₦50,000",
    features: ["200,000 emails per month", "Campaigns and transactional email", "REST API and SMTP", "Talk with our team about your needs"],
    action: "Contact sales",
    href: "/contact",
  },
];

export const planComparison = [
  ["Emails per month", "3,000", "10,000", "50,000", "200,000"],
  ["Daily email limit", "100", "₦5,000", "₦15,000", "₦50,000"],
  ["Campaigns and transactional email", "Included", "Included", "Included", "Included"],
  ["REST API and SMTP", "Included", "Included", "Included", "Included"],
  ["Pay-as-you-go terms", "Contact us", "Contact us", "Contact us", "Contact us"],
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
    answer: "Prices shown are monthly. Contact us to confirm annual pricing; no yearly discount is assumed here.",
  },
  {
    question: "Can I change plans later?",
    answer: "Contact Sendwise to discuss changing your plan and billing.",
  },
  {
    question: "Which payment methods do you accept?",
    answer: "Contact our team to confirm the payment methods currently available.",
  },
] as const;
