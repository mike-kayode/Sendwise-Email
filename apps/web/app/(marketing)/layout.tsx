import type { ReactNode } from "react";
import { PublicFrame } from "@/components/marketing/site";

export default function MarketingLayout({ children }: { children: ReactNode }) {
  return <PublicFrame>{children}</PublicFrame>;
}
