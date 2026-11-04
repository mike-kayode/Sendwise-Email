"use client";

import { toast as sonnerToast } from "sonner";

export function useToast() {
  const toast = sonnerToast;

  return {
    toast: (opts: {
      title?: string;
      description?: string;
      variant?: "default" | "destructive";
    }) => {
      if (opts.variant === "destructive") {
        return toast.error(opts.title, {
          description: opts.description,
          closeButton: true,
        });
      }
      return toast.success(opts.title, {
        description: opts.description,
        closeButton: true,
      });
    },
  };
}
