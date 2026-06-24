// Package engine is the channel-agnostic runtime that DRIVES the
// channel.Channel adapters defined in the parent channel package. Stage-1
// (MUL-3515) shipped the abstraction (Channel / InboundMessage /
// OutboundMessage / Registry) but no engine consumed it; this package is
// that engine — the "通用引擎" of MUL-3620, generalized out of the
// Feishu-specific lark.Hub / lark.Dispatcher.
//
// It currently provides:
//
//  1. Supervisor — the per-installation connection supervisor generalized
//     from lark.Hub. It enumerates active installations across ALL
//     channel types (no hard-coded platform), fences each behind the WS
//     lease CAS so at most one replica connects per installation, builds
//     the platform Channel via the channel.Registry, drives its
//     Connect/Disconnect lifecycle with exponential backoff + jitter, and
//     restarts a connection whose credentials rotated. It knows nothing
//     about any specific platform — adding a platform is "register a
//     factory", never "edit the engine".
//
// The engine depends only on the channel package and small interfaces
// (InstallationStore); it has no database, network, or platform imports.
// The DB-backed InstallationStore and the concrete platform adapters are
// wired by the application at boot.
package engine
