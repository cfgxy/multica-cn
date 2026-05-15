# Multica Mobile (iOS)

Expo + React Native iOS client for Multica. Independent from web/desktop — shares only types from `@multica/core/`. See [`CLAUDE.md`](./CLAUDE.md) for the locked tech-stack baseline and import rules.

## Scripts

| Command | What it does | Backend |
|---|---|---|
| `pnpm dev:mobile` | Metro only (reuse existing dev install) | local (`.env.development.local`) |
| `pnpm dev:mobile:staging` | Metro only (reuse existing dev install) | staging (`.env.staging`) |
| `pnpm ios:mobile:device` | Full rebuild + install on USB iPhone, **Debug** | local |
| `pnpm ios:mobile:device:staging` | Full rebuild + install on USB iPhone, **Debug** | staging |
| `pnpm ios:mobile:device:staging:release` | Full rebuild + install, **Release** (standalone) | staging |

`dev:*` runs Metro only — assumes a Debug build of the matching variant is already installed on the device. `ios:device:*` does a full native rebuild + install onto a USB-connected iPhone.

Bundle identifier and display name switch on `APP_ENV` (see `app.config.ts`), so Dev / Staging / Production variants can coexist on the same device.

## Build your own version onto your iPhone

Two paths, depending on what you want to do:

### Day-to-day development (you have the Mac in front of you)

```bash
pnpm ios:mobile:device:staging
```

Produces a **Debug build** with `expo-dev-launcher` embedded. Every launch the app probes Metro on your Mac and pulls fresh JS — perfect for hot-reload, painful when the Mac is asleep or you're on a different WiFi.

### Standalone / "just use it" (you want to walk away from the Mac)

```bash
pnpm ios:mobile:device:staging:release
```

Produces a **Release build**. No `expo-dev-launcher`, no Metro probe, no "Downloading…" screen. Splash → app, exactly like an App Store install. The trade-off: you cannot hot-reload — every JS change requires re-running this command.

Both paths share the same prerequisites: Mac with Xcode, free Apple ID added under Xcode → Settings → Accounts, iPhone connected via USB with Developer Mode enabled. Follow Expo's [Set up your environment](https://docs.expo.dev/get-started/set-up-your-environment/) — pick **Development build → iOS Device** — if any of that is missing.

First build of either variant downloads CocoaPods + compiles React Native from source — expect 10-20 minutes. Subsequent builds reuse Xcode's DerivedData cache.

## 7-day signing limit

A free Apple ID signs builds for **7 days only**, Debug and Release both. After that the app refuses to launch. Plug back into the Mac and re-run the corresponding `ios:*` script to re-sign. The only workaround is an Apple Developer Program account ($99/yr), which extends to 1 year.

## Pointing at a different backend

Edit `EXPO_PUBLIC_API_URL` in `.env.staging` (or `.env.development.local`). Then:

- For an installed **Debug build**: restart Metro (`pnpm dev:mobile:staging`) so the next JS bundle it serves picks up the new value.
- For an installed **Release build**: re-run the `ios:*:release` command — the value is baked into the embedded bundle at build time.

For local backend testing, use your Mac's LAN IP (`ipconfig getifaddr en0`), not `localhost`.
