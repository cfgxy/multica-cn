/**
 * Alpha blending for theme color strings (RUYI-741).
 *
 * THEME tokens are `hsl(H S% L%)` strings. Concatenating a hex-alpha
 * suffix onto them (`color + "1A"`) yields an invalid color, which
 * Android renders as opaque black. withAlphaColor parses the token
 * formats the theme actually uses and returns a valid rgba() string;
 * on unparseable input it returns the input unchanged (opaque) rather
 * than risk an invalid color downstream.
 */

/** `hsl(H S% L%)` / `hsl(H S% L% / a)` — comma forms accepted too. */
const HSL_RE =
  /^hsla?\(\s*(-?[\d.]+)(?:deg)?\s*[, ]\s*([\d.]+)%\s*[, ]\s*([\d.]+)%(?:\s*[,/]\s*([\d.]+))?\s*\)$/;

/** `#rgb` `#rgba` `#rrggbb` `#rrggbbaa` — an embedded alpha is overridden. */
const HEX_RE = /^#([0-9a-f]{3,8})$/i;

function hslToRgb(h: number, s: number, l: number): [number, number, number] {
  const hue = ((h % 360) + 360) % 360;
  const c = (1 - Math.abs(2 * l - 1)) * s;
  const x = c * (1 - Math.abs(((hue / 60) % 2) - 1));
  const [r1, g1, b1] =
    hue < 60
      ? [c, x, 0]
      : hue < 120
        ? [x, c, 0]
        : hue < 180
          ? [0, c, x]
          : hue < 240
            ? [0, x, c]
            : hue < 300
              ? [x, 0, c]
              : [c, 0, x];
  const m = l - c / 2;
  return [
    Math.round((r1 + m) * 255),
    Math.round((g1 + m) * 255),
    Math.round((b1 + m) * 255),
  ];
}

function hexToRgb(hex: string): [number, number, number] | null {
  const expand = (short: string) => parseInt(short + short, 16);
  if (hex.length === 3 || hex.length === 4) {
    return [
      expand(hex.slice(0, 1)),
      expand(hex.slice(1, 2)),
      expand(hex.slice(2, 3)),
    ];
  }
  if (hex.length === 6 || hex.length === 8) {
    return [
      parseInt(hex.slice(0, 2), 16),
      parseInt(hex.slice(2, 4), 16),
      parseInt(hex.slice(4, 6), 16),
    ];
  }
  return null;
}

export function withAlphaColor(color: string, alpha: number): string {
  const a = Math.min(1, Math.max(0, alpha));
  const hsl = HSL_RE.exec(color.trim());
  if (hsl) {
    const [r, g, b] = hslToRgb(
      parseFloat(hsl[1]),
      parseFloat(hsl[2]) / 100,
      parseFloat(hsl[3]) / 100,
    );
    return `rgba(${r}, ${g}, ${b}, ${a})`;
  }
  const hex = HEX_RE.exec(color.trim());
  if (hex) {
    const rgb = hexToRgb(hex[1]);
    if (rgb) return `rgba(${rgb[0]}, ${rgb[1]}, ${rgb[2]}, ${a})`;
  }
  return color;
}
