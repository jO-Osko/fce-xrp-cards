import type { Metadata } from "next";
import { Shell } from "@/components/shell";
import "./globals.css";

export const metadata: Metadata = {
  title: "XRPL Credit Card · Swell Demo",
  description:
    "Non-custodial XRP collateral → virtual RLUSD card spend → daily XRPL settlement (Flare TEE)",
};

export default function RootLayout({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  return (
    // suppressHydrationWarning: the script below may set data-theme before React hydrates.
    <html lang="en" suppressHydrationWarning>
      <head>
        {/* Apply the saved theme before first paint, so there is no flash of the other one. */}
        <script
          dangerouslySetInnerHTML={{
            __html: `try{var t=localStorage.getItem("xrpl-cc-theme");if(t==="light"||t==="dark")document.documentElement.dataset.theme=t}catch(e){}`,
          }}
        />
      </head>
      <body>
        <Shell>{children}</Shell>
      </body>
    </html>
  );
}
