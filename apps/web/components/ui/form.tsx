"use client";

import * as React from "react";

import * as LabelPrimitive from "@radix-ui/react-label";
import { Slot } from "@radix-ui/react-slot";
import { cn } from "@/lib/utils";

const Form = React.forwardRef<
  HTMLFormElement,
  React.ComponentPropsWithoutRef<"form">
>(({ className, ...props }, ref) => (
  <form ref={ref} className={cn(className)} {...props} />
));
Form.displayName = "Form";

const Field = React.forwardRef<
  HTMLDivElement,
  React.ComponentPropsWithoutRef<"div">
>(({ className, ...props }, ref) => (
  <div ref={ref} className={cn("space-y-2", className)} {...props} />
));
Field.displayName = "Field";

const FieldLabel = React.forwardRef<
  React.ElementRef<typeof LabelPrimitive.Root>,
  React.ComponentPropsWithoutRef<typeof LabelPrimitive.Root>
>(({ className, ...props }, ref) => (
  <LabelPrimitive.Root ref={ref} className={cn("text-sm font-medium leading-none peer-disabled:cursor-not-allowed peer-disabled:opacity-70", className)} {...props} />
));
FieldLabel.displayName = FieldLabel.displayName;

export { Form, Field, FieldLabel };
