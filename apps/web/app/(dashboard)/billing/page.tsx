import type { Metadata } from "next";
import { Plus, CreditCard } from "lucide-react";
import { PageHeader } from "@/components/page-header";
import { Card, CardHeader, CardTitle, CardDescription, CardContent } from "@/components/ui/card";
import { Button } from "@/components/ui/button";

export const metadata: Metadata = { title: "Billing" };

export default function BillingPage() {
  return (
    <div>
      <PageHeader
        eyebrow="Billing"
        title="Billing & usage"
        description="Manage your plan, monitor sending limits, and download invoices."
        actions={
          <button className="inline-flex h-9 items-center gap-1.5 rounded-lg bg-indigo-600 px-4 text-sm font-medium text-white shadow-sm hover:bg-indigo-500 disabled:opacity-60 disabled:hover:bg-indigo-600" disabled>
            <CreditCard className="h-4 w-4" /> Manage payment method
          </button>
        }
      />

      <Card>
        <CardHeader>
          <CardTitle>Coming soon</CardTitle>
          <CardDescription>
            View invoices, update payment method, and monitor sending quotas.
            Billing portal is in development for the next release.
          </CardDescription>
        </CardHeader>
        <CardContent className="rounded-lg border border-dashed border-slate-200 bg-slate-50 p-8 text-center">
          <CreditCard className="h-10 w-10 mx-auto text-slate-400 mb-3" />
          <h3 className="text-sm font-semibold text-slate-900 mb-1">Billing portal</h3>
          <p className="text-xs text-slate-500 max-w-xs mx-auto">
            Self-serve billing, plan upgrades, payment method management, and invoice
            history. Stay tuned.
          </p>
        </CardContent>
      </Card>
    </div>
  );
}
