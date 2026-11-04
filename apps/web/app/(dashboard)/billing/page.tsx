import type { Metadata } from "next";
import { Check, CreditCard } from "lucide-react";
import { PageHeader } from "@/components/page-header";
import { Card, CardHeader, CardTitle, CardDescription, CardContent } from "@/components/ui/card";
import { Badge, StatusBadge } from "@/components/ui/badge";
import { ProgressBar } from "@/components/charts";
import { Button } from "@/components/ui/button";
import { Table, Thead, Tbody, Tr, Th, Td } from "@/components/ui/table";
import { formatDate, formatNumber } from "@/lib/utils";

const invoices = [
  { id: "INV-2048", date: "2026-04-01T00:00:00Z", amount: 149, status: "paid" },
  { id: "INV-2036", date: "2026-03-01T00:00:00Z", amount: 149, status: "paid" },
  { id: "INV-2028", date: "2026-02-01T00:00:00Z", amount: 149, status: "paid" },
  { id: "INV-2020", date: "2026-01-01T00:00:00Z", amount: 149, status: "paid" },
];

const usage = [
  { label: "Contacts", used: 36800, limit: 50000, unit: "contacts" },
  { label: "Emails sent", used: 186420, limit: 250000, unit: "emails" },
  { label: "Sending domains", used: 3, limit: 5, unit: "domains" },
];

export const metadata: Metadata = { title: "Billing & usage" };

const plans = [
  {
    name: "Starter",
    price: 0,
    description: "For testing and small lists.",
    features: ["2,000 contacts", "10,000 emails / month", "1 sending domain", "Community support"],
  },
  {
    name: "Growth",
    price: 149,
    description: "For growing teams sending every week.",
    features: ["50,000 contacts", "250,000 emails / month", "5 sending domains", "Automations", "Priority support"],
    current: true,
  },
  {
    name: "Scale",
    price: 399,
    description: "For high-volume senders.",
    features: ["250,000 contacts", "2M emails / month", "Unlimited domains", "Dedicated IP", "SSO & audit logs"],
  },
];

export default function BillingPage() {
  return (
    <div>
      <PageHeader
        title="Billing & usage"
        description="Manage your plan, monitor sending limits, and download invoices."
        actions={
          <Button variant="secondary">
            <CreditCard className="h-4 w-4" /> Manage payment method
          </Button>
        }
      />

      <div className="grid gap-5 lg:grid-cols-3">
        <Card className="lg:col-span-2">
          <CardHeader className="flex flex-wrap items-center justify-between gap-2">
            <div>
              <CardTitle>Current plan</CardTitle>
              <CardDescription>Renews on December 1, 2026</CardDescription>
            </div>
            <Badge variant="indigo">Growth · $149/mo</Badge>
          </CardHeader>
          <CardContent className="space-y-5">
            {usage.map((item) => {
              const percent = (item.used / item.limit) * 100;
              return (
                <div key={item.label}>
                  <div className="mb-1.5 flex items-center justify-between text-sm">
                    <span className="text-slate-600">{item.label}</span>
                    <span className="tabular-nums text-slate-500">
                      {formatNumber(item.used)} <span className="text-slate-400">/ {formatNumber(item.limit)}</span>
                    </span>
                  </div>
                  <ProgressBar value={percent} />
                  <p className="mt-1 text-xs text-slate-400">{Math.round(percent)}% of {item.unit} used</p>
                </div>
              );
            })}
          </CardContent>
        </Card>

        <Card className="h-fit">
          <CardHeader>
            <CardTitle>Payment method</CardTitle>
          </CardHeader>
          <CardContent className="space-y-3">
            <div className="flex items-center gap-3 rounded-lg border border-slate-200 p-3">
              <span className="flex h-9 w-12 items-center justify-center rounded bg-slate-900 text-[10px] font-semibold text-white">
                VISA
              </span>
              <div>
                <p className="text-sm font-medium text-slate-900">Visa ending in 4242</p>
                <p className="text-xs text-slate-500">Expires 08/2029</p>
              </div>
            </div>
            <div className="text-sm">
              <p className="text-xs text-slate-500">Billing email</p>
              <p className="font-medium text-slate-900">billing@acme.studio</p>
            </div>
            <Button variant="secondary" className="w-full">
              Update payment method
            </Button>
          </CardContent>
        </Card>
      </div>

      <Card className="mt-5">
        <CardHeader>
          <CardTitle>Invoices</CardTitle>
          <CardDescription>Receipts for the last four billing periods.</CardDescription>
        </CardHeader>
        <CardContent className="px-0 py-0">
          <Table>
            <Thead>
              <Tr>
                <Th>Invoice</Th>
                <Th>Date</Th>
                <Th className="text-right">Amount</Th>
                <Th>Status</Th>
                <Th />
              </Tr>
            </Thead>
            <Tbody>
              {invoices.map((invoice) => (
                <Tr key={invoice.id}>
                  <Td className="font-medium text-slate-900">{invoice.id}</Td>
                  <Td className="text-slate-600">{formatDate(invoice.date)}</Td>
                  <Td className="text-right tabular-nums">${invoice.amount}.00</Td>
                  <Td>
                    <StatusBadge status={invoice.status} />
                  </Td>
                  <Td>
                    <button className="text-xs font-medium text-indigo-600 hover:text-indigo-500">Download</button>
                  </Td>
                </Tr>
              ))}
            </Tbody>
          </Table>
        </CardContent>
      </Card>

      <div className="mt-5 grid gap-4 md:grid-cols-3">
        {plans.map((plan) => (
          <Card key={plan.name} className={plan.current ? "border-indigo-300 ring-1 ring-indigo-200" : undefined}>
            <CardContent className="p-5">
              <div className="flex items-center justify-between">
                <h3 className="text-sm font-semibold text-slate-900">{plan.name}</h3>
                {plan.current ? <Badge variant="indigo">Current</Badge> : null}
              </div>
              <p className="mt-2 text-2xl font-semibold tracking-tight text-slate-900">
                ${plan.price}
                <span className="text-sm font-normal text-slate-500">/mo</span>
              </p>
              <p className="mt-1 text-xs text-slate-500">{plan.description}</p>
              <ul className="mt-4 space-y-2 text-sm">
                {plan.features.map((feature) => (
                  <li key={feature} className="flex items-center gap-2 text-slate-600">
                    <Check className="h-3.5 w-3.5 text-emerald-500" /> {feature}
                  </li>
                ))}
              </ul>
              <button
                disabled={plan.current}
                className="mt-4 h-9 w-full rounded-lg bg-slate-900 text-sm font-medium text-white hover:bg-slate-800 disabled:bg-slate-100 disabled:text-slate-400"
              >
                {plan.current ? "Current plan" : "Switch plan"}
              </button>
            </CardContent>
          </Card>
        ))}
      </div>
    </div>
  );
}
