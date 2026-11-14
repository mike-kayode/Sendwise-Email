import Link from "next/link";
import type { ReactNode } from "react";

export function Wrap({ children, className = "" }: { children: ReactNode; className?: string }) {
  return <div className={`wrap ${className}`.trim()}>{children}</div>;
}

export function Section({
  children,
  dark = false,
  className = "",
}: {
  children: ReactNode;
  dark?: boolean;
  className?: string;
}) {
  return <section className={`${dark ? "dark " : ""}sec ${className}`.trim()}>{children}</section>;
}

export function ButtonLink({
  href,
  children,
  variant = "ink",
  className = "",
}: {
  href: string;
  children: ReactNode;
  variant?: "accent" | "ink" | "ghost-dark" | "ghost-light";
  className?: string;
}) {
  return <Link className={`btn btn-${variant} ${className}`.trim()} href={href}>{children}</Link>;
}

export function Card({
  children,
  dark = false,
  className = "",
}: {
  children: ReactNode;
  dark?: boolean;
  className?: string;
}) {
  return <div className={`${dark ? "card-dark" : "card"} ${className}`.trim()}>{children}</div>;
}

export function Badge({
  children,
  tone = "ok",
}: {
  children: ReactNode;
  tone?: "ok" | "warn" | "bad" | "soon";
}) {
  return <span className={`badge badge-${tone}`}>{children}</span>;
}

export function Chip({ children }: { children: ReactNode }) {
  return <span className="chip">{children}</span>;
}

export function CheckList({ children }: { children: ReactNode }) {
  return <ul className="checks">{children}</ul>;
}

export function Grid({
  children,
  columns = 3,
}: {
  children: ReactNode;
  columns?: 2 | 3 | 4;
}) {
  return <div className={`grid-${columns}`}>{children}</div>;
}

export function PlanCard({
  name,
  description,
  price,
  period,
  features,
  href,
  action,
  featured = false,
}: {
  name: string;
  description: string;
  price: string;
  period: string;
  features: string[];
  href: string;
  action: string;
  featured?: boolean;
}) {
  return (
    <article className={`plan${featured ? " plan-hot" : ""}`}>
      <div>
        {featured ? <Badge>Most popular</Badge> : null}
        <h2 className="h3">{name}</h2>
        <p className="small plan-description">{description}</p>
      </div>
      <p className="price">₦{price}<span className="small"> {period}</span></p>
      <CheckList>{features.map((feature) => <li key={feature}>{feature}</li>)}</CheckList>
      <ButtonLink href={href} variant={featured ? "accent" : "ghost-light"}>{action}</ButtonLink>
    </article>
  );
}

export function MockPanel({
  title,
  children,
}: {
  title: ReactNode;
  children: ReactNode;
}) {
  return (
    <div className="mock">
      <div className="mock-h">{title}</div>
      {children}
    </div>
  );
}
