"use client";

import { forwardRef, type ButtonHTMLAttributes } from "react";
import { cn } from "@/lib/utils";

type ButtonVariant = "primary" | "secondary" | "ghost" | "danger" | "outline";

type ButtonSize = "sm" | "md" | "lg";

export type ButtonProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: ButtonVariant;
  size?: ButtonSize;
};

const variants: Record<ButtonVariant, string> = {
  primary: "bg-indigo-600 text-white shadow-sm hover:bg-indigo-500 focus-visible:outline-indigo-600",
  secondary: "bg-white text-slate-900 shadow-sm ring-1 ring-inset ring-slate-200 hover:bg-slate-50",
  outline: "bg-transparent text-slate-900 ring-1 ring-inset ring-slate-200 hover:bg-slate-50",
  ghost: "bg-transparent text-slate-700 hover:bg-slate-100 hover:text-slate-900",
  danger: "bg-rose-600 text-white shadow-sm hover:bg-rose-500 focus-visible:outline-rose-600",
};

const sizes: Record<ButtonSize, string> = {
  sm: "h-8 px-3 text-sm",
  md: "h-9 px-4 text-sm",
  lg: "h-10 px-5 text-sm",
};

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(
  ({ className, variant = "primary", size = "md", type = "button", ...props }, ref) => {
    return (
      <button
        ref={ref}
        type={type}
        className={cn(
          "inline-flex items-center justify-center gap-2 rounded-lg font-medium transition focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 disabled:pointer-events-none disabled:opacity-50",
          variants[variant],
          sizes[size],
          className,
        )}
        {...props}
      />
    );
  },
);

Button.displayName = "Button";
