"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { useEffect, useRef, useState } from "react";
import { Moon, Sun } from "lucide-react";

const navigation = [
  { href: "/platform", label: "Platform", key: "platform" },
  { href: "/developers", label: "Developers", key: "developers" },
  { href: "/pricing", label: "Pricing", key: "pricing" },
  { href: "/about", label: "About", key: "about" },
  { href: "/contact", label: "Contact", key: "contact" },
] as const;

function Logo() {
  return (
    <Link href="/" className="marketing-logo" aria-label="Sendwise home">
      <svg width="32" height="32" viewBox="0 0 32 32" aria-hidden="true">
        <rect width="32" height="32" rx="9" fill="#BFF1CC" />
        <path d="m8 11.5 8 6 8-6M9.5 10h13a1.5 1.5 0 0 1 1.5 1.5v9a1.5 1.5 0 0 1-1.5 1.5h-13A1.5 1.5 0 0 1 8 20.5v-9A1.5 1.5 0 0 1 9.5 10Z" fill="none" stroke="#111111" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
      </svg>
      <span>Sendwise</span>
    </Link>
  );
}

export function SiteHeader({
  theme,
  onToggleTheme,
}: {
  theme: "light" | "dark";
  onToggleTheme: () => void;
}) {
  const pathname = usePathname();
  const [menuOpen, setMenuOpen] = useState(false);
  const menuButton = useRef<HTMLButtonElement>(null);
  const menu = useRef<HTMLElement>(null);

  useEffect(() => {
    if (!menuOpen) return;
    menu.current?.querySelector<HTMLAnchorElement>("a")?.focus();
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        setMenuOpen(false);
        menuButton.current?.focus();
      }
    };
    window.addEventListener("keydown", closeOnEscape);
    return () => window.removeEventListener("keydown", closeOnEscape);
  }, [menuOpen]);

  const closeMenu = () => setMenuOpen(false);

  return (
    <header className="marketing-header">
      <div className="wrap marketing-header-inner">
        <Logo />
        <button
          type="button"
          className="marketing-theme-toggle"
          aria-label={theme === "light" ? "Switch to dark mode" : "Switch to light mode"}
          aria-pressed={theme === "dark"}
          onClick={onToggleTheme}
        >
          {theme === "light" ? <Moon size={16} aria-hidden="true" /> : <Sun size={16} aria-hidden="true" />}
          <span>{theme === "light" ? "Dark" : "Light"}</span>
        </button>
        <button
          ref={menuButton}
          type="button"
          className="marketing-menu-button"
          aria-label={menuOpen ? "Close navigation menu" : "Open navigation menu"}
          aria-expanded={menuOpen}
          aria-controls="marketing-navigation"
          onClick={() => setMenuOpen((open) => !open)}
        >
          <span aria-hidden="true">{menuOpen ? "×" : "☰"}</span>
        </button>
        <nav
          ref={menu}
          id="marketing-navigation"
          className={`marketing-nav${menuOpen ? " is-open" : ""}`}
          aria-label="Main"
        >
          {navigation.map((item) => {
            const active = pathname === item.href;
            return (
              <Link
                key={item.key}
                href={item.href}
                aria-current={active ? "page" : undefined}
                onClick={closeMenu}
              >
                {item.label}
              </Link>
            );
          })}
          <div className="marketing-nav-mobile-actions">
            <Link href="/login" className="btn btn-ghost-dark" onClick={closeMenu}>Sign in</Link>
            <Link href="/register" className="btn btn-accent" onClick={closeMenu}>Start sending</Link>
          </div>
        </nav>
        <div className="marketing-header-actions">
          <Link href="/login" className="btn btn-ghost-dark">Sign in</Link>
          <Link href="/register" className="btn btn-accent">Start sending</Link>
        </div>
      </div>
    </header>
  );
}

const footerColumns = [
  {
    title: "Platform",
    links: [
      ["Campaigns", "/platform"],
      ["Transactional email", "/developers"],
      ["Domains and DNS", "/platform"],
      ["Templates", "/platform"],
      ["Pricing", "/pricing"],
    ],
  },
  {
    title: "Developers",
    links: [
      ["Documentation", "/developers"],
      ["REST API", "/developers"],
      ["SMTP", "/developers"],
      ["Webhooks", "/developers"],
    ],
  },
  {
    title: "Company",
    links: [
      ["About", "/about"],
      ["Contact", "/contact"],
      ["Sign in", "/login"],
      ["Create account", "/register"],
    ],
  },
  {
    title: "Legal",
    links: [
      ["Privacy", "/legal/privacy"],
      ["Terms", "/legal/terms"],
      ["Acceptable use", "/legal/acceptable-use"],
      ["Data protection", "/legal/data-protection"],
    ],
  },
] as const;

export function SiteFooter() {
  const year = new Date().getFullYear();
  return (
    <footer className="marketing-footer">
      <div className="wrap marketing-footer-inner">
        <div className="foot-grid">
          <div>
            <Logo />
            <p className="marketing-footer-description">
              Email infrastructure for campaigns and application email, sent from your own domain.
            </p>
          </div>
          {footerColumns.map((column) => (
            <div key={column.title}>
              <h2>{column.title}</h2>
              {column.links.map(([label, href]) => (
                <Link href={href} key={label}>{label}</Link>
              ))}
            </div>
          ))}
        </div>
        <div className="marketing-footer-bottom">
          <span>© {year} Sendwise. All rights reserved.</span>
        </div>
      </div>
    </footer>
  );
}

export function PublicFrame({
  children,
  active,
}: {
  children: React.ReactNode;
  active?: string;
}) {
  const [theme, setTheme] = useState<"light" | "dark">("light");

  useEffect(() => {
    const storedTheme = window.localStorage.getItem("sendwise-marketing-theme");
    if (storedTheme === "light" || storedTheme === "dark") setTheme(storedTheme);
  }, []);

  const toggleTheme = () => {
    setTheme((currentTheme) => {
      const nextTheme = currentTheme === "light" ? "dark" : "light";
      window.localStorage.setItem("sendwise-marketing-theme", nextTheme);
      return nextTheme;
    });
  };

  return (
    <div className="marketing" data-theme={theme}>
      <a className="marketing-skip-link" href="#main-content">Skip to content</a>
      <SiteHeader theme={theme} onToggleTheme={toggleTheme} />
      <main id="main-content" tabIndex={-1} data-active-page={active}>
        {children}
      </main>
      <SiteFooter />
    </div>
  );
}

export function AuthFrame({ children }: { children: React.ReactNode }) {
  return <PublicFrame active="auth">{children}</PublicFrame>;
}
