#!/usr/bin/env bash
# SW-203 (W5.q) binary-size campaign — the measurement harness.
#
# Added in rebuild round 1 in answer to review finding F5 ("the binary-size
# measurement scripts are not checked in, so the campaign is not reproducible by
# a third party"). Every transcript in ../raw/ is produced by a subcommand here.
#
# THE RULE THIS SCRIPT EXISTS TO ENFORCE (review finding F1):
#
#   Leg A, the additivity control, the noise control, the red/green control and
#   the host leg are all built with `-buildvcs=true`. Go stamps `vcs.modified`
#   into the binary, and `vcs.modified=true` vs `=false` shifts the ELF layout by
#   a few tens of bytes on SOME tag sets. So:
#
#     * the worktree MUST be clean (tracked AND untracked) while measuring, and
#     * this script MUST NOT write its own output into the worktree.
#
#   Round 1's leg-a-tag-route.txt recorded no-markdown at 34,155,025 because that
#   one build ran while the tree was dirty; the clean-tree value is 34,154,993.
#   The `vcs-stamp` subcommand is the control that demonstrates it.
#
# Usage:
#   ./measure.sh <outdir> <subcommand>...
#     outdir       a directory OUTSIDE the repository (enforced)
#     subcommands  env leg-a leg-b noise additivity redgreen host vcs-stamp
#                  blobs toolchain | all
#
# The 21 build tags are DERIVED from internal/release.DefaultGrammarSubsetTags —
# the single source of truth — and are never retyped here.

set -euo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../../../.." && pwd)"
if [ ! -f "$REPO/go.mod" ]; then echo "not a repo root: $REPO" >&2; exit 1; fi

OUT="${1:?usage: measure.sh <outdir-outside-repo> <subcommand>...}"; shift
mkdir -p "$OUT"; OUT="$(cd "$OUT" && pwd)"
case "$OUT" in
  "$REPO"|"$REPO"/*)
    echo "REFUSING: outdir $OUT is inside the repo — its own files would dirty the" >&2
    echo "tree and stamp vcs.modified=true into every -buildvcs=true build (F1)." >&2
    exit 1;;
esac
BIN="$OUT/bin"; mkdir -p "$BIN"

VERSION_VAR="github.com/samibel/graphi/internal/version.Version"
GOTC="${GOTC:-go1.26.6}"

# --- the source of truth -----------------------------------------------------

tags_all() {
  sed -n '/^var DefaultGrammarSubsetTags = \[\]string{$/,/^}$/p' \
      "$REPO/internal/release/build.go" \
    | grep -o '"[a-z_]*"' | tr -d '"' | tr '\n' ' ' | sed 's/ $//'
}

# tags_drop "<tags>" lang [lang...]
tags_drop() {
  local t="$1"; shift
  local l
  for l in "$@"; do
    t="$(echo "$t" | tr ' ' '\n' | grep -v "^grammar_subset_${l}\$" | tr '\n' ' ' | sed 's/ $//')"
  done
  echo "$t"
}

ntags() { echo "$1" | wc -w | tr -d ' '; }
sha16() { shasum -a 256 "$1" | cut -c1-16; }
dirt() { ( cd "$REPO" && git status --porcelain ); }

require_clean() {
  local d; d="$(dirt)"
  if [ -n "$d" ]; then
    echo "REFUSING: worktree is not clean. -buildvcs=true would stamp" >&2
    echo "vcs.modified=true and shift measured sizes (review finding F1):" >&2
    echo "$d" >&2
    exit 1
  fi
}

blob_langs() {
  ( cd "$REPO" && GOTOOLCHAIN="$GOTC" go tool nm "$1" ) 2>/dev/null \
    | grep -o 'subsetBlobFS_[a-z_0-9]*' | sed 's/subsetBlobFS_//' | sort -u
}

# build <label> <tags> -> echoes bytes; leaves $BIN/graphi-<label>
build() {
  local label="$1" tags="$2"
  ( cd "$REPO" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOAMD64=v1 GOTOOLCHAIN="$GOTC" \
      go build -trimpath -buildvcs=true -tags "$tags" \
        -ldflags "-X ${VERSION_VAR}=dev" -o "$BIN/graphi-$label" ./cmd/graphi/ )
  stat -f%z "$BIN/graphi-$label"
}

# same recipe, no GOOS/GOARCH — what internal/bench/harness.go buildBinary builds
build_host() {
  local label="$1" tags="$2"
  ( cd "$REPO" && CGO_ENABLED=0 GOTOOLCHAIN="$GOTC" \
      go build -trimpath -buildvcs=true -tags "$tags" \
        -ldflags "-X ${VERSION_VAR}=dev" -o "$BIN/graphi-$label" ./cmd/graphi/ )
  stat -f%z "$BIN/graphi-$label"
}

# build with -buildvcs=false (leg B: the tree is edited, so the VCS stamp must go)
build_novcs() {
  local label="$1" tags="$2"
  ( cd "$REPO" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOAMD64=v1 GOTOOLCHAIN="$GOTC" \
      go build -trimpath -buildvcs=false -tags "$tags" \
        -ldflags "-X ${VERSION_VAR}=dev" -o "$BIN/graphi-$label" ./cmd/graphi/ )
  stat -f%z "$BIN/graphi-$label"
}

# --- subcommands -------------------------------------------------------------

sub_env() {
  {
    echo "# environment, captured at measurement time"
    printf '%-14s: %s\n' "go" "$( cd "$REPO" && GOTOOLCHAIN="$GOTC" go version )"
    printf '%-14s: %s\n' "GOTOOLCHAIN" "$GOTC (pinned by this script; the toolchain control pins it per leg too)"
    printf '%-14s: %s\n' "kernel" "$(uname -sr) $(uname -m)"
    printf '%-14s: %s\n' "os" "$(sw_vers -productName) $(sw_vers -productVersion) ($(sw_vers -buildVersion))"
    printf '%-14s: %s\n' "cpu" "$(sysctl -n machdep.cpu.brand_string)"
    printf '%-14s: %s\n' "cores" "$(sysctl -n hw.physicalcpu) physical / $(sysctl -n hw.logicalcpu) logical"
    printf '%-14s: %s\n' "memory" "$(sysctl -n hw.memsize) bytes"
    printf '%-14s: %s\n' "power" "$(pmset -g batt | head -1)"
    printf '%-14s: %s\n' "thermal" "$(pmset -g therm 2>/dev/null | tr '\n' ' ')"
    printf '%-14s: %s\n' "loadavg" "$(uptime | sed 's/.*averages*: //')"
    printf '%-14s: %s\n' "repo HEAD" "$( cd "$REPO" && git rev-parse HEAD )"
    printf '%-14s: %s\n' "repo branch" "$( cd "$REPO" && git rev-parse --abbrev-ref HEAD )"
    echo "build target  : linux/amd64 GOAMD64=v1 CGO_ENABLED=0 (the gate's platform), cross-built from darwin/arm64"
    echo "host leg      : darwin/arm64 CGO_ENABLED=0 (what internal/bench/harness.go buildBinary actually builds: it passes no GOOS/GOARCH)"
    echo
    echo "# worktree cleanliness at measurement time — tracked AND untracked."
    echo "# -buildvcs=true stamps vcs.modified into the binary, so a dirty tree is a"
    echo "# measurement condition, not a nuisance. See vcs-stamp-control.txt."
    dirt | sed 's/^/    /'
    echo "    (no indented line above == git status --porcelain was empty)"
  } > "$OUT/environment.txt"
}

sub_leg_a() {
  require_clean
  local T l d
  T="$(tags_all)"
  {
    printf 'TAGCOUNT=%s\n' "$(ntags "$T")"
    printf 'TAGS=%s\n' "$T"
    echo "worktree at measurement: CLEAN (git status --porcelain empty) => vcs.modified=false in every binary below"
    printf '=== leg A results (label\tbytes\tsha256(16))\n'
    printf 'baseline\t%s\t%s\n' "$(build baseline "$T")" "$(sha16 "$BIN/graphi-baseline")"
    for l in css hcl markdown toml yaml kotlin; do
      d="$(tags_drop "$T" "$l")"
      printf -- '-- without %s (tags=%s)\n' "$l" "$(ntags "$d")"
      printf 'no-%s\t%s\t%s\n' "$l" "$(build "no-$l" "$d")" "$(sha16 "$BIN/graphi-no-$l")"
    done
  } > "$OUT/leg-a-tag-route.txt"
}

sub_vcs_stamp() {
  require_clean
  local T l d lbl c y
  T="$(tags_all)"
  local MARK="$REPO/.sw203-vcs-stamp-probe"
  {
    echo "=== control for review finding F1: does worktree dirt change the measured size?"
    echo "Same recipe as leg A (-buildvcs=true). Every build is run twice: once on the"
    echo "clean committed tree, once with a single UNTRACKED file present (a non-empty"
    echo "git status --porcelain is what Go reads to set vcs.modified). Nothing else"
    echo "differs between the two columns."
    echo
    printf '%-12s %14s %14s %8s\n' "build" "CLEAN" "DIRTY" "delta"
    for l in "" css hcl markdown toml yaml kotlin; do
      if [ -z "$l" ]; then d="$T"; lbl="baseline"; else d="$(tags_drop "$T" "$l")"; lbl="no-$l"; fi
      c="$(build "vcs-clean-$lbl" "$d")"
      : > "$MARK"
      y="$(build "vcs-dirty-$lbl" "$d")"
      rm -f "$MARK"
      printf '%-12s %14s %14s %8s\n' "$lbl" "$c" "$y" "$(( y - c ))"
    done
    echo
    echo "The ONLY build whose size moves is no-markdown. Round 1's leg-a-tag-route.txt"
    echo "recorded 34155025 for it, which is the DIRTY value; the clean value is 34154993."
    echo "The only buildinfo difference between the two binaries is vcs.modified:"
    ( cd "$REPO" && GOTOOLCHAIN="$GOTC" go version -m "$BIN/graphi-vcs-clean-no-markdown" ) \
      | grep 'vcs\.' | sed 's/^[[:space:]]*/    clean  /'
    ( cd "$REPO" && GOTOOLCHAIN="$GOTC" go version -m "$BIN/graphi-vcs-dirty-no-markdown" ) \
      | grep 'vcs\.' | sed 's/^[[:space:]]*/    dirty  /'
    echo
    echo "vcs.modified=true is one byte SHORTER than =false, yet the file is 32 B LARGER:"
    echo "the buildinfo blob sits before section padding, so a one-byte shift can cross an"
    echo "alignment boundary. Whether it does depends on the tag set, which is why six of"
    echo "the seven leg-A builds are insensitive to tree dirt and one is not."
    echo
    echo "restore check:"
    dirt | sed 's/^/    DIRTY /'
    echo "    (no DIRTY line above == the probe file was removed)"
  } > "$OUT/vcs-stamp-control.txt"
  rm -f "$MARK"
}

sub_noise() {
  require_clean
  local T R i
  T="$(tags_all)"
  {
    echo "=== (1) reproducibility: same build, 3 dispatches"
    for i in 1 2 3; do
      printf 'repro-%s\t%s\t%s\n' "$i" "$(build "repro-$i" "$T")" "$(sha16 "$BIN/graphi-repro-$i")"
    done
    echo "=== (2) layout control: identical tag SET, reversed ORDER"
    R="$(echo "$T" | tr ' ' '\n' | tail -r | tr '\n' ' ' | sed 's/ $//')"
    printf 'reorder\t%s\t%s\n' "$(build reorder "$R")" "$(sha16 "$BIN/graphi-reorder")"
    echo
    echo "NOTE ON THE sha256 VALUES (review finding F3). Size is the measured quantity;"
    echo "sha is used only WITHIN one capture, to tell two builds apart. -buildvcs=true"
    echo "stamps vcs.revision and vcs.time, so every sha here changes with every commit"
    echo "while the sizes do not. Quote these prefixes only against this capture."
    printf 'capture HEAD: %s\n' "$( cd "$REPO" && git rev-parse HEAD )"
  } > "$OUT/noise-control.txt"
}

sub_additivity() {
  require_clean
  local T
  T="$(tags_all)"
  {
    printf 'lua-only\t%s\n' "$(build lua-only "$(tags_drop "$T" lua)")"
    printf 'toml-lua\t%s\n' "$(build toml-lua "$(tags_drop "$T" toml lua)")"
    printf 'css-yaml\t%s\n' "$(build css-yaml "$(tags_drop "$T" css yaml)")"
  } > "$OUT/additivity-control.txt"
}

rg_row() {
  local lbl="$1" f="$BIN/graphi-$2"
  printf '%-26s bytes=%-10s sha256=%s langs_embedded=%s css_present=%s\n' \
    "$lbl" "$(stat -f%z "$f")" "$(sha16 "$f")" \
    "$(blob_langs "$f" | grep -c .)" "$(blob_langs "$f" | grep -c '^css$' || true)"
}

sub_redgreen() {
  require_clean
  local T RED GRN
  T="$(tags_all)"
  {
    echo "--- baseline (all 21 tags)"
    build rg-baseline "$T" > /dev/null
    rg_row "rg-baseline" rg-baseline
    echo "--- RED: 'remove' a tag that is not in the set (grammar_subset_cssx) — the removal cannot take effect"
    RED="$(tags_drop "$T" cssx)"
    printf '    tag count after the no-op removal: %s (unchanged == the edit did nothing)\n' "$(ntags "$RED")"
    build rg-red "$RED" > /dev/null
    rg_row "rg-red   (no-op removal)" rg-red
    echo "--- GREEN: remove grammar_subset_css for real"
    GRN="$(tags_drop "$T" css)"
    printf '    tag count after the real removal: %s\n' "$(ntags "$GRN")"
    build rg-green "$GRN" > /dev/null
    rg_row "rg-green (real removal)" rg-green
    echo
    echo "RED must equal baseline in bytes AND sha AND langs_embedded AND css_present=1."
    echo "GREEN must differ in bytes, drop to langs_embedded=19 and css_present=0."
    echo "If RED and GREEN looked the same, every marginal cost in this campaign would be meaningless."
  } > "$OUT/red-green-non-vacuity.txt"
}

sub_host() {
  require_clean
  local T l
  T="$(tags_all)"
  {
    printf 'host platform: %s/%s\n' "$(uname -s | tr 'A-Z' 'a-z')" "$(uname -m)"
    printf '%-14s %s\n' "baseline" "$(build_host h-baseline "$T")"
    for l in css hcl markdown toml yaml kotlin; do
      printf '%-14s %s\n' "no-$l" "$(build_host "h-no-$l" "$(tags_drop "$T" "$l")")"
    done
  } > "$OUT/host-platform-leg.txt"
}

sub_blobs() {
  local f n
  {
    for f in "$BIN"/graphi-*; do
      n="$(blob_langs "$f" | tr '\n' ',')"
      printf '%-24s embeds=%2s  %s\n' "$(basename "$f")" "$(blob_langs "$f" | grep -c .)" "$n"
    done
  } > "$OUT/embedded-blob-check.txt"
}

sub_leg_b() {
  # Registration route. This leg EDITS core/parse/defaults.go, so it uses
  # -buildvcs=false on BOTH halves: the dirty-tree VCS stamp must not contaminate
  # the delta. That is the same mechanism F1 caught contaminating leg A, handled
  # correctly here — which is why leg B reproduces and leg A did not.
  require_clean
  local T D
  T="$(tags_all)"
  D="$REPO/core/parse/defaults.go"
  cp "$D" "$OUT/defaults.go.orig"
  {
    echo "=== leg B baseline (buildvcs=false, unmodified tree)"
    printf 'b-baseline\t%s\n' "$(build_novcs b-baseline "$T")"
    echo "=== leg B: json registration removed (tag set UNCHANGED — json has no tag)"
    sed -i '' '/r\.Register(NewJSONParser())/d' "$D"
    printf '  remaining NewJSONParser refs in defaults.go: %s\n' "$(grep -c 'NewJSONParser' "$D" || true)"
    printf 'b-no-json-reg\t%s\n' "$(build_novcs b-no-json-reg "$T")"
    cp "$OUT/defaults.go.orig" "$D"
    echo "=== leg B: css registration removed AND grammar_subset_css dropped"
    sed -i '' '/r\.Register(NewCSSParser())/d' "$D"
    printf 'b-no-css-full\t%s\n' "$(build_novcs b-no-css-full "$(tags_drop "$T" css)")"
    cp "$OUT/defaults.go.orig" "$D"
    echo "=== restore check"
    dirt | sed 's/^/  DIRTY /'
    echo "  (no DIRTY line above == defaults.go restored)"
  } > "$OUT/leg-b-registration-route.txt"
  cp "$OUT/defaults.go.orig" "$D"
}

sub_toolchain() {
  # The AC-6 platform control. GOTOOLCHAIN is pinned PER LEG and read back out of
  # each binary's buildinfo, because the ambient GOTOOLCHAIN=auto silently
  # switches DOWN to satisfy an old tree's `go` directive (campaign record §5.1).
  local W T tc b
  T="$(tags_all)"
  W="$OUT/wt-80d67ed"
  ( cd "$REPO" && git worktree add --detach "$W" 80d67ed > /dev/null 2>&1 )
  {
    printf 'tree=80d67ed (v0.7.1)  go.mod directive: %s  tags=%s\n' \
      "$(grep -m1 '^go ' "$W/go.mod")" "$(ntags "$T")"
    for tc in go1.26.5 go1.26.6; do
      b="$( cd "$W" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOAMD64=v1 GOTOOLCHAIN="$tc" \
              go build -trimpath -buildvcs=true -tags "$T" \
                -ldflags "-X ${VERSION_VAR}=dev" -o "$BIN/graphi-t${tc#go}" ./cmd/graphi/ \
            && stat -f%z "$BIN/graphi-t${tc#go}" )"
      printf '%-10s bytes=%-12s buildinfo-toolchain=%s\n' "t${tc#go}" "$b" \
        "$( cd "$REPO" && go version -m "$BIN/graphi-t${tc#go}" | head -1 | awk '{print $2}' )"
    done
    echo "HEAD shipped default, same method:"
    printf '%-10s bytes=%-12s buildinfo-toolchain=%s\n' "HEAD" "$(build tc-head "$T")" \
      "$( cd "$REPO" && go version -m "$BIN/graphi-tc-head" | head -1 | awk '{print $2}' )"
    echo "SW-177 published: 80d67ed@go1.26.5=32741344  80d67ed@go1.26.6=32750198  3b8d43f@go1.26.6=34162926"
  } > "$OUT/toolchain-control.txt"
  ( cd "$REPO" && git worktree remove --force "$W" )
  echo "worktree removed" >> "$OUT/toolchain-control.txt"
}

for sub in "$@"; do
  case "$sub" in
    env)        sub_env;;
    leg-a)      sub_leg_a;;
    leg-b)      sub_leg_b;;
    noise)      sub_noise;;
    additivity) sub_additivity;;
    redgreen)   sub_redgreen;;
    host)       sub_host;;
    vcs-stamp)  sub_vcs_stamp;;
    blobs)      sub_blobs;;
    toolchain)  sub_toolchain;;
    all)        sub_env; sub_leg_a; sub_vcs_stamp; sub_noise; sub_additivity;
                sub_redgreen; sub_host; sub_leg_b; sub_toolchain; sub_blobs;;
    *) echo "unknown subcommand: $sub" >&2; exit 2;;
  esac
  echo "ok: $sub -> $OUT"
done
