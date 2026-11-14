import Link from "next/link";
import {
  Activity,
  Globe2,
  ListChecks,
  ShieldCheck,
  Slash,
  Zap,
} from "lucide-react";
import { CopyCodeTabs, FAQAccordion, type CodeExample } from "@/components/marketing/interactive";
import {
  Badge,
  ButtonLink,
  Card,
  CheckList,
  Chip,
  Grid,
  MockPanel,
  PlanCard,
  Section,
  Wrap,
} from "@/components/marketing/primitives";
import { plans, pricingFAQs, planComparison } from "@/lib/marketing/pricing";
import { ContactForm } from "@/components/marketing/contact-form";
import { PricingExperience } from "@/components/marketing/pricing-interaction";

const API_BASE_URL = `${(process.env.NEXT_PUBLIC_SITE_URL || "https://campaign.pipsleague.com").replace(/\/+$/, "")}/api/v1`;

const homeExamples: CodeExample[] = [
  {
    label: "cURL",
    code: [
      `curl ${API_BASE_URL}/transactional/messages \\`,
      '  -H "Authorization: Bearer $SENDWISE_API_KEY" \\',
      '  -H "Content-Type: application/json" \\',
      "  -d '{",
      '    "from": "Acme <hello@yourdomain.com>",',
      '    "to": ["ada@example.com"],',
      '    "subject": "Your verification code",',
      '    "text": "Your verification code is 482915.",',
      '    "html": "<p>Your verification code is 482915.</p>"',
      "  }'",
    ].join("\n"),
  },
  {
    label: "Node",
    code: [
      "const res = await fetch(",
      `  "${API_BASE_URL}/transactional/messages",`,
      "  {",
      '    method: "POST",',
      "    headers: {",
      '      Authorization: "Bearer " + process.env.SENDWISE_API_KEY,',
      '      "Content-Type": "application/json",',
      "    },",
      "    body: JSON.stringify({",
      '      from: "Acme <hello@yourdomain.com>",',
      '      to: ["ada@example.com"],',
      '      subject: "Your verification code",',
      '      text: "Your verification code is 482915.",',
      '      html: "<p>Your verification code is 482915.</p>",',
      "    }),",
      "  }",
      ");",
    ].join("\n"),
  },
  {
    label: "Python",
    code: [
      "import os, requests",
      "",
      "requests.post(",
      `    "${API_BASE_URL}/transactional/messages",`,
      '    headers={"Authorization": "Bearer " + os.environ["SENDWISE_API_KEY"]},',
      "    json={",
      '        "from": "Acme <hello@yourdomain.com>",',
      '        "to": ["ada@example.com"],',
      '        "subject": "Your verification code",',
      '        "text": "Your verification code is 482915.",',
      '        "html": "<p>Your verification code is 482915.</p>"',
      "    },",
      ")",
    ].join("\n"),
  },
  {
    label: "PHP",
    code: [
      `$ch = curl_init("${API_BASE_URL}/transactional/messages");`,
      "curl_setopt_array($ch, [",
      "  CURLOPT_POST => true,",
      "  CURLOPT_HTTPHEADER => [",
      '    "Authorization: Bearer " . getenv("SENDWISE_API_KEY"),',
      '    "Content-Type: application/json",',
      "  ],",
      "  CURLOPT_POSTFIELDS => json_encode([",
      '    "from" => "Acme <hello@yourdomain.com>",',
      '    "to" => ["ada@example.com"],',
      '    "subject" => "Your verification code",',
      '    "text" => "Your verification code is 482915.",',
      '    "html" => "<p>Your verification code is 482915.</p>",',
      "  ]),",
      "]);",
      "curl_exec($ch);",
    ].join("\n"),
  },
];

const developerExamples: CodeExample[] = [
  {
    label: "Send",
    file: "POST /api/v1/transactional/messages",
    note: "Use a verified sender address on a domain that is enabled for transactional email.",
    code: [
      `curl ${API_BASE_URL}/transactional/messages \\`,
      '  -H "Authorization: Bearer $SENDWISE_API_KEY" \\',
      '  -H "Idempotency-Key: order-1042-receipt" \\',
      '  -H "Content-Type: application/json" \\',
      "  -d '{",
      '    "from": "Acme <billing@yourdomain.com>",',
      '    "to": ["ada@example.com"],',
      '    "subject": "Your receipt",',
      '    "text": "Your receipt for order 1042 is ready.",',
      '    "html": "<p>Your receipt for order 1042 is ready.</p>"',
      "  }'",
    ].join("\n"),
  },
  {
    label: "Webhooks",
    file: "Webhook payload",
    note: "Every request is signed. Verify the signature header before trusting the payload.",
    code: [
      "POST https://yourapp.com/webhooks/sendwise",
      "Sendwise-Signature: t=1731578400,v1=5f2b…",
      "",
      "{",
      '  "event": "message.delivered",',
      '  "message_id": "msg_01J8",',
      '  "to": "ada@example.com",',
      '  "domain": "yourdomain.com",',
      '  "timestamp": "2026-11-14T09:20:00Z"',
      "}",
    ].join("\n"),
  },
  {
    label: "Verify",
    file: "Verify a signature (Node)",
    note: "Reject requests with an old timestamp to block replayed events.",
    code: [
      'import crypto from "node:crypto";',
      "",
      "export function verify(rawBody, header, secret) {",
      '  const [t, v1] = header.split(",").map((p) => p.split("=")[1]);',
      "  const expected = crypto",
      '    .createHmac("sha256", secret)',
      '    .update(t + "." + rawBody)',
      '    .digest("hex");',
      "  return crypto.timingSafeEqual(Buffer.from(v1), Buffer.from(expected));",
      "}",
    ].join("\n"),
  },
];

const commonQuestions = [
  {
    question: "Do I need my own domain?",
    answer: "Yes. You send from your own domain, so recipients see your brand and your reputation stays yours. You publish a few DNS records and Sendwise shows the status of each one.",
  },
  {
    question: "How long does domain verification take?",
    answer: "It depends on how quickly your DNS provider publishes changes. Sendwise rechecks the records and shows per-record results, so you can see exactly what is missing or wrong.",
  },
  {
    question: "Can I send both marketing and application emails?",
    answer: "Yes. Campaigns and transactional email run in separate lanes with their own limits, so a large campaign never delays a verification code or receipt.",
  },
  {
    question: "Can I connect with SMTP instead of the API?",
    answer: "Yes. Generate SMTP credentials in your dashboard and use them in any app, framework or plugin that supports SMTP.",
  },
  {
    question: "What happens to bounces and complaints?",
    answer: "Hard bounces and complaints are added to a suppression list automatically and future sends to those addresses are blocked, which protects your sender reputation.",
  },
];

function FeatureCard({
  icon,
  title,
  description,
  dark = false,
}: {
  icon?: React.ReactNode;
  title: string;
  description: string;
  dark?: boolean;
}) {
  return (
    <Card dark={dark} className="feature-card">
      <span className={`ico${dark ? " ico-dark" : ""}`} aria-hidden="true">{icon ?? <Activity size={20} />}</span>
      <h3 className="h3">{title}</h3>
      <p className="small">{description}</p>
    </Card>
  );
}

function CTA({
  title,
  description,
  primary = "Start sending",
  primaryHref = "/register",
  secondary = "Talk to us",
  secondaryHref = "/contact",
}: {
  title: string;
  description?: string;
  primary?: string;
  primaryHref?: string;
  secondary?: string;
  secondaryHref?: string;
}) {
  return (
    <section className="dark marketing-cta">
      <Wrap className="marketing-cta-inner">
        <div>
          <h2 className="title">{title}</h2>
          {description ? <p className="lead">{description}</p> : null}
        </div>
        <div className="marketing-cta-actions">
          <ButtonLink href={primaryHref} variant="accent">{primary}</ButtonLink>
          <ButtonLink href={secondaryHref} variant="ghost-dark">{secondary}</ButtonLink>
        </div>
      </Wrap>
    </section>
  );
}

function PageHero({
  eyebrow,
  title,
  description,
  centered = false,
  children,
}: {
  eyebrow: string;
  title: string;
  description: string;
  centered?: boolean;
  children?: React.ReactNode;
}) {
  return (
    <section className="dark marketing-page-hero">
      <Wrap className={centered ? "marketing-page-hero-centered" : ""}>
        <span className="eyebrow eyebrow-dark">{eyebrow}</span>
        <h1 className="display">{title}</h1>
        <p className="lead">{description}</p>
        {children}
      </Wrap>
    </section>
  );
}

export function HomePage() {
  return (
    <>
      <script
        type="application/ld+json"
        dangerouslySetInnerHTML={{
          __html: JSON.stringify({
            "@context": "https://schema.org",
            "@type": "Organization",
            name: "Sendwise",
            url: process.env.NEXT_PUBLIC_SITE_URL || "https://campaign.pipsleague.com",
            description: "Email infrastructure for campaigns and application email.",
          }),
        }}
      />
      <section className="dark">
        <Wrap className="hero-grid home-hero">
          <div>
            <span className="eyebrow eyebrow-dark">Email infrastructure</span>
            <h1 className="display">Email that reaches the inbox.</h1>
            <p className="lead">
              Send campaigns and application emails from your own domain, with verified DNS,
              delivery tracking and the controls that keep your sender reputation clean.
            </p>
            <div className="marketing-button-row">
              <ButtonLink href="/register" variant="accent">Start sending</ButtonLink>
              <ButtonLink href="/developers" variant="ghost-dark">Read the docs</ButtonLink>
            </div>
            <p className="small home-free-tier">Free plan: 3,000 emails per month, up to 100 per day.</p>
          </div>
          <div>
            <CopyCodeTabs examples={homeExamples} ariaLabel="Integration language" />
            <div className="code-response">
              <Badge>202 Accepted</Badge>
              <code className="code">{'{ "id": "msg_01J8", "status": "queued" }'}</code>
            </div>
          </div>
        </Wrap>
      </section>

      <Section className="use-cases">
        <Wrap>
          <p className="small use-cases-title">Built for the emails your product depends on</p>
          <div className="chip-row">
            {["Account verification", "Password reset", "One-time codes", "Payment receipts", "Order confirmations", "Booking notices", "Invoices", "System alerts", "Newsletters", "Promotions"].map((item) => <Chip key={item}>{item}</Chip>)}
          </div>
        </Wrap>
      </Section>

      <Section>
        <Wrap>
          <div className="section-intro">
            <span className="eyebrow">Two products, one platform</span>
            <h2 className="title">Send to your audience. Send to your users.</h2>
            <p className="lead">Marketing and transactional mail have different rules. Sendwise keeps them in separate lanes so a big campaign never delays a login code.</p>
          </div>
          <Grid columns={2}>
            <Card className="product-card">
              <Badge>Campaigns</Badge>
              <h3 className="h3 product-title">Email campaigns that respect your audience</h3>
              <CheckList>
                <li>Drag-and-drop editor with prebuilt templates you can recolor</li>
                <li>Import your own HTML templates and keep them intact</li>
                <li>Lists, tags and segments, with CSV import</li>
                <li>Required company profile and compliance footer on every send</li>
                <li>Warm-up limits that grow volume gradually</li>
              </CheckList>
              <ButtonLink href="/platform" variant="ghost-light">Explore campaigns</ButtonLink>
            </Card>
            <Card dark className="product-card">
              <Badge tone="warn">Transactional</Badge>
              <h3 className="h3 product-title">Application email with an API and SMTP</h3>
              <CheckList>
                <li>REST API and SMTP credentials, with revocable keys</li>
                <li>Reusable templates with dynamic variables</li>
                <li>Per-message logs and live delivery status</li>
                <li>Signed webhooks, automatic retries, suppression list</li>
                <li>Rate limits and usage reports per workspace</li>
              </CheckList>
              <ButtonLink href="/developers" variant="accent">See the developer docs</ButtonLink>
            </Card>
          </Grid>
        </Wrap>
      </Section>

      <Section className="section-paper">
        <Wrap>
          <div className="section-intro">
            <span className="eyebrow">Deliverability built in</span>
            <h2 className="title">The unglamorous parts, handled.</h2>
          </div>
          <Grid>
            <FeatureCard icon={<Globe2 />} title="Domain verification" description="Add your domain, publish the records, and watch each one turn green with per-resolver results. Copy buttons sit beside every name and value." />
            <FeatureCard icon={<ShieldCheck />} title="SPF, DKIM and DMARC" description="Authentication is checked before you can send, so messages leave your domain properly signed and aligned." />
            <FeatureCard icon={<ListChecks />} title="Searchable email logs" description="Every attempt, with its timeline: queued, sent, delivered, deferred, bounced or failed, plus the server response." />
            <FeatureCard icon={<Slash />} title="Bounces and suppression" description="Hard bounces and complaints are suppressed automatically, protecting your reputation and your next send." />
            <FeatureCard icon={<Activity />} title="Webhooks" description="Signed events tell your app when a message is delivered, bounced or complained about, with retries if your server is down." />
            <FeatureCard icon={<Zap />} title="Rate limits and warm-up" description="Control sending speed, caps and ramp-up per workspace, so new domains grow into volume instead of tripping filters." />
          </Grid>
        </Wrap>
      </Section>

      <Section>
        <Wrap className="split">
          <div>
            <span className="eyebrow">See what is happening</span>
            <h2 className="title">From DNS to delivery, in one view.</h2>
            <p className="lead">Know why a record is failing and what a message did after you sent it. No guessing, no support ticket to find out.</p>
            <CheckList>
              <li>Per-record DNS status with the exact value found</li>
              <li>Message timeline with SMTP responses</li>
              <li>Bounce, complaint and delivery rates per domain</li>
              <li>Daily and monthly usage against your limits</li>
            </CheckList>
          </div>
          <div className="mock-stack">
            <MockPanel title={<><span>DNS records</span><span className="small mono">yourdomain.com</span></>}>
              <div className="row"><span><strong>SPF</strong> <span className="small mono">TXT @</span></span><Badge>Verified</Badge></div>
              <div className="row"><span><strong>DKIM</strong> <span className="small mono">TXT mail._domainkey</span></span><Badge>Verified</Badge></div>
              <div className="row"><span><strong>DMARC</strong> <span className="small mono">TXT _dmarc</span></span><Badge tone="warn">Checking</Badge></div>
              <div className="row"><span><strong>Ownership</strong> <span className="small mono">TXT _sendwise-verification</span></span><Badge>Verified</Badge></div>
            </MockPanel>
            <MockPanel title={<><span>Message timeline</span><Badge>Delivered</Badge></>}>
              <div className="row"><span>Queued</span><span className="small mono">00:00.00</span></div>
              <div className="row"><span>Sent to mail server</span><span className="small mono">00:00.31</span></div>
              <div className="row"><span>Accepted by recipient server</span><span className="small mono">00:01.12</span></div>
              <div className="row mock-sample"><span className="small">Sample data for illustration</span></div>
            </MockPanel>
          </div>
        </Wrap>
      </Section>

      <Section dark>
        <Wrap>
          <div className="section-intro">
            <span className="eyebrow eyebrow-dark">Get going</span>
            <h2 className="title">Four steps to your first send.</h2>
          </div>
          <Grid columns={4}>
            {[
              ["Add your domain", "Create a workspace and add the domain you send from."],
              ["Verify DNS", "Publish ownership, SPF, DKIM and DMARC. We show the status of each."],
              ["Create credentials", "Generate an API key or SMTP username and password."],
              ["Send and monitor", "Watch logs, delivery reports and usage as mail goes out."],
            ].map(([title, description], index) => (
              <Card dark key={title}>
                <div className="step-n step-accent">{index + 1}</div>
                <h3 className="h3">{title}</h3>
                <p className="small">{description}</p>
              </Card>
            ))}
          </Grid>
        </Wrap>
      </Section>

      <Section>
        <Wrap>
          <div className="section-intro">
            <span className="eyebrow">Pricing</span>
            <h2 className="title">Simple plans that grow with your volume.</h2>
            <p className="lead">Choose from Free, Starter, Business, and Enterprise plans with clear monthly sending volumes.</p>
          </div>
          <Grid>
            {plans.map((plan) => (
              <PlanCard
                key={plan.name}
                name={plan.name}
                description={plan.audience}
                price={plan.monthlyPrice}
                period="/month"
                features={plan.features.slice(0, 3)}
                href="/pricing"
                action="View plan"
                featured={plan.featured}
              />
            ))}
          </Grid>
        </Wrap>
      </Section>

      <Section className="faq-section">
        <Wrap className="faq-wrap">
          <span className="eyebrow">Questions</span>
          <h2 className="title">Frequently asked</h2>
          <FAQAccordion items={commonQuestions} label="Frequently asked questions" />
          <p className="small faq-contact">Something else? <Link href="/contact">Talk to us</Link>.</p>
        </Wrap>
      </Section>

      <CTA title="Ready to send email people actually receive?" description="Create your workspace, verify a domain, and send your first message today." />
    </>
  );
}

export function PlatformPage() {
  return (
    <>
      <PageHero
        eyebrow="The platform"
        title="Everything between your domain and the inbox."
        description="Campaigns, contacts, templates, domains and delivery tracking in one workspace, with transactional email running alongside."
        centered
      >
        <div className="marketing-button-row centered">
          <ButtonLink href="/register" variant="accent">Start sending</ButtonLink>
          <ButtonLink href="/pricing" variant="ghost-dark">See pricing</ButtonLink>
        </div>
      </PageHero>
      <Section>
        <Wrap className="split">
          <div>
            <span className="eyebrow">Email editor</span>
            <h2 className="title">Design visually, or bring your own HTML.</h2>
            <p className="lead">Start from a prebuilt template, drag in blocks, change colors and text, or import an HTML email you already have and keep its layout.</p>
            <CheckList>
              <li>Prebuilt templates that are fully editable</li>
              <li>Switch between visual design, HTML and plain text</li>
              <li>Imported HTML keeps its original table layout</li>
              <li>Images are stored with your workspace, so they always load</li>
              <li>Desktop and mobile preview before you send</li>
            </CheckList>
          </div>
          <div className="mock editor-mock">
            <div className="mock-h"><span>Template editor</span><span className="badge badge-ok">Mobile</span></div>
            <div className="editor-mock-body">
              <div className="editor-blocks"><span className="small">Blocks</span>{["Heading", "Text", "Image", "Button", "Columns"].map((text) => <Chip key={text}>{text}</Chip>)}</div>
              <div className="editor-canvas">
                <div className="email-mock">
                  <div className="email-mock-top" />
                  <div className="email-mock-content"><span /><i /><i /><b /></div>
                </div>
              </div>
            </div>
          </div>
        </Wrap>
      </Section>
      <Section className="section-paper">
        <Wrap>
          <div className="section-intro"><span className="eyebrow">Audience</span><h2 className="title">Know who you are sending to.</h2></div>
          <Grid>
            {[
              ["Lists and tags", "Organize contacts into lists, label them with tags, and send to exactly the group you mean."],
              ["Segments", "Build dynamic groups from contact fields and behavior, and let them update themselves."],
              ["CSV import", "Bring contacts in with a file, map the columns, and see what was added, skipped or rejected."],
              ["Forms", "Collect sign-ups from your site and add subscribers straight to a list."],
              ["Unsubscribe handling", "Every campaign carries a working unsubscribe link and your company details, added for you."],
              ["Suppression list", "Bounced, complained and unsubscribed addresses are blocked from future sends automatically."],
            ].map(([title, description]) => <FeatureCard key={title} title={title} description={description} />)}
          </Grid>
        </Wrap>
      </Section>
      <Section>
        <Wrap className="split">
          <MockPanel title={<><span>Sending domains</span><Badge>2 verified</Badge></>}>
            <div className="row"><span><strong>news.yourdomain.com</strong><br /><span className="small">Marketing</span></span><Badge>Verified</Badge></div>
            <div className="row"><span><strong>tx.yourdomain.com</strong><br /><span className="small">Transactional</span></span><Badge>Verified</Badge></div>
            <div className="row"><span><strong>shop.example.com</strong><br /><span className="small">Waiting for DNS</span></span><Badge tone="warn">Pending</Badge></div>
            <div className="row mock-sample"><span className="small">Sample data for illustration</span></div>
          </MockPanel>
          <div>
            <span className="eyebrow">Domains and senders</span>
            <h2 className="title">Send from addresses that match your brand.</h2>
            <p className="lead">Verify a domain once, then create as many sender addresses on it as you need.</p>
            <CheckList>
              <li>newsletter@, sales@, support@, or any name you choose</li>
              <li>Live DNS checks with per-record results</li>
              <li>Copy button beside every record name and value</li>
              <li>Separate marketing and transactional domains</li>
            </CheckList>
          </div>
        </Wrap>
      </Section>
      <Section dark>
        <Wrap>
          <div className="section-intro"><span className="eyebrow eyebrow-dark">Protection</span><h2 className="title">Controls that guard your reputation.</h2></div>
          <Grid columns={4}>
            {[
              ["Warm-up", "Daily limits grow in steps so new domains build trust before volume arrives."],
              ["Rate limits", "Cap sending speed and daily totals per workspace."],
              ["Auto-pause", "Sending pauses when bounce or complaint rates cross safe thresholds."],
              ["Dead-letter queue", "Failed messages are kept so you can inspect and retry them."],
            ].map(([title, description]) => <FeatureCard key={title} title={title} description={description} dark />)}
          </Grid>
        </Wrap>
      </Section>
      <Section>
        <Wrap>
          <div className="section-intro"><span className="eyebrow">Reporting</span><h2 className="title">Numbers you can act on.</h2></div>
          <Grid>
            {[
              ["Campaign reports", "Delivered, opened, clicked, bounced and unsubscribed, per campaign."],
              ["Delivery logs", "Search every message and open its full event timeline."],
              ["Usage", "Daily and monthly volume against your plan limits."],
            ].map(([title, description]) => <FeatureCard key={title} title={title} description={description} />)}
          </Grid>
        </Wrap>
      </Section>
      <CTA title="Set up your workspace in an afternoon." description="" />
    </>
  );
}

export function DevelopersPage() {
  return (
    <>
      <PageHero
        eyebrow="Developers"
        title="Send from your app in minutes."
        description="A REST API and standard SMTP for every transactional email your product sends: verification, password reset, one-time codes, receipts and alerts."
      >
        <div className="marketing-button-row">
          <ButtonLink href="/register" variant="accent">Get an API key</ButtonLink>
          <ButtonLink href="/contact" variant="ghost-dark">Ask an engineer</ButtonLink>
        </div>
      </PageHero>
      <Section className="section-bottom-small">
        <Wrap>
          <Grid>
            {[
              ["Verify a domain", "Publish the DNS records and wait for SPF, DKIM and DMARC to turn green."],
              ["Create a credential", "Generate an API key or SMTP username and password. Secrets are shown once."],
              ["Send a message", "Call the API or point your SMTP settings at Sendwise, then watch it in the logs."],
            ].map(([title, description], index) => (
              <Card key={title}>
                <div className="step-n">{index + 1}</div>
                <h2 className="h3">{title}</h2>
                <p className="small">{description}</p>
              </Card>
            ))}
          </Grid>
        </Wrap>
      </Section>
      <Section className="section-top-small">
        <Wrap>
          <div className="section-intro"><span className="eyebrow">Integrate</span><h2 className="title">Pick your way in.</h2></div>
          <CopyCodeTabs examples={developerExamples} ariaLabel="Integration guide" />
        </Wrap>
      </Section>
      <Section className="section-paper">
        <Wrap>
          <div className="section-intro"><span className="eyebrow">REST API</span><h2 className="title">Endpoints at a glance.</h2></div>
          <div className="table-scroll">
            <table className="tbl">
              <thead><tr><th>Method</th><th>Path</th><th>Purpose</th></tr></thead>
              <tbody>
                <tr><td><Badge>POST</Badge></td><td className="mono">/api/v1/transactional/messages</td><td>Send one message, with HTML, text or a template</td></tr>
                <tr><td><Badge>GET</Badge></td><td className="mono">/api/v1/transactional/messages</td><td>List messages for the authenticated API key</td></tr>
                <tr><td><Badge>GET</Badge></td><td className="mono">/api/v1/transactional/messages/{"{id}"}</td><td>Read a message and its delivery events</td></tr>
                <tr><td><Badge>GET</Badge></td><td className="mono">/api/v1/transactional/templates</td><td>List workspace templates (user session)</td></tr>
                <tr><td><Badge>POST</Badge></td><td className="mono">/api/v1/transactional/templates</td><td>Create a workspace template (user session)</td></tr>
                <tr><td><Badge>GET, PATCH, DELETE</Badge></td><td className="mono">/api/v1/transactional/templates/{"{id}"}</td><td>Read, update, or delete a workspace template (user session)</td></tr>
                <tr><td><Badge>POST</Badge></td><td className="mono">/api/v1/transactional/webhooks</td><td>Register an endpoint for delivery events</td></tr>
              </tbody>
            </table>
          </div>
          <p className="small table-note">Send requests support idempotency keys, so a retry never sends the same email twice.</p>
        </Wrap>
      </Section>
      <Section>
        <Wrap>
          <div className="section-intro"><span className="eyebrow">Built for production</span><h2 className="title">What your integration can rely on.</h2></div>
          <Grid columns={4}>
            {[
              ["Credentials", "Create, revoke and rotate API keys with scopes, and issue separate SMTP credentials per app."],
              ["Retries", "Temporary failures retry automatically with backoff. Permanent failures stop and are logged."],
              ["Webhooks", "Signed events for delivered, bounced, deferred, complained and suppressed messages."],
              ["Limits", "Per-key rate limits and quotas return a clear 429 with a Retry-After header."],
            ].map(([title, description]) => <FeatureCard key={title} title={title} description={description} />)}
          </Grid>
        </Wrap>
      </Section>
      <Section dark>
        <Wrap>
          <div className="section-intro">
            <span className="eyebrow eyebrow-dark">SDKs and integrations</span>
            <h2 className="title">Use your preferred client.</h2>
            <p className="lead">Sendwise does not currently publish official SDK packages. The REST API works with standard HTTP clients; SMTP submission is available to enabled workspaces.</p>
          </div>
          <Grid columns={2}>
            {[
              ["REST API", "Use an HTTP client in your language. The examples above call the implemented message endpoint.", "Live"],
              ["SMTP submission", "Use an SMTP client with the host and connection details provided for your deployment.", "Available when enabled"],
            ].map(([title, description, status]) => (
              <Card dark key={String(title)}>
                <Badge>{status}</Badge>
                <h3 className="h3 library-title">{title}</h3>
                <p className="small">{description}</p>
              </Card>
            ))}
          </Grid>
        </Wrap>
      </Section>
      <CTA title="Create an account and send your first API request." primary="Get an API key" secondary="Talk to us" />
    </>
  );
}

export function PricingPage() {
  return (
    <>
      <PricingExperience />
      <Section className="section-paper">
        <Wrap>
          <h2 className="title table-title">Compare plans</h2>
          <div className="table-scroll">
            <table className="tbl">
              <thead><tr><th>Feature</th><th>Free</th><th>Starter</th><th>Business</th><th>Enterprise</th></tr></thead>
              <tbody>{planComparison.map(([feature, free, starter, business, enterprise]) => <tr key={feature}><td>{feature}</td><td>{free}</td><td>{starter}</td><td>{business}</td><td>{enterprise}</td></tr>)}</tbody>
            </table>
          </div>
        </Wrap>
      </Section>
      <Section>
        <Wrap className="faq-wrap">
          <h2 className="title table-title">Pricing questions</h2>
          <FAQAccordion items={pricingFAQs} label="Pricing questions" />
        </Wrap>
      </Section>
      <CTA title="Not sure which plan fits? Tell us what you send." primary="Talk to us" primaryHref="/contact" secondary="Start sending" secondaryHref="/register" />
    </>
  );
}

export function AboutPage() {
  return (
    <>
      <PageHero
        eyebrow="About Sendwise"
        title="Email infrastructure you can understand."
        description="Sendwise brings campaign tools and application email delivery together, with domain verification and message status visible in the product."
      />
      <Section>
        <Wrap className="split">
          <div>
            <span className="eyebrow">The product</span>
            <h2 className="title">One workspace for two kinds of email.</h2>
            <p className="lead">
              Marketing teams can manage contacts and campaigns, while application teams can submit transactional messages
              through the REST API or authenticated SMTP. Both use sending domains configured in the workspace.
            </p>
            <p className="lead">
              Domain setup shows the required DNS records and their verification results. Delivery views expose message
              status without requiring a separate email provider dashboard.
            </p>
          </div>
          <Card className="glance-card">
            <h3 className="h3">At a glance</h3>
            {[
              ["Campaigns", "Create and send marketing campaigns"],
              ["Transactional", "Submit messages through REST API or SMTP"],
              ["Domain setup", "SPF and DKIM verification; DMARC is advisory"],
              ["Message status", "Review sending and delivery events"],
            ].map(([label, value]) => <div className="row" key={label}><span className="small">{label}</span><strong>{value}</strong></div>)}
          </Card>
        </Wrap>
      </Section>
      <Section className="section-paper">
        <Wrap>
          <div className="section-intro"><span className="eyebrow">How we work</span><h2 className="title">Principles behind the product.</h2></div>
          <Grid>
            {[
              ["Domain verification", "The product displays SPF and DKIM setup records and reports DNS verification results."],
              ["Separate sending paths", "Campaign delivery and transactional submission use distinct workflows."],
              ["Developer access", "Transactional messages can be submitted through the REST API or authenticated SMTP when enabled."],
              ["Delivery visibility", "Message and campaign status are available in the workspace for operational follow-up."],
            ].map(([title, description]) => <FeatureCard key={title} title={title} description={description} />)}
          </Grid>
        </Wrap>
      </Section>
      <CTA title="Want to talk about your sending?" primary="Contact us" primaryHref="/contact" />
    </>
  );
}

export function ContactPage() {
  return (
    <>
      <Section className="contact-section">
        <Wrap className="split contact-layout">
          <div>
            <span className="eyebrow">Contact</span>
            <h1 className="display">Talk to the Sendwise team.</h1>
            <p className="lead">
              Use the form for plan questions, migration, domain verification, transactional email, or abuse reports.
              Choose the closest topic and include the details needed to understand your request.
            </p>
            <p className="small">The form confirms when your message has been recorded. A response-time commitment is not published.</p>
          </div>
          <ContactForm />
        </Wrap>
      </Section>
    </>
  );
}
