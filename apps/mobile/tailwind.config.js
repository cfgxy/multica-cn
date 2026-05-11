/**
 * Mobile design tokens — transcribed by hand from packages/ui/styles/tokens.css
 * (web/desktop). Web tokens use oklch + Tailwind v4 @theme inline syntax which
 * NativeWind 4 + Tailwind 3.4 can't consume, so we re-author them here as hex
 * approximations. When web tokens drift, sync this file by hand — divergence
 * is intentional.
 *
 * Mobile-specific tweaks (touch-friendly spacing, no hover variants) live here
 * too. Do NOT import packages/ui/styles/tokens.css.
 */

/** @type {import('tailwindcss').Config} */
module.exports = {
  content: [
    "./app/**/*.{js,jsx,ts,tsx}",
    "./components/**/*.{js,jsx,ts,tsx}",
  ],
  presets: [require("nativewind/preset")],
  theme: {
    extend: {
      colors: {
        background: "#ffffff",
        foreground: "#1f1f23",
        card: "#ffffff",
        "card-foreground": "#1f1f23",
        popover: "#ffffff",
        "popover-foreground": "#1f1f23",
        primary: "#2e2e33",
        "primary-foreground": "#fafafa",
        secondary: "#f4f4f5",
        "secondary-foreground": "#2e2e33",
        muted: "#f4f4f5",
        "muted-foreground": "#71717a",
        // ~5% darker than `secondary` (#f4f4f5). Dedicated surface for
        // fenced code blocks so they have visual elevation inside a
        // comment card (which itself uses `secondary`). Don't fold this
        // into `muted` — muted is used for many other neutral fills
        // (disabled states, placeholder bg, /50 overlays) and bumping
        // it would shift those too. Mirrors GitHub Primer's separate
        // `bgColor-muted` token for code surfaces.
        "code-surface": "#e8e8eb",
        accent: "#f4f4f5",
        "accent-foreground": "#2e2e33",
        destructive: "#dc2626",
        border: "#e4e4e7",
        input: "#e4e4e7",
        ring: "#a1a1aa",
        brand: "#4571e0",
        "brand-foreground": "#fafafa",
        success: "#22c55e",
        warning: "#eab308",
        info: "#3b82f6",
        priority: "#f97316",
      },
      borderRadius: {
        sm: "calc(0.625rem * 0.6)",
        md: "calc(0.625rem * 0.8)",
        lg: "0.625rem",
        xl: "calc(0.625rem * 1.4)",
      },
    },
  },
  plugins: [],
};
