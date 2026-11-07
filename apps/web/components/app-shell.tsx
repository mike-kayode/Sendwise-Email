"use client";

import Image from "next/image";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { useState, type ReactNode } from "react";
import {
  BarChart3,
  Activity,
  Filter,
  Globe,
  LayoutDashboard,
  LayoutTemplate,
  List,
  LogOut,
  Mail,
  Menu,
  Plus,
  Search,
  Send,
  Settings,
  ShieldAlert,
  Tags,
  Users,
  Workflow,
  X,
} from "lucide-react";
import { cn, initials } from "@/lib/utils";
import { useAuthStore } from "@/stores/auth";
import { useAuth } from "@/hooks/use-auth";

type NavItem = {
  href: string;
  label: string;
  icon: React.ComponentType<{ className?: string }>;
};

const navigation: { label: string; items: NavItem[] }[] = [
  {
    label: "Workspace",
    items: [
      { href: "/dashboard", label: "Dashboard", icon: LayoutDashboard },
      { href: "/audience/contacts", label: "Audience", icon: Users },
      { href: "/campaigns", label: "Campaigns", icon: Send },
      { href: "/automations", label: "Automations", icon: Workflow },
      { href: "/templates", label: "Templates", icon: LayoutTemplate },
      { href: "/forms", label: "Forms", icon: Mail },
    ],
  },
  {
    label: "Audience",
    items: [
      { href: "/audience/lists", label: "Lists", icon: List },
      { href: "/audience/tags", label: "Tags", icon: Tags },
      { href: "/audience/segments", label: "Segments", icon: Filter },
    ],
  },
  {
    label: "Insights",
    items: [
      { href: "/reports", label: "Reports", icon: BarChart3 },
      { href: "/domains", label: "Sending domains", icon: Globe },
    ],
  },
  {
    label: "Deliverability",
    items: [
      { href: "/delivery/dead-letter", label: "Dead letter queue", icon: ShieldAlert },
      { href: "/delivery/suppressions", label: "Suppressions", icon: Mail },
    ],
  },
  {
    label: "Account",
    items: [
      { href: "/settings", label: "Settings", icon: Settings },
      { href: "/billing", label: "Usage", icon: Activity },
    ],
  },
];

function isActive(pathname: string, href: string) {
  if (href === "/campaigns") {
    return pathname === href || pathname.startsWith("/campaigns/");
  }
  if (href === "/automations") {
    return pathname === href || pathname.startsWith("/automations/");
  }
  return pathname === href || pathname.startsWith(`${href}/`);
}

function SidebarContent({
  pathname,
  onNavigate,
  workspaceName,
  workspacePlan,
  userName,
  userEmail,
  onLogout,
  isLoggingOut,
  logoutError,
}: {
  pathname: string;
  onNavigate?: () => void;
  workspaceName: string;
  workspacePlan: string;
  userName: string;
  userEmail: string;
  onLogout: () => void;
  isLoggingOut: boolean;
  logoutError: string | null;
}) {
  return (
    <div className="flex h-full flex-col">
      <div className="flex h-[76px] items-center gap-2.5 border-b border-slate-100 px-5">
        <Image src="/logo.svg" width={30} height={30} alt="" />
        <div>
          <p className="text-[17px] font-semibold tracking-tight text-slate-900">Sendwise</p>
          <p className="text-[9px] font-medium uppercase tracking-[0.14em] text-slate-500">Marketing workspace</p>
        </div>
      </div>

      <div className="px-3 pt-4">
        <button className="flex w-full items-center gap-2 rounded-lg border border-slate-200 bg-slate-50 px-3 py-2 text-left hover:bg-white">
          <span className="flex h-7 w-7 items-center justify-center rounded-md bg-emerald-800 text-[11px] font-semibold text-white">
            {initials(workspaceName).slice(0, 2).toUpperCase()}
          </span>
          <span className="min-w-0 flex-1">
            <span className="block truncate text-sm font-medium text-slate-900">{workspaceName}</span>
            <span className="block text-[11px] text-slate-500">{workspacePlan} plan</span>
          </span>
          <span className="text-xs text-slate-400">⌄</span>
        </button>
      </div>

      <nav className="flex-1 space-y-6 overflow-y-auto px-3 py-5">
        {navigation.map((group) => (
          <div key={group.label}>
            <p className="px-3 pb-2 text-[10px] font-semibold uppercase tracking-widest text-slate-400">
              {group.label}
            </p>
            <div className="space-y-0.5">
              {group.items.map((item) => {
                const Icon = item.icon;
                const active = isActive(pathname, item.href);
                return (
                  <Link
                    key={item.href}
                    href={item.href}
                    onClick={onNavigate}
                    className={cn(
                      "flex items-center gap-2.5 rounded-lg px-3 py-2 text-sm transition",
                      active
                        ? "bg-emerald-50 font-medium text-emerald-900"
                        : "text-slate-600 hover:bg-slate-50 hover:text-slate-900",
                    )}
                  >
                    <Icon className={cn("h-4 w-4", active ? "text-emerald-800" : "text-slate-400")} />
                    {item.label}
                  </Link>
                );
              })}
            </div>
          </div>
        ))}
      </nav>

      <div className="border-t border-slate-100 p-3">
        <div className="flex items-center gap-2.5 rounded-lg px-2 py-2">
          <span className="flex h-8 w-8 items-center justify-center rounded-full bg-slate-900 text-xs font-semibold text-white">
            {initials(userName)}
          </span>
          <div className="min-w-0 flex-1">
            <p className="truncate text-sm font-medium text-slate-900">{userName}</p>
            <p className="truncate text-[11px] text-slate-500">{userEmail}</p>
          </div>
        </div>
        <button
          type="button"
          onClick={onLogout}
          disabled={isLoggingOut}
          className="mt-1 flex w-full items-center gap-2 rounded-lg px-2 py-2 text-sm text-slate-600 hover:bg-slate-50 hover:text-slate-900 disabled:cursor-wait disabled:opacity-60"
        >
          <LogOut className="h-4 w-4" />
          {isLoggingOut ? "Signing out..." : "Sign out"}
        </button>
        {logoutError ? (
          <p role="alert" className="px-2 pt-1 text-xs text-rose-700">
            {logoutError}
          </p>
        ) : null}
      </div>
    </div>
  );
}

export function AppShell({ children }: { children: ReactNode }) {
  const pathname = usePathname();
  const [mobileOpen, setMobileOpen] = useState(false);
  const [isLoggingOut, setIsLoggingOut] = useState(false);
  const [logoutError, setLogoutError] = useState<string | null>(null);
  const user = useAuthStore((state) => state.user);
  const organization = useAuthStore((state) => state.organization);
  const { logout } = useAuth();
  const workspaceName = organization?.name ?? "Sendwise";
  const workspacePlan = organization?.role ? organization.role.toUpperCase() : "WORKSPACE";
  const userName = user?.name ?? "Workspace owner";
  const userEmail = user?.email ?? "owner@example.com";
  const handleLogout = async () => {
    if (isLoggingOut) return;
    setIsLoggingOut(true);
    setLogoutError(null);
    try {
      await logout();
    } catch (error) {
      setLogoutError(error instanceof Error ? error.message : "Could not sign out. Please try again.");
      setIsLoggingOut(false);
    }
  };

  return (
    <div className="min-h-screen bg-[#f6f8f7]">
      <aside className="fixed inset-y-0 left-0 z-30 hidden w-[250px] border-r border-slate-200 bg-white lg:block">
        <SidebarContent
          pathname={pathname}
          workspaceName={workspaceName}
          workspacePlan={workspacePlan}
          userName={userName}
          userEmail={userEmail}
          onLogout={handleLogout}
          isLoggingOut={isLoggingOut}
          logoutError={logoutError}
        />
      </aside>

      {mobileOpen ? (
        <div className="fixed inset-0 z-50 lg:hidden">
          <button
            aria-label="Close navigation"
            className="absolute inset-0 bg-slate-900/40"
            onClick={() => setMobileOpen(false)}
          />
          <aside className="relative h-full w-[250px] max-w-[85%] border-r border-slate-200 bg-white shadow-xl">
            <button
              aria-label="Close navigation"
              className="absolute right-3 top-4 rounded-lg p-1.5 text-slate-500 hover:bg-slate-100"
              onClick={() => setMobileOpen(false)}
            >
              <X className="h-5 w-5" />
            </button>
            <SidebarContent
              pathname={pathname}
              onNavigate={() => setMobileOpen(false)}
              workspaceName={workspaceName}
              workspacePlan={workspacePlan}
              userName={userName}
              userEmail={userEmail}
              onLogout={handleLogout}
              isLoggingOut={isLoggingOut}
              logoutError={logoutError}
            />
          </aside>
        </div>
      ) : null}

      <div className="lg:pl-[250px]">
        <header className="sticky top-0 z-20 flex h-[76px] items-center gap-3 border-b border-slate-200 bg-white/90 px-4 backdrop-blur sm:px-6 lg:px-8">
          <button
            aria-label="Open navigation"
            className="rounded-lg p-2 text-slate-500 hover:bg-slate-100 lg:hidden"
            onClick={() => setMobileOpen(true)}
          >
            <Menu className="h-5 w-5" />
          </button>

          <div className="relative hidden max-w-sm flex-1 sm:block">
            <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-slate-400" />
            <input
              placeholder="Search contacts, campaigns, templates..."
              className="h-9 w-full rounded-lg border-0 bg-slate-50 pl-9 pr-3 text-sm text-slate-900 ring-1 ring-inset ring-slate-200 placeholder:text-slate-400 focus:bg-white focus:ring-2 focus:ring-inset focus:ring-emerald-700"
            />
          </div>

          <div className="ml-auto flex items-center gap-2">
            {pathname !== "/dashboard" ? (
              <Link
                href="/campaigns/new"
                className="inline-flex h-9 items-center gap-1.5 rounded-lg bg-emerald-800 px-3.5 text-sm font-medium text-white shadow-sm hover:bg-emerald-900"
              >
                <Plus className="h-4 w-4" />
                <span className="hidden sm:inline">New campaign</span>
                <span className="sm:hidden">New</span>
              </Link>
            ) : null}
          </div>
        </header>

        <main className="mx-auto max-w-7xl px-4 py-6 sm:px-6 lg:px-8">{children}</main>
      </div>
    </div>
  );
}
