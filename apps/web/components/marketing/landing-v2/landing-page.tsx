"use client";

import Link from "next/link";
import { useState, type ReactNode } from "react";

function LogoMark({ className = "h-8 w-8" }: { className?: string }) {
  return (
    <svg viewBox="0 0 32 32" className={className} aria-hidden="true">
      <rect width="32" height="32" rx="9" fill="#22c55e" />
      <path
        d="M8 11.5l8 6 8-6M9.5 10h13A1.5 1.5 0 0124 11.5v9a1.5 1.5 0 01-1.5 1.5h-13A1.5 1.5 0 018 20.5v-9A1.5 1.5 0 019.5 10z"
        fill="none"
        stroke="#052e16"
        strokeWidth="2"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}

function Brand({ light = false }: { light?: boolean }) {
  return (
    <Link href="/" className="inline-flex items-center gap-2" aria-label="Sendwise home">
      <LogoMark />
      <span className={`text-[22px] font-extrabold tracking-[-0.03em] ${light ? "text-white" : "text-[#111827]"}`}>Sendwise</span>
    </Link>
  );
}

function Header() {
  return (
    <header className="sticky top-0 z-50 border-b border-[#eef0ee] bg-white/95 backdrop-blur">
      <div className="mx-auto flex h-[72px] max-w-[1240px] items-center justify-between px-6 lg:px-10">
        <Brand />
        <nav className="hidden items-center gap-8 text-[15px] font-medium text-[#374151] md:flex" aria-label="Main">
          <Link href="/platform" className="inline-flex items-center gap-1 hover:text-[#111827]">Product <ChevronDown /></Link>
          <Link href="/developers" className="hover:text-[#111827]">Resources</Link>
          <Link href="/pricing" className="hover:text-[#111827]">Pricing</Link>
        </nav>
        <div className="flex items-center gap-3">
          <Link href="/login" className="hidden px-3 py-2 text-[15px] font-semibold text-[#111827] hover:text-[#16a34a] sm:inline-flex">Log in</Link>
          <Link href="/register" className="inline-flex h-11 items-center justify-center rounded-full bg-[#22c55e] px-5 text-[15px] font-semibold text-white shadow-sm transition hover:bg-[#16a34a]">Get started</Link>
        </div>
      </div>
    </header>
  );
}

function ChevronDown() {
  return (
    <svg width="14" height="14" viewBox="0 0 20 20" fill="none" aria-hidden="true" className="mt-0.5 text-[#6b7280]">
      <path d="M5 7.5l5 5 5-5" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

function Hero() {
  return (
    <section className="bg-white px-6 pt-10 pb-8 lg:px-10 lg:pt-14">
      <div className="mx-auto max-w-[1240px]">
        <div className="mx-auto max-w-[760px] text-center">
          <div className="inline-flex items-center gap-2 rounded-full border border-[#e5e7eb] bg-white px-4 py-1.5 text-[13px] font-medium text-[#374151] shadow-sm">
            <span className="inline-flex h-5 w-5 items-center justify-center rounded-full bg-[#dcfce7] text-[#16a34a]">
              <svg viewBox="0 0 16 16" className="h-3 w-3" fill="none" aria-hidden="true"><path d="M3 8.5l3 3 7-7" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"/></svg>
            </span>
            100+ Email marketing platform
          </div>
          <h1 className="mt-6 text-[44px] font-extrabold leading-[1.05] tracking-[-0.04em] text-[#111827] sm:text-[58px] lg:text-[72px]">
            Automated marketing
            <br />
            that fuels growth.
          </h1>
          <p className="mx-auto mt-5 max-w-[620px] text-[17px] leading-[1.7] text-[#6b7280] sm:text-[18px]">
            Simplify your journey to success &ndash; from exploring leads to turning them into loyal customers, we&apos;re here to do the heavy lifting.
          </p>
          <div className="mt-8 flex justify-center">
            <Link href="/register" className="inline-flex h-12 items-center justify-center rounded-full bg-[#22c55e] px-7 text-[16px] font-semibold text-white shadow-sm transition hover:bg-[#16a34a]">
              Get started for free
            </Link>
          </div>
        </div>

        <div className="relative mx-auto mt-12 max-w-[1120px]">
          <div className="relative overflow-visible rounded-[28px] bg-gradient-to-b from-[#f3f4f6] to-[#eef2f1] px-4 pt-8 pb-0 sm:px-8 lg:px-12 lg:pt-10">
            {/* floating card: best seller */}
            <div className="absolute left-2 top-8 z-10 w-[210px] rounded-2xl bg-[#111827] p-4 text-white shadow-xl sm:left-6 lg:left-8 lg:top-14">
              <div className="flex items-start justify-between gap-2">
                <p className="text-[13px] font-semibold leading-snug">Meet this month&apos;s<br/>best seller</p>
                <span className="mt-0.5 inline-flex h-6 w-6 items-center justify-center rounded-full bg-white/10">↗</span>
              </div>
              <div className="mt-4 flex items-center gap-3">
                <div className="flex h-10 w-10 items-center justify-center rounded-full bg-gradient-to-br from-[#86efac] to-[#22c55e] text-sm font-bold text-[#052e16]">MA</div>
                <div>
                  <p className="text-[13px] font-semibold">Maya Andrews</p>
                  <p className="text-[11px] text-white/60">Top seller</p>
                </div>
              </div>
              <p className="mt-3 text-[11px] font-medium text-[#86efac]">▲ 30.5% from last month</p>
            </div>

            {/* floating card: clicked email */}
            <div className="absolute right-2 top-4 z-10 w-[190px] rounded-2xl border border-[#e5e7eb] bg-white p-4 shadow-lg sm:right-8 lg:right-16 lg:top-8">
              <p className="text-[28px] font-extrabold tracking-[-0.03em] text-[#111827]">90.5K</p>
              <p className="text-[12px] font-medium text-[#6b7280]">Clicked email</p>
              <svg viewBox="0 0 160 46" className="mt-2 h-10 w-full" aria-hidden="true">
                <polyline points="0,38 24,30 48,34 72,18 96,22 120,10 144,14 160,6" fill="none" stroke="#22c55e" strokeWidth="3" strokeLinecap="round" strokeLinejoin="round" />
                <circle cx="160" cy="6" r="4" fill="#22c55e" />
              </svg>
              <div className="mt-2 flex items-end justify-between border-t border-[#f3f4f6] pt-2">
                <div>
                  <p className="text-[15px] font-bold text-[#111827]">18.5K</p>
                  <p className="text-[11px] text-[#6b7280]">New users</p>
                </div>
                <span className="rounded-full bg-[#dcfce7] px-2 py-0.5 text-[11px] font-semibold text-[#16a34a]">12%</span>
              </div>
            </div>

            {/* floating card: newsletter */}
            <div className="absolute bottom-8 left-3 z-10 w-[220px] rounded-2xl border border-[#e5e7eb] bg-white p-4 shadow-lg sm:left-10 lg:bottom-10 lg:left-14">
              <div className="flex items-center justify-between">
                <span className="text-[12px] font-semibold text-[#111827]">Marketing</span>
                <span className="text-[11px] text-[#9ca3af]">⋯</span>
              </div>
              <p className="mt-1 text-[14px] font-bold text-[#111827]">Your monthly newsletter</p>
              <div className="mt-3 rounded-xl bg-[#f9fafb] p-3">
                <div className="flex gap-2">
                  <div className="h-12 w-12 flex-none rounded-lg bg-gradient-to-br from-[#d1fae5] to-[#a7f3d0]" />
                  <div className="flex-1 space-y-1.5">
                    <div className="h-2 w-full rounded bg-[#e5e7eb]" />
                    <div className="h-2 w-4/5 rounded bg-[#e5e7eb]" />
                    <div className="mt-2 h-5 w-14 rounded bg-[#22c55e]" />
                  </div>
                </div>
              </div>
            </div>

            {/* central person illustration */}
            <div className="relative mx-auto flex h-[360px] max-w-[720px] items-end justify-center sm:h-[420px] lg:h-[460px]">
              <div className="absolute bottom-0 left-1/2 h-[300px] w-[300px] -translate-x-1/2 rounded-full bg-[#dff7e8] sm:h-[340px] sm:w-[340px]" />
              <svg viewBox="0 0 320 420" className="relative z-[1] h-full w-auto max-w-[320px]" aria-label="Person with megaphone illustration">
                <defs>
                  <linearGradient id="shirt" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stopColor="#fef3c7"/><stop offset="1" stopColor="#fde68a"/></linearGradient>
                </defs>
                {/* body */}
                <ellipse cx="160" cy="410" rx="90" ry="18" fill="#000" opacity="0.06" />
                <path d="M95 250c0-40 29-70 65-70s65 30 65 70v120c0 20-130 20-130 0V250z" fill="url(#shirt)" />
                <rect x="120" y="320" width="35" height="90" rx="12" fill="#1f2937" />
                <rect x="165" y="320" width="35" height="90" rx="12" fill="#1f2937" />
                {/* neck */}
                <rect x="145" y="155" width="30" height="35" rx="10" fill="#f1c27d" />
                {/* head */}
                <ellipse cx="160" cy="120" rx="48" ry="54" fill="#f1c27d" />
                {/* hair */}
                <path d="M112 115c0-40 20-60 48-60s48 20 48 60c0 10-4 10-8 0-4-22-18-34-40-34s-36 12-40 34c-4 10-8 10-8 0z" fill="#3f2a1d" />
                <ellipse cx="112" cy="130" rx="10" ry="24" fill="#3f2a1d" />
                <ellipse cx="208" cy="130" rx="10" ry="24" fill="#3f2a1d" />
                {/* face */}
                <circle cx="143" cy="118" r="4" fill="#1f2937" />
                <circle cx="177" cy="118" r="4" fill="#1f2937" />
                <path d="M145 142c8 8 22 8 30 0" stroke="#b45309" strokeWidth="3" fill="none" strokeLinecap="round" />
                {/* arm holding megaphone */}
                <path d="M205 230c30-20 55-55 60-80" stroke="#f1c27d" strokeWidth="18" strokeLinecap="round" fill="none" />
                {/* megaphone */}
                <g transform="translate(250,120) rotate(20)">
                  <path d="M0 20 L55 0 L55 50 L0 30 Z" fill="#facc15" stroke="#ca8a04" strokeWidth="2" />
                  <rect x="-14" y="15" width="18" height="20" rx="4" fill="#eab308" stroke="#ca8a04" strokeWidth="2" />
                  <path d="M55 10 Q80 25 55 40" fill="none" stroke="#ca8a04" strokeWidth="3" />
                  <path d="M62 5 Q95 25 62 45" fill="none" stroke="#fde047" strokeWidth="2" opacity="0.8" />
                </g>
                {/* left arm */}
                <path d="M115 235c-20 10-35 30-40 55" stroke="#f1c27d" strokeWidth="18" strokeLinecap="round" fill="none" />
              </svg>
              {/* floating badge */}
              <div className="absolute right-8 bottom-24 z-10 flex h-12 w-12 items-center justify-center rounded-full bg-white shadow-md lg:right-24">
                <span className="text-[#22c55e] text-xl">✦</span>
              </div>
            </div>
          </div>
        </div>
      </div>
    </section>
  );
}

function LogoStrip() {
  const logos = ["amazon", "slack", "SAMSUNG", "Spotify", "Coca-Cola"];
  return (
    <section className="bg-white px-6 py-10 lg:px-10">
      <div className="mx-auto flex max-w-[1100px] flex-wrap items-center justify-center gap-x-12 gap-y-6 opacity-50 grayscale">
        {logos.map((name) => (
          <span key={name} className="text-[22px] font-bold tracking-tight text-[#374151] sm:text-[26px]" style={{ fontFamily: name === "Spotify" ? "inherit" : undefined }}>
            {name === "slack" ? "slack" : name === "amazon" ? "amazon" : name}
          </span>
        ))}
      </div>
    </section>
  );
}

function TwoCards() {
  return (
    <section className="bg-[#f7f7f5] px-6 py-16 lg:px-10 lg:py-20">
      <div className="mx-auto grid max-w-[1240px] gap-6 md:grid-cols-2">
        <div className="rounded-[28px] border border-[#eceeea] bg-white p-6 shadow-sm sm:p-8">
          <div className="overflow-hidden rounded-2xl bg-[#f3f4f6] p-4">
            <div className="rounded-xl border border-[#e5e7eb] bg-white p-4 shadow-sm">
              <div className="flex items-center justify-between text-[12px] font-semibold text-[#111827]"><span>Online store</span><span className="text-[#22c55e]">● Live</span></div>
              <div className="mt-3 grid grid-cols-2 gap-3">
                <div className="rounded-lg bg-[#f9fafb] p-3"><div className="h-16 rounded-md bg-gradient-to-br from-[#e5e7eb] to-[#f3f4f6]" /><div className="mt-2 h-2 w-3/4 rounded bg-[#e5e7eb]" /><div className="mt-1 h-2 w-1/2 rounded bg-[#e5e7eb]" /></div>
                <div className="rounded-lg bg-[#f9fafb] p-3"><div className="h-16 rounded-md bg-gradient-to-br from-[#dcfce7] to-[#f0fdf4]" /><div className="mt-2 h-2 w-3/4 rounded bg-[#e5e7eb]" /><div className="mt-1 h-2 w-1/2 rounded bg-[#e5e7eb]" /></div>
              </div>
              <div className="mt-3 rounded-lg bg-[#111827] px-3 py-2 text-center text-[12px] font-semibold text-white">Shop new arrivals</div>
            </div>
          </div>
          <h3 className="mt-6 text-[28px] font-extrabold tracking-[-0.03em] text-[#111827]">I sell products online</h3>
          <p className="mt-2 max-w-[420px] text-[15px] leading-[1.7] text-[#6b7280]">Drive more sales with powerful e-commerce tools, automations, and personalized product recommendations.</p>
          <Link href="/platform" className="mt-5 inline-flex items-center gap-1 text-[15px] font-semibold text-[#111827] hover:text-[#16a34a]">Learn more <span aria-hidden="true">→</span></Link>
        </div>

        <div className="rounded-[28px] border border-[#eceeea] bg-white p-6 shadow-sm sm:p-8">
          <div className="overflow-hidden rounded-2xl bg-[#f3f4f6] p-4">
            <div className="rounded-xl border border-[#e5e7eb] bg-white p-4 shadow-sm">
              <div className="flex items-center justify-between text-[12px] font-semibold text-[#111827]"><span>Service booking</span><span className="rounded-full bg-[#dcfce7] px-2 py-0.5 text-[10px] text-[#16a34a]">Confirmed</span></div>
              <div className="mt-3 space-y-2">
                <div className="flex items-center gap-3 rounded-lg border border-[#f3f4f6] p-2"><div className="h-10 w-10 rounded-lg bg-[#eef2ff]" /><div className="flex-1"><div className="h-2 w-2/3 rounded bg-[#e5e7eb]" /><div className="mt-1 h-2 w-1/2 rounded bg-[#f3f4f6]" /></div></div>
                <div className="flex items-center gap-3 rounded-lg border border-[#f3f4f6] p-2"><div className="h-10 w-10 rounded-lg bg-[#ecfeff]" /><div className="flex-1"><div className="h-2 w-2/3 rounded bg-[#e5e7eb]" /><div className="mt-1 h-2 w-1/2 rounded bg-[#f3f4f6]" /></div></div>
              </div>
              <div className="mt-3 grid grid-cols-2 gap-2"><div className="rounded-lg bg-[#f9fafb] py-2 text-center text-[11px] font-semibold text-[#374151]">View details</div><div className="rounded-lg bg-[#22c55e] py-2 text-center text-[11px] font-semibold text-white">Book now</div></div>
            </div>
          </div>
          <h3 className="mt-6 text-[28px] font-extrabold tracking-[-0.03em] text-[#111827]">I sell services &amp; experiences</h3>
          <p className="mt-2 max-w-[420px] text-[15px] leading-[1.7] text-[#6b7280]">Nurture leads and convert clients with appointment reminders, follow-ups, and tailored email journeys.</p>
          <Link href="/platform" className="mt-5 inline-flex items-center gap-1 text-[15px] font-semibold text-[#111827] hover:text-[#16a34a]">Learn more <span aria-hidden="true">→</span></Link>
        </div>
      </div>
    </section>
  );
}

export function LandingPage() {
  const [toolTab, setToolTab] = useState<"automated" | "powerful" | "segment">("powerful");
  const [annual, setAnnual] = useState(true);

  return (
    <div className="min-h-screen bg-white text-[#111827]" style={{ fontFamily: "var(--font-manrope), Manrope, system-ui, sans-serif" }}>
      <Header />
      <main>
        <Hero />
        <LogoStrip />
        <TwoCards />

        {/* Scale section */}
        <section className="bg-white px-6 py-16 lg:px-10 lg:py-24">
          <div className="mx-auto grid max-w-[1240px] items-center gap-12 lg:grid-cols-[1fr_1.05fr]">
            <div>
              <span className="text-[13px] font-bold uppercase tracking-[0.14em] text-[#16a34a]">How we scale</span>
              <h2 className="mt-3 text-[38px] font-extrabold leading-[1.1] tracking-[-0.03em] sm:text-[46px]">Scale up your email<br/>marketing program</h2>
              <div className="mt-10 space-y-8">
                {[
                  { n: "01", t: "Identify", d: "Attract and collect leads with tools that help you find the right audience and grow your contact list faster." },
                  { n: "02", t: "Engage", d: "Build relationships through personalized content, timely follow-ups, and campaigns that feel relevant to every subscriber." },
                  { n: "03", t: "Convert", d: "Drive actions and track sales with automated journeys that turn interested prospects into loyal customers." },
                ].map((s) => (
                  <div key={s.n} className="grid grid-cols-[auto_1fr] gap-4">
                    <span className="pt-0.5 text-[14px] font-bold text-[#22c55e]">{s.n}</span>
                    <div>
                      <h3 className="text-[22px] font-bold tracking-[-0.02em] text-[#111827]">{s.t}</h3>
                      <p className="mt-1 max-w-[420px] text-[15px] leading-[1.7] text-[#6b7280]">{s.d}</p>
                    </div>
                  </div>
                ))}
              </div>
            </div>
            <div className="relative">
              <div className="rounded-[28px] bg-[#f3f4f6] p-6 sm:p-8">
                <div className="mx-auto max-w-[420px] overflow-hidden rounded-2xl border border-[#e5e7eb] bg-white shadow-lg">
                  <div className="bg-[#f9fafb] px-5 py-3 text-center text-[12px] font-medium text-[#6b7280]">Welcome, You&apos;re in</div>
                  <div className="p-6 text-center">
                    <div className="mx-auto flex h-14 w-14 items-center justify-center rounded-2xl bg-[#dcfce7]"><LogoMark className="h-8 w-8" /></div>
                    <h4 className="mt-4 text-[20px] font-extrabold text-[#111827]">Welcome aboard!</h4>
                    <p className="mt-2 text-[14px] leading-[1.7] text-[#6b7280]">Thanks for joining. We&apos;re excited to help you create campaigns that connect with your audience.</p>
                    <div className="mt-5 inline-flex h-10 items-center rounded-full bg-[#111827] px-6 text-[14px] font-semibold text-white">Visit site</div>
                    <div className="mt-5 h-28 rounded-xl bg-gradient-to-br from-[#e0f2fe] via-[#f0fdf4] to-[#dcfce7]" />
                  </div>
                </div>
                <div className="pointer-events-none absolute -left-2 bottom-16 flex -space-x-2 sm:left-2">
                  {["#fca5a5","#93c5fd","#86efac"].map((c,i)=>(<span key={i} className="inline-flex h-10 w-10 items-center justify-center rounded-full border-2 border-white text-xs font-bold text-white shadow" style={{background:c}}>{["A","B","C"][i]}</span>))}
                </div>
                <div className="pointer-events-none absolute -right-1 top-10 rounded-xl border border-[#e5e7eb] bg-white px-3 py-2 shadow-md sm:right-4">
                  <p className="text-[11px] font-semibold text-[#111827]">New subscriber</p>
                  <p className="text-[10px] text-[#16a34a]">just now</p>
                </div>
              </div>
            </div>
          </div>
        </section>

        {/* Maximize profits / tools */}
        <section className="bg-[#f7f7f5] px-6 py-16 lg:px-10 lg:py-24">
          <div className="mx-auto max-w-[1240px]">
            <div className="mx-auto max-w-[760px] text-center">
              <span className="text-[12px] font-bold uppercase tracking-[0.16em] text-[#16a34a]">Automate your journey</span>
              <h2 className="mt-3 text-[38px] font-extrabold leading-[1.1] tracking-[-0.03em] sm:text-[46px]">Maximize profits with the<br/>power of our tools</h2>
              <div className="mt-8 flex flex-wrap items-center justify-center gap-2">
                {([
                  ["automated","Automated campaigns"],
                  ["powerful","Powerful automations"],
                  ["segment","Segment & custom fields"],
                ] as const).map(([k,label]) => (
                  <button key={k} type="button" onClick={() => setToolTab(k)} className={`rounded-full px-5 py-2.5 text-[14px] font-semibold transition ${toolTab===k?"bg-[#22c55e] text-white shadow-sm":"bg-white text-[#374151] border border-[#e5e7eb] hover:border-[#d1d5db]"}`}>
                    {label}
                  </button>
                ))}
              </div>
            </div>

            <div className="mt-12 grid items-center gap-8 rounded-[32px] border border-[#eceeea] bg-white p-6 shadow-sm lg:grid-cols-[0.95fr_1.1fr_0.95fr] lg:p-10">
              <div className="order-2 lg:order-1">
                <div className="mx-auto max-w-[280px] rounded-[28px] border border-[#e5e7eb] bg-[#f9fafb] p-3 shadow-md">
                  <div className="rounded-[22px] border border-[#e5e7eb] bg-white p-4">
                    <div className="mx-auto mb-3 h-1.5 w-16 rounded-full bg-[#e5e7eb]" />
                    <p className="text-center text-[13px] font-bold text-[#111827]">Did you forget something?</p>
                    <div className="mt-3 rounded-xl bg-[#f3f4f6] p-3">
                      <div className="flex gap-3">
                        <div className="h-14 w-14 rounded-lg bg-gradient-to-br from-[#d1d5db] to-[#e5e7eb]" />
                        <div className="flex-1">
                          <div className="h-2 w-full rounded bg-[#e5e7eb]" />
                          <div className="mt-1.5 h-2 w-2/3 rounded bg-[#e5e7eb]" />
                          <p className="mt-2 text-[11px] font-bold text-[#111827]">$49.00</p>
                        </div>
                      </div>
                    </div>
                    <p className="mt-3 text-center text-[11px] leading-[1.6] text-[#6b7280]">You left items in your cart. Complete your purchase before they&apos;re gone.</p>
                    <div className="mt-3 rounded-lg bg-[#22c55e] py-2 text-center text-[12px] font-semibold text-white">Return to cart</div>
                  </div>
                </div>
              </div>
              <div className="order-1 text-center lg:order-2 lg:px-4 lg:text-left">
                <h3 className="text-[34px] font-extrabold leading-[1.15] tracking-[-0.03em] text-[#111827] sm:text-[40px]">Seamless outcomes through marketing automation</h3>
                <p className="mt-4 text-[16px] leading-[1.75] text-[#6b7280]">Execute your entire marketing strategy &ndash; from a single welcome email to complex, multi-step journeys &ndash; that sends the right message at the right time, every time.</p>
                <div className="mt-7 flex flex-wrap items-center justify-center gap-3 lg:justify-start">
                  <Link href="/register" className="inline-flex h-11 items-center rounded-full bg-[#22c55e] px-6 text-[15px] font-semibold text-white hover:bg-[#16a34a]">Get started</Link>
                  <Link href="/platform" className="inline-flex h-11 items-center gap-2 rounded-full px-4 text-[15px] font-semibold text-[#111827] hover:bg-[#f3f4f6]"><span className="inline-flex h-7 w-7 items-center justify-center rounded-full border border-[#d1d5db]"><svg viewBox="0 0 16 16" className="h-3 w-3 fill-[#111827]" aria-hidden="true"><path d="M5 3.5v9l7-4.5-7-4.5z"/></svg></span>See how</Link>
                </div>
              </div>
              <div className="order-3 space-y-4">
                {[
                  { t: "Email & text message workflows", d: "Automate multi-channel sequences." },
                  { t: "Custom fields & personalization", d: "Tailor every message with data." },
                  { t: "Promote automation sequences", d: "Launch journeys that convert." },
                ].map((f) => (
                  <div key={f.t} className="flex items-start gap-3 rounded-2xl border border-[#f0f0ee] bg-[#fafaf8] p-4">
                    <span className="mt-0.5 inline-flex h-8 w-8 flex-none items-center justify-center rounded-full bg-[#dcfce7] text-[#16a34a]"><svg viewBox="0 0 16 16" className="h-4 w-4" fill="none" aria-hidden="true"><path d="M3.5 8.5l3 3 6-6" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"/></svg></span>
                    <div><p className="text-[14px] font-bold text-[#111827]">{f.t}</p><p className="text-[13px] text-[#6b7280]">{f.d}</p></div>
                  </div>
                ))}
              </div>
            </div>

            <div className="mt-8 grid gap-4 text-center sm:grid-cols-3">
              {[
                { k: "Easy to use", v: "10min", icon: "◷" },
                { k: "Fully customizable", v: "", icon: "✎" },
                { k: "Cross-channel focus", v: "", icon: "✦" },
              ].map((s) => (
                <div key={s.k} className="flex items-center justify-center gap-3 rounded-2xl bg-white py-4 px-5 border border-[#eceeea]">
                  <span className="text-[18px] text-[#22c55e]" aria-hidden="true">{s.icon}</span>
                  <span className="text-[14px] text-[#6b7280]">{s.k}</span>
                  {s.v ? <span className="text-[15px] font-extrabold text-[#111827]">{s.v}</span> : null}
                </div>
              ))}
            </div>
          </div>
        </section>

        {/* Pricing */}
        <section className="bg-white px-6 py-16 lg:px-10 lg:py-24">
          <div className="mx-auto max-w-[1240px]">
            <div className="mx-auto max-w-[760px] text-center">
              <span className="text-[12px] font-bold uppercase tracking-[0.16em] text-[#16a34a]">Customizable pricing</span>
              <h2 className="mt-3 text-[38px] font-extrabold leading-[1.1] tracking-[-0.03em] sm:text-[46px]">Plans to support you every<br/>step of the way</h2>
              <div className="mt-6 inline-flex items-center rounded-full border border-[#e5e7eb] bg-[#f9fafb] p-1 text-[14px] font-semibold">
                <button type="button" onClick={() => setAnnual(false)} className={`rounded-full px-5 py-2 ${!annual?"bg-white text-[#111827] shadow-sm":"text-[#6b7280]"}`}>Monthly</button>
                <button type="button" onClick={() => setAnnual(true)} className={`rounded-full px-5 py-2 ${annual?"bg-[#22c55e] text-white shadow-sm":"text-[#6b7280]"}`}>Annually</button>
              </div>
            </div>

            <div className="mx-auto mt-12 grid max-w-[980px] gap-8 rounded-[32px] border border-[#eceeea] bg-[#fbfbfa] p-6 sm:p-10 lg:grid-cols-[0.95fr_1.05fr]">
              <div className="flex flex-col justify-center">
                <p className="text-[14px] font-medium text-[#6b7280]">Starting at</p>
                <p className="mt-2 text-[56px] font-extrabold leading-none tracking-[-0.04em] text-[#111827]">{annual?"$240":"$300"}<span className="ml-1 text-[22px] font-bold tracking-normal text-[#6b7280]">/month</span></p>
                <p className="mt-3 text-[14px] text-[#6b7280]">Get 20% off on the annual plan. Cancel anytime.</p>
                <div className="mt-8">
                  <Link href="/register" className="inline-flex h-12 items-center justify-center rounded-full bg-[#22c55e] px-8 text-[16px] font-semibold text-white hover:bg-[#16a34a]">Start for Free</Link>
                </div>
              </div>
              <div className="rounded-[24px] border border-[#e5e7eb] bg-white p-6 sm:p-8">
                <p className="text-[18px] font-bold text-[#111827]">Included in price:</p>
                <ul className="mt-5 space-y-3.5">
                  {[
                    "5000+ email subscribers",
                    "Unlimited email per mo sends",
                    "Advanced automation",
                    "Advanced segmentation",
                    "Multivariate testing",
                    "Chat and email support",
                    "Free migration",
                  ].map((item) => (
                    <li key={item} className="flex items-center gap-3 text-[15px] text-[#374151]">
                      <span className="inline-flex h-6 w-6 flex-none items-center justify-center rounded-full border border-[#22c55e] text-[#22c55e]"><svg viewBox="0 0 16 16" className="h-3.5 w-3.5" fill="none" aria-hidden="true"><path d="M3.5 8.5l3 3 6-6" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"/></svg></span>
                      {item}
                    </li>
                  ))}
                </ul>
              </div>
            </div>
          </div>
        </section>

        {/* Need larger plan */}
        <section className="px-6 lg:px-10">
          <div className="mx-auto flex max-w-[1240px] flex-col items-start justify-between gap-6 rounded-[28px] bg-[#111827] px-8 py-10 text-white sm:flex-row sm:items-center sm:px-12">
            <div>
              <h3 className="text-[30px] font-extrabold tracking-[-0.02em]">Need a larger plan?</h3>
              <p className="mt-2 max-w-[620px] text-[15px] leading-[1.7] text-[#9ca3af]">Our expert team will help you choose the plan that suits your organization best.</p>
            </div>
            <Link href="/contact" className="inline-flex h-11 flex-none items-center gap-2 rounded-full border border-white/20 px-6 text-[15px] font-semibold text-white hover:bg-white/10">Let&apos;s chat <span aria-hidden="true">→</span></Link>
          </div>
        </section>

        {/* Testimonial */}
        <section className="bg-white px-6 py-16 lg:px-10 lg:py-24">
          <div className="mx-auto max-w-[1240px]">
            <div className="mx-auto max-w-[760px] text-center">
              <p className="text-[12px] font-bold uppercase tracking-[0.16em] text-[#6b7280]">Customer stories</p>
              <h2 className="mt-3 text-[38px] font-extrabold leading-[1.1] tracking-[-0.03em] sm:text-[46px]">Trusted by 180,000+ successful<br className="hidden sm:block"/> businesses worldwide</h2>
            </div>
            <div className="mx-auto mt-12 grid max-w-[1040px] items-stretch overflow-hidden rounded-[32px] border border-[#eceeea] bg-[#fbfbfa] lg:grid-cols-[1.1fr_0.9fr]">
              <div className="flex flex-col justify-between p-8 sm:p-12">
                <div>
                  <div className="flex items-center gap-2 text-[28px] font-extrabold tracking-tight text-[#111827]">
                    <span className="inline-flex h-8 w-8 items-center justify-center rounded-md bg-[#ef4444] text-white text-[16px] font-black">T</span>
                    Telkomsel
                  </div>
                  <p className="mt-8 text-[22px] leading-[1.6] font-medium text-[#111827] sm:text-[26px]">
                    &ldquo;With an affordable price for the tool, world-class support, and ongoing success, Telkomsel is worth every cent &ndash; while easily self financing itself through revenue generated.&rdquo;
                  </p>
                </div>
                <div className="mt-10 flex items-end justify-between gap-4">
                  <div>
                    <p className="text-[16px] font-bold text-[#111827]">Christian Sugiono</p>
                    <p className="text-[14px] text-[#6b7280]">Marcomm at Telkomsel</p>
                  </div>
                  <div className="flex gap-2">
                    <button type="button" aria-label="Previous story" className="inline-flex h-10 w-10 items-center justify-center rounded-full border border-[#d1d5db] text-[#374151] hover:bg-white">‹</button>
                    <button type="button" aria-label="Next story" className="inline-flex h-10 w-10 items-center justify-center rounded-full border border-[#d1d5db] text-[#374151] hover:bg-white">›</button>
                  </div>
                </div>
              </div>
              <div className="relative min-h-[320px] bg-gradient-to-br from-[#e5e7eb] via-[#f3f4f6] to-[#d1fae5] lg:min-h-full">
                <svg viewBox="0 0 420 520" className="absolute inset-0 h-full w-full" aria-label="Customer portrait" preserveAspectRatio="xMidYMid slice">
                  <rect width="420" height="520" fill="url(#pg)" />
                  <defs>
                    <linearGradient id="pg" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stopColor="#e5e7eb"/><stop offset="1" stopColor="#d1fae5"/></linearGradient>
                  </defs>
                  <circle cx="210" cy="520" r="260" fill="#d1d5db" opacity="0.5" />
                  <ellipse cx="210" cy="250" rx="70" ry="80" fill="#d4a574" />
                  <path d="M140 230c0-55 30-85 70-85s70 30 70 85c0 8-6 8-10 0-8-30-28-48-60-48s-52 18-60 48c-4 8-10 8-10 0z" fill="#1f2937" />
                  <rect x="188" y="310" width="44" height="50" rx="12" fill="#d4a574" />
                  <path d="M110 360c10-30 55-45 100-45s90 15 100 45v160H110V360z" fill="#111827" />
                  <path d="M165 360l45 70 45-70" fill="#f9fafb" opacity="0.9" />
                  <circle cx="185" cy="245" r="5" fill="#1f2937" />
                  <circle cx="235" cy="245" r="5" fill="#1f2937" />
                  <path d="M192 280c10 10 26 10 36 0" stroke="#92400e" strokeWidth="4" fill="none" strokeLinecap="round" />
                </svg>
              </div>
            </div>
          </div>
        </section>

        {/* Integrations */}
        <section className="bg-[#f7f7f5] px-6 py-16 lg:px-10 lg:py-24">
          <div className="mx-auto max-w-[1240px] text-center">
            <span className="text-[12px] font-bold uppercase tracking-[0.16em] text-[#16a34a]">Integrations</span>
            <h2 className="mt-3 text-[38px] font-extrabold leading-[1.1] tracking-[-0.03em] sm:text-[46px]">Bring in more data, drive<br/>more growth</h2>
            <div className="mx-auto mt-12 grid max-w-[900px] grid-cols-3 gap-4 sm:grid-cols-4 lg:grid-cols-6">
              {[
                ["Shopify","#95bf47","S"],["Zapier","#ff4a00","Z"],["Stripe","#635bff","S"],["Woo","#96588a","W"],["HubSpot","#ff7a59","H"],["Analytics","#f9ab00","A"],
                ["Mailchimp","#ffe01b","M"],["WordPress","#21759b","W"],["Squarespace","#111827","S"],["Salesforce","#00a1e0","S"],["Slack","#4a154b","Sl"],["Eventbrite","#f05537","E"],
              ].map(([name,color,abbr]) => (
                <div key={name as string} className="flex flex-col items-center gap-2 rounded-2xl border border-[#e8eae6] bg-white p-4 shadow-sm">
                  <span className="inline-flex h-12 w-12 items-center justify-center rounded-2xl text-white text-[16px] font-extrabold" style={{background: color as string}}>{abbr as string}</span>
                  <span className="text-[12px] font-semibold text-[#374151]">{name as string}</span>
                </div>
              ))}
            </div>
          </div>
        </section>

        {/* Big CTA */}
        <section className="px-6 py-8 lg:px-10">
          <div className="mx-auto max-w-[1240px] rounded-[36px] bg-gradient-to-r from-[#3b82f6] via-[#6366f1] to-[#8b5cf6] px-8 py-16 text-center text-white sm:px-12 sm:py-20">
            <h2 className="text-[38px] font-extrabold leading-[1.1] tracking-[-0.03em] sm:text-[52px]">Reach your target audience<br/>and grow your sales</h2>
            <div className="mt-8">
              <Link href="/register" className="inline-flex h-12 items-center justify-center rounded-full bg-white px-8 text-[16px] font-semibold text-[#111827] hover:bg-[#f3f4f6]">Get started</Link>
            </div>
          </div>
        </section>

        {/* Subscribe */}
        <section className="bg-white px-6 py-16 lg:px-10">
          <div className="mx-auto flex max-w-[1240px] flex-col items-start justify-between gap-8 border-t border-[#eef0ee] pt-12 sm:flex-row sm:items-center">
            <div>
              <h3 className="text-[28px] font-extrabold tracking-[-0.02em] text-[#111827]">Subscribe</h3>
              <p className="mt-1 text-[15px] text-[#6b7280]">Stay up to date with the latest news, announcements, and articles.</p>
            </div>
            <form className="flex w-full max-w-[460px] flex-col gap-3 sm:flex-row" onSubmit={(e) => e.preventDefault()}>
              <label htmlFor="subscribe-email" className="sr-only">Email address</label>
              <input id="subscribe-email" type="email" placeholder="Enter your email" className="h-12 flex-1 rounded-full border border-[#d1d5db] px-5 text-[15px] outline-none focus:border-[#22c55e] focus:ring-2 focus:ring-[#22c55e]/20" />
              <button type="submit" className="h-12 rounded-full bg-[#22c55e] px-7 text-[15px] font-semibold text-white hover:bg-[#16a34a]">Subscribe</button>
            </form>
          </div>
        </section>
      </main>

      <footer className="bg-[#111827] px-6 pt-14 pb-8 text-white lg:px-10">
        <div className="mx-auto max-w-[1240px]">
          <div className="grid gap-10 border-b border-white/10 pb-12 sm:grid-cols-2 lg:grid-cols-[1.4fr_repeat(4,1fr)]">
            <div>
              <Brand light />
              <p className="mt-4 max-w-[280px] text-[14px] leading-[1.7] text-[#9ca3af]">Automated marketing that fuels growth and helps you connect with your audience.</p>
              <div className="mt-5 flex gap-3">
                {["X","f","in","ig"].map((s) => (
                  <span key={s} className="inline-flex h-9 w-9 items-center justify-center rounded-full border border-white/15 text-[13px] font-bold text-[#d1d5db]">{s}</span>
                ))}
              </div>
            </div>
            {[
              { h: "Product", links: [["Features","/platform"],["Integrations","/platform"],["Pricing","/pricing"],["Changelog","/developers"]] },
              { h: "Resources", links: [["Blog","/developers"],["Help Center","/contact"],["Community","/about"],["Developers","/developers"]] },
              { h: "Company", links: [["About","/about"],["Careers","/about"],["Contact","/contact"],["Partners","/contact"]] },
              { h: "Support", links: [["Terms","/legal"],["Privacy","/legal"],["Security","/legal"],["Status","/developers"]] },
            ].map((col) => (
              <div key={col.h}>
                <h4 className="text-[14px] font-bold text-white">{col.h}</h4>
                <ul className="mt-4 space-y-2.5">
                  {col.links.map(([label, href]) => (
                    <li key={label}><Link href={href} className="text-[14px] text-[#9ca3af] hover:text-white">{label}</Link></li>
                  ))}
                </ul>
              </div>
            ))}
          </div>
          <div className="flex flex-col gap-2 pt-6 text-[13px] text-[#6b7280] sm:flex-row sm:items-center sm:justify-between">
            <span>© {new Date().getFullYear()} Sendwise. All rights reserved.</span>
            <span>Automated marketing platform</span>
          </div>
        </div>
      </footer>
    </div>
  );
}
