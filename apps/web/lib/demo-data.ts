export type CampaignStatus =
  | "draft"
  | "scheduled"
  | "sending"
  | "completed"
  | "paused"
  | "cancelled";

export type DemoCampaign = {
  id: string;
  name: string;
  subject: string;
  status: CampaignStatus;
  audience: string;
  recipients: number;
  openRate: number;
  clickRate: number;
  date: string;
};

export type DemoContact = {
  id: string;
  name: string;
  email: string;
  status: "subscribed" | "pending" | "unsubscribed" | "bounced" | "complained";
  tags: string[];
  source: string;
  joined: string;
};

export const workspace = {
  name: "Acme Studio",
  plan: "Growth",
  sender: "Acme Studio <hello@acme.studio>",
  members: 5,
};

export const currentUser = {
  name: "Kola Adeniyi",
  email: "kola@acme.studio",
  role: "Owner",
};

export const dashboardStats = {
  contacts: 24892,
  contactsChange: 12.4,
  sent: 184320,
  sentChange: 8.1,
  openRate: 42.8,
  openRateChange: 2.3,
  clickRate: 6.2,
  clickRateChange: -0.4,
  unsubscribes: 148,
  complaints: 6,
};

export const growthSeries = [
  { label: "May", value: 12400 },
  { label: "Jun", value: 14600 },
  { label: "Jul", value: 15900 },
  { label: "Aug", value: 18200 },
  { label: "Sep", value: 20500 },
  { label: "Oct", value: 22400 },
  { label: "Nov", value: 24892 },
];

export const engagementSeries = [
  { label: "W1", opens: 42, clicks: 6 },
  { label: "W2", opens: 47, clicks: 7 },
  { label: "W3", opens: 39, clicks: 5 },
  { label: "W4", opens: 52, clicks: 8 },
  { label: "W5", opens: 48, clicks: 7 },
  { label: "W6", opens: 55, clicks: 9 },
  { label: "W7", opens: 51, clicks: 8 },
  { label: "W8", opens: 58, clicks: 10 },
];

export const campaigns: DemoCampaign[] = [
  {
    id: "cmp_01",
    name: "November product digest",
    subject: "New: automations, segments, and faster imports",
    status: "completed",
    audience: "Newsletter subscribers",
    recipients: 18420,
    openRate: 48.2,
    clickRate: 8.4,
    date: "2026-11-01T09:30:00Z",
  },
  {
    id: "cmp_02",
    name: "Black Friday preview",
    subject: "Early access starts in 24 hours",
    status: "scheduled",
    audience: "Customers",
    recipients: 6420,
    openRate: 0,
    clickRate: 0,
    date: "2026-11-04T08:00:00Z",
  },
  {
    id: "cmp_03",
    name: "Onboarding week 1",
    subject: "Three ways to get more from Sendwise",
    status: "completed",
    audience: "New signups",
    recipients: 2840,
    openRate: 61.4,
    clickRate: 14.2,
    date: "2026-10-29T10:15:00Z",
  },
  {
    id: "cmp_04",
    name: "October customer stories",
    subject: "How three teams send smarter",
    status: "completed",
    audience: "Newsletter subscribers",
    recipients: 17890,
    openRate: 44.7,
    clickRate: 6.9,
    date: "2026-10-24T09:00:00Z",
  },
  {
    id: "cmp_05",
    name: "Webinar invitation",
    subject: "Build your first automation with us",
    status: "draft",
    audience: "Trial accounts",
    recipients: 0,
    openRate: 0,
    clickRate: 0,
    date: "2026-11-02T14:20:00Z",
  },
  {
    id: "cmp_06",
    name: "Win-back series · email 1",
    subject: "We saved your spot",
    status: "paused",
    audience: "Lapsed customers",
    recipients: 1340,
    openRate: 31.5,
    clickRate: 4.1,
    date: "2026-10-30T16:00:00Z",
  },
  {
    id: "cmp_07",
    name: "Holiday shipping deadlines",
    subject: "Order by these dates to arrive on time",
    status: "completed",
    audience: "Customers",
    recipients: 5970,
    openRate: 52.1,
    clickRate: 11.8,
    date: "2026-10-18T09:45:00Z",
  },
];

export const contacts: DemoContact[] = [
  {
    id: "ct_01",
    name: "Amara Okafor",
    email: "amara@northstar.co",
    status: "subscribed",
    tags: ["customer", "vip"],
    source: "Website form",
    joined: "2026-10-28T12:00:00Z",
  },
  {
    id: "ct_02",
    name: "Daniel Brooks",
    email: "daniel@brightpath.io",
    status: "subscribed",
    tags: ["newsletter"],
    source: "CSV import",
    joined: "2026-10-27T09:30:00Z",
  },
  {
    id: "ct_03",
    name: "Sofia Martins",
    email: "sofia@lumen.design",
    status: "subscribed",
    tags: ["customer"],
    source: "Checkout",
    joined: "2026-10-25T15:10:00Z",
  },
  {
    id: "ct_04",
    name: "Kai Mensah",
    email: "kai@orbitlabs.dev",
    status: "pending",
    tags: ["trial"],
    source: "API",
    joined: "2026-10-24T11:20:00Z",
  },
  {
    id: "ct_05",
    name: "Priya Raman",
    email: "priya@fieldnote.app",
    status: "subscribed",
    tags: ["customer", "newsletter"],
    source: "Website form",
    joined: "2026-10-22T08:05:00Z",
  },
  {
    id: "ct_06",
    name: "Liam Chen",
    email: "liam@studiofive.co",
    status: "unsubscribed",
    tags: ["newsletter"],
    source: "CSV import",
    joined: "2026-10-20T17:40:00Z",
  },
  {
    id: "ct_07",
    name: "Noah Williams",
    email: "noah@atlasworks.com",
    status: "bounced",
    tags: [],
    source: "CSV import",
    joined: "2026-10-16T10:00:00Z",
  },
  {
    id: "ct_08",
    name: "Zara Ahmed",
    email: "zara@meridian.io",
    status: "subscribed",
    tags: ["vip"],
    source: "Referral",
    joined: "2026-10-14T13:30:00Z",
  },
];

export const lists = [
  { id: "lst_01", name: "Newsletter subscribers", description: "Main weekly newsletter audience", contacts: 18420, growth: 4.2 },
  { id: "lst_02", name: "Customers", description: "Paying customers and active accounts", contacts: 6420, growth: 2.8 },
  { id: "lst_03", name: "Trial accounts", description: "Accounts currently in trial", contacts: 1280, growth: 9.4 },
  { id: "lst_04", name: "Lapsed customers", description: "No purchase in the last 180 days", contacts: 1340, growth: -1.2 },
];

export const tags = [
  { id: "tag_01", name: "customer", color: "#4f46e5", contacts: 6420 },
  { id: "tag_02", name: "newsletter", color: "#0891b2", contacts: 18420 },
  { id: "tag_03", name: "vip", color: "#d97706", contacts: 412 },
  { id: "tag_04", name: "trial", color: "#7c3aed", contacts: 1280 },
  { id: "tag_05", name: "webinar", color: "#059669", contacts: 860 },
];

export const segments = [
  { id: "seg_01", name: "Engaged subscribers", description: "Opened or clicked in the last 30 days", contacts: 8420, rules: "opened any campaign · 30 days" },
  { id: "seg_02", name: "Nigerian customers", description: "Country is Nigeria and status is subscribed", contacts: 1240, rules: "country equals Nigeria" },
  { id: "seg_03", name: "At-risk accounts", description: "No open in 90 days, customer tag", contacts: 1860, rules: "last open before 90 days" },
  { id: "seg_04", name: "New this month", description: "Joined in the last 30 days", contacts: 2380, rules: "created after Oct 2" },
];

export const templates = [
  { id: "tpl_01", name: "Product digest", category: "Newsletter", updated: "2026-10-30T09:00:00Z", accent: "#4f46e5", style: "digest" },
  { id: "tpl_02", name: "Welcome series", category: "Lifecycle", updated: "2026-10-28T14:00:00Z", accent: "#0891b2", style: "welcome" },
  { id: "tpl_03", name: "Launch announcement", category: "Promotion", updated: "2026-10-22T11:30:00Z", accent: "#d97706", style: "launch" },
  { id: "tpl_04", name: "Simple update", category: "General", updated: "2026-10-15T16:00:00Z", accent: "#059669", style: "simple" },
  { id: "tpl_05", name: "Event invitation", category: "Events", updated: "2026-10-11T10:00:00Z", accent: "#db2777", style: "event" },
  { id: "tpl_06", name: "Monthly roundup", category: "Newsletter", updated: "2026-10-04T08:30:00Z", accent: "#7c3aed", style: "roundup" },
];

export const automations = [
  { id: "aut_01", name: "Welcome series", status: "active", contacts: 1280, completion: 68, trigger: "Contact subscribes", updated: "2026-11-01T09:00:00Z" },
  { id: "aut_02", name: "Win-back lapsed customers", status: "active", contacts: 640, completion: 42, trigger: "No purchase in 180 days", updated: "2026-10-29T15:30:00Z" },
  { id: "aut_03", name: "Webinar follow-up", status: "paused", contacts: 320, completion: 51, trigger: "Form submitted", updated: "2026-10-25T12:00:00Z" },
  { id: "aut_04", name: "Birthday offer", status: "draft", contacts: 0, completion: 0, trigger: "Date-based", updated: "2026-10-20T09:15:00Z" },
];

export const domains = [
  { id: "dom_01", domain: "acme.studio", status: "verified", spf: "verified", dkim: "verified", dmarc: "verified", checked: "2026-11-02T08:10:00Z" },
  { id: "dom_02", domain: "news.acme.studio", status: "misconfigured", spf: "verified", dkim: "missing", dmarc: "verified", checked: "2026-11-02T08:10:00Z" },
  { id: "dom_03", domain: "mail.acme.studio", status: "pending", spf: "missing", dkim: "missing", dmarc: "missing", checked: "2026-11-01T18:00:00Z" },
];

export const activity = [
  { id: "act_01", title: "November product digest completed", detail: "18,420 recipients · 48.2% open rate", time: "2026-11-01T09:42:00Z", type: "campaign" },
  { id: "act_02", title: "Domain acme.studio verified", detail: "SPF, DKIM, and DMARC passed", time: "2026-11-01T08:10:00Z", type: "domain" },
  { id: "act_03", title: "2,400 contacts imported", detail: "November conference list", time: "2026-10-31T16:20:00Z", type: "contacts" },
  { id: "act_04", title: "Black Friday preview scheduled", detail: "Sends Nov 4 at 8:00 AM", time: "2026-10-31T14:05:00Z", type: "campaign" },
  { id: "act_05", title: "Welcome series updated", detail: "Kola edited email 2 of 4", time: "2026-10-30T11:30:00Z", type: "automation" },
];

export const invoices = [
  { id: "INV-2026-011", date: "2026-11-01T00:00:00Z", amount: 149, status: "paid" },
  { id: "INV-2026-010", date: "2026-10-01T00:00:00Z", amount: 149, status: "paid" },
  { id: "INV-2026-009", date: "2026-09-01T00:00:00Z", amount: 149, status: "paid" },
  { id: "INV-2026-008", date: "2026-08-01T00:00:00Z", amount: 99, status: "paid" },
];

export const usage = [
  { label: "Contacts stored", used: 24892, limit: 50000, unit: "contacts" },
  { label: "Emails sent this month", used: 184320, limit: 250000, unit: "emails" },
  { label: "Automation runs", used: 3840, limit: 10000, unit: "runs" },
  { label: "Team members", used: 5, limit: 10, unit: "members" },
];

export const campaignReport = {
  id: "cmp_01",
  name: "November product digest",
  subject: "New: automations, segments, and faster imports",
  sentAt: "2026-11-01T09:30:00Z",
  recipients: 18420,
  submitted: 18334,
  delivered: 18102,
  opens: 8728,
  uniqueOpens: 8878,
  clicks: 1547,
  uniqueClicks: 1320,
  bounces: 232,
  complaints: 4,
  unsubscribes: 61,
  openRate: 48.2,
  clickRate: 8.4,
  bounceRate: 1.26,
  topLinks: [
    { url: "https://acme.studio/blog/automations", clicks: 612 },
    { url: "https://acme.studio/pricing", clicks: 384 },
    { url: "https://acme.studio/changelog", clicks: 221 },
    { url: "https://acme.studio/webinar", clicks: 103 },
  ],
};

export const templateBlocks = [
  { id: "blk_01", type: "heading", content: "A better way to stay in touch" },
  { id: "blk_02", type: "paragraph", content: "Hi {{first_name}}, here is what is new at Acme Studio this month. We shipped automations, faster imports, and a refreshed template library." },
  { id: "blk_03", type: "image", content: "Product preview", src: "/logo.svg" },
  { id: "blk_04", type: "button", content: "See what's new", url: "https://acme.studio/changelog" },
  { id: "blk_05", type: "divider", content: "" },
  { id: "blk_06", type: "paragraph", content: "Thanks for reading. Reply to this email if you have feedback — we read every response." },
];
