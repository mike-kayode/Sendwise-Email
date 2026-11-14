"use client";

import { useState } from "react";
import { ButtonLink, PlanCard, Wrap } from "@/components/marketing/primitives";
import { plans, type BillingPeriod } from "@/lib/marketing/pricing";

export function PricingExperience() {
  const [period, setPeriod] = useState<BillingPeriod>("Monthly");
  const periodLabel = period === "Monthly" ? "/month" : "/month, billed yearly";
  return (
    <>
      <section className="dark marketing-page-hero">
        <Wrap className="marketing-page-hero-centered">
          <span className="eyebrow eyebrow-dark">Pricing</span>
          <h1 className="display">Plans that grow with your sending.</h1>
          <p className="lead">Campaigns and transactional email on every plan. Pay for the volume you actually send.</p>
          <div className="billing-toggle" role="group" aria-label="Billing period">
            {(["Monthly", "Yearly"] as const).map((value) => (
              <button
                type="button"
                key={value}
                className={`tl${period === value ? " on" : ""}`}
                aria-pressed={period === value}
                onClick={() => setPeriod(value)}
              >
                {value}
              </button>
            ))}
          </div>
        </Wrap>
      </section>
      <section className="sec pricing-plans">
        <Wrap>
          <div className="grid-4">
            {plans.map((plan) => (
              <PlanCard
                key={plan.name}
                name={plan.name}
                description={plan.audience}
                price={period === "Monthly" ? plan.monthlyPrice : plan.yearlyPrice ?? "Contact us"}
                pricePrefix={period === "Yearly" && !plan.yearlyPrice ? "" : "₦"}
                period={
                  period === "Monthly" || plan.yearlyPrice
                    ? periodLabel
                    : "for annual pricing"
                }
                features={plan.features}
                href={plan.href}
                action={plan.action}
                featured={plan.featured}
              />
            ))}
          </div>
          <aside className="payg-card" aria-labelledby="payg-heading">
            <div>
              <span className="eyebrow">Flexible billing</span>
              <h2 id="payg-heading" className="h3">Pay as you go</h2>
              <p className="small">Need a usage-based option? Talk to us about billing that fits your sending volume.</p>
            </div>
            <ButtonLink href="/contact" variant="ghost-light">Ask about pay as you go</ButtonLink>
          </aside>
        </Wrap>
      </section>
    </>
  );
}
