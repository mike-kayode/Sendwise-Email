import { ButtonLink, PlanCard, Wrap } from "@/components/marketing/primitives";
import { plans } from "@/lib/marketing/pricing";

export function PricingExperience() {
  return (
    <>
      <section className="dark marketing-page-hero">
        <Wrap className="marketing-page-hero-centered">
          <span className="eyebrow eyebrow-dark">Pricing</span>
          <h1 className="display">Plans that grow with your sending.</h1>
          <p className="lead">Compare monthly email volumes and listed prices. Daily sending limits are managed per sending domain.</p>
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
                price={plan.monthlyPrice}
                period="/month"
                features={plan.features}
                href={plan.href}
                action={plan.action}
                featured={plan.featured}
              />
            ))}
          </div>
          <aside className="payg-card" aria-labelledby="payg-heading">
            <div>
              <span className="eyebrow">Billing and activation</span>
              <h2 id="payg-heading" className="h3">Questions about plan setup?</h2>
              <p className="small">The app does not currently process payments or change paid plans. Contact us to confirm the current activation process.</p>
            </div>
            <ButtonLink href="/contact" variant="ghost-light">Ask about a plan</ButtonLink>
          </aside>
        </Wrap>
      </section>
    </>
  );
}
