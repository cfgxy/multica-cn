// Asset imports — modern bundlers (Next.js / electron-vite / vite) resolve
// these to a URL string at build time. This file lets TypeScript accept
// `import logo from "./logo.png"` inside the shared package.
declare module "*.png" {
  const src: string;
  export default src;
}
declare module "*.svg" {
  const src: string;
  export default src;
}
