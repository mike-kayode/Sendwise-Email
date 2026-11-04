"use client";

import { Toaster as SonnerToaster, type ToasterProps } from "sonner";

export function Toaster(props: ToasterProps) {
  return <SonnerToaster {...props} richColors closeButton />;
}

export type { ToasterProps as SonnerToasterProps } from "sonner";
