"use client";

import Link from "next/link";
import { useEffect } from "react";

export default function DashboardError({
  error,
  reset,
}: {
  error: Error & { digest?: string };
  reset: () => void;
}) {
  useEffect(() => {
    console.error("Dashboard page error", error);
  }, [error]);

  return (
    <main className="mx-auto flex min-h-[50vh] max-w-2xl flex-col justify-center px-6 py-12">
      <h1 className="text-xl font-semibold text-slate-900">This page hit an error</h1>
      <p className="mt-2 text-sm text-slate-600">{error.message || "An unexpected error occurred."}</p>
      {error.digest ? <p className="mt-2 text-xs text-slate-500">Error reference: {error.digest}</p> : null}
      <div className="mt-6 flex items-center gap-4">
        <button
          type="button"
          onClick={reset}
          className="rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white hover:bg-indigo-500"
        >
          Retry
        </button>
        <Link href="/dashboard" className="text-sm font-medium text-indigo-600 hover:text-indigo-500">
          Go to dashboard
        </Link>
      </div>
    </main>
  );
}
