// Instrument Serif ships as a fontsource npm package (same self-hosting
// approach as the app fonts in app/layout.tsx) — no build-time fetch of
// fonts.googleapis.com, which is unreachable on some build hosts.
import "@fontsource/instrument-serif/400.css";
import { LocaleProvider } from "@/features/landing/i18n";
import { getRequestLocale } from "@/lib/request-locale";

// Instrument Serif is the landing display face and is Latin-only. The full
// `--font-serif` stack (Instrument Serif + the per-locale CJK serif tail) is
// composed in static CSS in app/custom.css — same reasoning as `--font-sans`
// in app/globals.css: the CJK tail must be overridable per `<html lang>`, and
// the family name is a static fontsource name, so no CSS-variable indirection
// is needed.

const jsonLd = {
  "@context": "https://schema.org",
  "@graph": [
    {
      "@type": "Organization",
      name: "Multica",
      url: "https://www.multica.ai",
      sameAs: ["https://github.com/multica-ai/multica"],
    },
    {
      "@type": "SoftwareApplication",
      name: "Multica",
      applicationCategory: "ProjectManagement",
      operatingSystem: "Web",
      description:
        "Open-source project management platform that turns coding agents into real teammates.",
      offers: {
        "@type": "Offer",
        price: "0",
        priceCurrency: "USD",
      },
    },
  ],
};

export default async function LandingLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  const initialLocale = await getRequestLocale();

  return (
    <>
      <script
        type="application/ld+json"
        dangerouslySetInnerHTML={{ __html: JSON.stringify(jsonLd) }}
      />
      <div className="landing-light h-full overflow-x-hidden overflow-y-auto bg-white">
        <LocaleProvider initialLocale={initialLocale}>{children}</LocaleProvider>
      </div>
    </>
  );
}
