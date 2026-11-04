"use client";

import * as React from "react";
import * as SelectPrimitive from "@radix-ui/react-select";
import { Check, ChevronDown, ChevronUp } from "lucide-react";
import { cn } from "@/lib/utils";

type SelectTriggerRef = React.ElementRef<typeof SelectPrimitive.Trigger>;
type SelectTriggerProps = React.ComponentPropsWithoutRef<typeof SelectPrimitive.Trigger>;
type SelectItemRef = React.ElementRef<typeof SelectPrimitive.Item>;
type SelectItemProps = React.ComponentPropsWithoutRef<typeof SelectPrimitive.Item>;
type SelectSeparatorRef = React.ElementRef<typeof SelectPrimitive.Separator>;
type SelectSeparatorProps = React.ComponentPropsWithoutRef<typeof SelectPrimitive.Separator>;

const Select = SelectPrimitive.Root;
const SelectGroup = SelectPrimitive.Group;
const SelectValue = SelectPrimitive.Value;

const SelectTrigger = React.forwardRef<SelectTriggerRef, SelectTriggerProps>(
  ({ className, children, ...props }, ref) => (
    <SelectPrimitive.Trigger
      ref={ref}
      className={cn(
        "flex h-10 w-full items-center justify-between rounded-md border border-input bg-background px-3 py-2 text-sm shadow-sm ring-offset-white file:border-0 file:px-0 file:bg-transparent file:text-sm file:font-medium placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50",
        className
      )}
      {...props}
    >
      {children}
      <SelectPrimitive.Icon className="ml-2 h-4 w-4 opacity-50">
        <ChevronDown className="h-4 w-4" />
      </SelectPrimitive.Icon>
    </SelectPrimitive.Trigger>
  ),
);
SelectTrigger.displayName = "SelectTrigger";

const SelectContent = React.forwardRef<
  React.ElementRef<typeof SelectPrimitive.Content>,
  React.ComponentPropsWithoutRef<typeof SelectPrimitive.Content>
>(
  ({ className, children, position = "popper", ...props }, ref) => (
    <SelectPrimitive.Portal>
      <SelectPrimitive.Content
        ref={ref}
        position={position}
        className={cn(
          "relative z-50 max-h-96 min-w-[8rem] overflow-hidden rounded-md border bg-popover text-popover-foreground shadow-md state:(data-closed):outline-none focus-visible:outline-none focus-visible:ring-0",
          className
        )}
        {...props}
      >
        <SelectPrimitive.Viewport
          className={cn(
            "p-1",
            position === "popper" &&
              "w-full min-w-[8rem] data-[side=bottom]:translate-y-1 data-[side=left]:-translate-x-1 data-[side=right]:translate-x-1 data-[side=top]:-translate-y-1"
          )}
        >
          {children}
        </SelectPrimitive.Viewport>
      </SelectPrimitive.Content>
    </SelectPrimitive.Portal>
  ),
);
SelectContent.displayName = "SelectContent";

const SelectItem = React.forwardRef<SelectItemRef, SelectItemProps>(
  ({ className, children, ...props }, ref) => (
    <SelectPrimitive.Item
      ref={ref}
      className={cn(
        "relative flex w-full cursor-pointer select-none items-center rounded-sm px-2 py-1.5 text-sm outline-none transition-colors focus:bg-accent focus:text-accent-foreground disabled:pointer-events-none disabled:opacity-50",
        className
      )}
      {...props}
    >
      <span className="absolute left-2 flex h-3.5 w-3.5 items-center justify-center">
        <SelectPrimitive.ItemIndicator>
          <Check className="h-4 w-4" />
        </SelectPrimitive.ItemIndicator>
      </span>
      <SelectPrimitive.ItemText>{children}</SelectPrimitive.ItemText>
    </SelectPrimitive.Item>
  ),
);
SelectItem.displayName = "SelectItem";

const SelectSeparator = React.forwardRef<SelectSeparatorRef, SelectSeparatorProps>(
  ({ className, ...props }, ref) => (
    <SelectPrimitive.Separator
      ref={ref}
      className={cn("-mx-1 h-px bg-muted", className)}
      {...props}
    />
  ),
);
SelectSeparator.displayName = "SelectSeparator";

export {
  Select,
  SelectGroup,
  SelectValue,
  SelectTrigger,
  SelectContent,
  SelectItem,
  SelectSeparator,
};
