import "../global.css";
// Fonts ship as fontsource npm packages — the same packages apps/web uses —
// instead of next/font/google: next/font/google downloads faces from
// fonts.googleapis.com at build time, which stalls builds on hosts that cannot
// reach Google (the build log fills with `Retrying 1/3...`). npm packages ride
// the configured registry mirror and keep the build network-independent.
// Family names are the static fontsource names ("Inter Variable", …), composed
// into the font stacks in static CSS in ./global.css (CSP-safe, no inline
// <style>). Mirrors apps/web/app/layout.tsx.
import "@fontsource-variable/geist-mono";
import "@fontsource-variable/inter";
import "@fontsource-variable/source-serif-4";
import { RootProvider } from "fumadocs-ui/provider";
import { DocsLayout } from "fumadocs-ui/layouts/docs";
import type { ReactNode } from "react";
import type { Metadata } from "next";
import { baseOptions } from "@/app/layout.config";
import { source } from "@/lib/source";
import { i18n, type Lang } from "@/lib/i18n";
import { uiTranslations, localeLabels } from "@/lib/translations";
import { DocsSettings } from "@/components/docs-settings";

// Serif italic is deliberately NOT loaded — italic in CJK is a synthetic
// slant that breaks glyph design. Emphasis in docs is carried by brand color
// + weight, never font-style.

export const metadata: Metadata = {
  title: {
    template: "%s | Multica Docs",
    default: "Multica Docs",
  },
  description:
    "Documentation for Multica — the open-source managed agents platform.",
};

export function generateStaticParams() {
  return i18n.languages.map((lang) => ({ lang }));
}

export default async function Layout({
  params,
  children,
}: {
  params: Promise<{ lang: string }>;
  children: ReactNode;
}) {
  const { lang: rawLang } = await params;
  const lang = (i18n.languages as readonly string[]).includes(rawLang)
    ? (rawLang as Lang)
    : (i18n.defaultLanguage as Lang);
  const locales = i18n.languages.map((l) => ({
    locale: l,
    name: localeLabels[l],
  }));

  return (
    <html
      lang={lang}
      suppressHydrationWarning
      className="antialiased"
    >
      <body className="font-sans">
        <RootProvider
          i18n={{
            locale: lang,
            locales,
            translations: uiTranslations[lang],
          }}
          search={{ options: { api: "/docs/api/search" } }}
        >
          <DocsLayout
            tree={source.getPageTree(lang)}
            // Suppress Fumadocs's default sidebar-footer icons (theme +
            // language + search). Our custom <DocsSettings> is mounted as
            // the sidebar footer instead — two labelled buttons, not three
            // icons.
            themeSwitch={{ enabled: false }}
            searchToggle={{ enabled: false }}
            sidebar={{ footer: <DocsSettings locale={lang} /> }}
            {...baseOptions}
          >
            {children}
          </DocsLayout>
        </RootProvider>
      </body>
    </html>
  );
}
