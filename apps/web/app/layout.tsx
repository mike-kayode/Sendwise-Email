import type { Metadata } from "next";
import { JetBrains_Mono, Manrope } from "next/font/google";
import "./globals.css";
import "@/components/marketing/marketing.css";
import { Providers } from "@/components/providers";

const manrope = Manrope({
  subsets: ["latin"],
  display: "swap",
  variable: "--font-manrope",
  weight: ["400", "500", "600", "700", "800"],
});

const jetBrains = JetBrains_Mono({
  subsets: ["latin"],
  display: "swap",
  variable: "--font-jetbrains",
  weight: ["400", "500"],
});

export const metadata: Metadata = {
  metadataBase: new URL(process.env.NEXT_PUBLIC_SITE_URL || "https://sendwise.ng"),
  title: {
    default: "Sendwise",
    template: "%s · Sendwise",
  },
  description: "Self-hosted email marketing for teams that care about deliverability.",
  openGraph: {
    type: "website",
    siteName: "Sendwise",
    title: "Sendwise",
    description: "Self-hosted email marketing for teams that care about deliverability.",
  },
  twitter: {
    card: "summary",
    title: "Sendwise",
    description: "Self-hosted email marketing for teams that care about deliverability.",
  },
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body className={`${manrope.variable} ${jetBrains.variable}`}>
        <Providers>{children}</Providers>
      </body>
    </html>
  );
}
