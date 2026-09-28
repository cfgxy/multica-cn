#!/usr/bin/env bash
set -euo pipefail

# Turn an env file into a fragment GNU make can `include`, and print its path.
#
# `include` parses its argument as makefile syntax, which has no notion of a
# value spanning lines. One multi-line value in the env file — a PEM key, a
# certificate — therefore aborts EVERY target with "missing separator" at the
# value's second line, before the first recipe runs (RUYI-218).
#
# Dropping such keys instead of translating them is not an option: nothing else
# loads the env file for `make server` / `make start` / `make setup`, whose
# `go run` children receive only what the Makefile's top-level `export` places
# in the environment. A make `define` block carries the newlines, and `export`
# hands them to a subprocess intact, so the value survives whole.
#
# Single-line input passes through byte-for-byte, which is what keeps make's
# handling of comments, blank lines, empty values, `#` inside a value and `$`
# expansion identical to including the env file directly.
#
# Recognized multi-line form is the one shells write: KEY="…  with the closing
# quote on a later line. An escaped quote inside the value is not parsed, and
# neither is a value whose quote never closes — both leave the file untouched
# so the caller keeps make's original diagnostics rather than a silently
# different value.

src=${1:?usage: env-make-include.sh <env-file>}

# Fall back to the env file itself. The caller includes whatever this prints,
# so a failure here must degrade to make's own parse error rather than to an
# empty `include` (which make rejects with a different, more confusing error).
fallback() {
  [ -n "${1:-}" ] && echo "env-make-include.sh: $1" >&2
  printf '%s\n' "$src"
  exit 0
}

[ -f "$src" ] || fallback ""

abs=$(cd "$(dirname "$src")" && pwd)/$(basename "$src")

# Under .env-make/ so the repository's existing `.env*` ignore rule covers the
# generated fragments: they hold the same secrets as their source.
out_dir=.env-make
# The path hash keeps two env files of the same basename — a worktree's and the
# one a recursive `make ENV_FILE=…` names by absolute path — in separate
# fragments.
hash=$(printf '%s' "$abs" | cksum | cut -d' ' -f1)
out=$out_dir/$(basename "$src").$hash.mk

# make calls this once per invocation, including every recursive `$(MAKE)`, so
# an up-to-date fragment is reused rather than rebuilt. The reuse test is the
# source's checksum, not its timestamp: the documented way to set up a worktree
# or QA environment is to copy the main checkout's .env in, and a copy that
# preserves the original mtime would otherwise be served a stale fragment.
stamp="# env-make-include.sh source: $(cksum <"$src" | cut -d' ' -f1,2)"
if [ -f "$out" ] && [ "$(head -n 1 "$out")" = "$stamp" ]; then
  printf '%s\n' "$out"
  exit 0
fi

mkdir -p "$out_dir" || fallback "cannot create $out_dir"

tmp=$(mktemp "$out_dir/.tmp.XXXXXX") || fallback "cannot create a temp file in $out_dir"
trap 'rm -f "$tmp"' EXIT

printf '%s\n' "$stamp" >"$tmp"

awk '
function flush_pending() {
  printf "define %s\n%s\nendef\n", pending_key, buffer
}
{
  line = $0
  sub(/\r$/, "", line)

  if (pending_key != "") {
    # The closing quote ends the value; anything after it on that line is not
    # part of it, matching how a shell reads the same file.
    if (index(line, quote) > 0) {
      tail = substr(line, 1, index(line, quote) - 1)
      buffer = buffer "\n" tail
      flush_pending()
      pending_key = ""
    } else {
      buffer = buffer "\n" line
    }
    next
  }

  if (match(line, /^[ \t]*(export[ \t]+)?[A-Za-z_][A-Za-z0-9_]*[ \t]*=/)) {
    eq = index(line, "=")
    key = substr(line, 1, eq - 1)
    sub(/^[ \t]*(export[ \t]+)?/, "", key)
    sub(/[ \t]+$/, "", key)
    rhs = substr(line, eq + 1)

    q = substr(rhs, 1, 1)
    if (q == "\"" || q == "'\''") {
      rest = substr(rhs, 2)
      if (index(rest, q) == 0) {
        pending_key = key
        quote = q
        buffer = rest
        next
      }
    }
  }

  print $0
}
END {
  # An unterminated value means the file is not what this translation assumes.
  if (pending_key != "") exit 3
}
' "$src" >>"$tmp" || fallback "unterminated quoted value in $src"

mv "$tmp" "$out" || fallback "cannot install $out"
trap - EXIT

printf '%s\n' "$out"
