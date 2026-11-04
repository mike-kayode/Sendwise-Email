"use client";

import { forwardRef, type InputHTMLAttributes, type SelectHTMLAttributes, type TextareaHTMLAttributes } from "react";
import { cn } from "@/lib/utils";

const fieldStyles =
  "block w-full rounded-lg border-0 bg-white px-3 py-2 text-sm text-slate-900 shadow-sm ring-1 ring-inset ring-slate-200 placeholder:text-slate-400 focus:ring-2 focus:ring-inset focus:ring-indigo-500 disabled:bg-slate-50 disabled:text-slate-500";

export const Input = forwardRef<HTMLInputElement, InputHTMLAttributes<HTMLInputElement>>(
  ({ className, ...props }, ref) => {
    return <input ref={ref} className={cn(fieldStyles, "h-9", className)} {...props} />;
  },
);

Input.displayName = "Input";

export const Textarea = forwardRef<HTMLTextAreaElement, TextareaHTMLAttributes<HTMLTextAreaElement>>(
  ({ className, ...props }, ref) => {
    return <textarea ref={ref} className={cn(fieldStyles, className)} {...props} />;
  },
);

Textarea.displayName = "Textarea";

export const Select = forwardRef<HTMLSelectElement, SelectHTMLAttributes<HTMLSelectElement>>(
  ({ className, ...props }, ref) => {
    return <select ref={ref} className={cn(fieldStyles, "h-9", className)} {...props} />;
  },
);

Select.displayName = "Select";
