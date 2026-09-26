#!/usr/bin/env bash
# Count non-blank, non-comment lines per Go file and fail on any file over the limit.
#
#   scripts/loc.sh [-m MAXLOC] [-a] [PATH...]     default: MAXLOC=1500, PATH=cmd internal pkg
#     -m MAXLOC   fail threshold (lines of code per file)
#     -a          print every file, largest first, instead of only offenders
#     --self-test
#
# Exit: 0 all files within limit, 1 an offender, 2 usage.
set -euo pipefail
cd "$(dirname "$0")/.."

MAXLOC=1500 ALL=
while getopts ':m:a-:' opt; do
  case $opt in
    m) MAXLOC=$OPTARG ;;
    a) ALL=1 ;;
    -) [ "$OPTARG" = self-test ] && SELF_TEST=1 || { echo "unknown option --$OPTARG" >&2; exit 2; } ;;
    *) sed -n '2,9p' "$0" >&2; exit 2 ;;
  esac
done
shift $((OPTIND - 1))
[[ $MAXLOC =~ ^[0-9]+$ ]] || { echo "-m needs an integer" >&2; exit 2; }

# regex tokenizer, not a Go parser; `//` or `/*` inside a string literal
# counts as a comment. Swap for `go/scanner` if that ever produces a wrong verdict.
count() {
  awk '
    FNR == 1 { n[FILENAME] = 0 }
    {
      line = $0
      if (inblock) {
        if (!match(line, /\*\//)) next
        inblock = 0; line = substr(line, RSTART + 2)
      }
      while (match(line, /\/\*.*\*\//)) line = substr(line, 1, RSTART - 1) substr(line, RSTART + RLENGTH)
      if (match(line, /\/\*/)) { inblock = 1; line = substr(line, 1, RSTART - 1) }
      sub(/\/\/.*/, "", line)
      if (line ~ /^[[:space:]]*$/) next
      n[FILENAME]++
    }
    END { for (f in n) printf "%d\t%s\n", n[f], f }
  ' "$@" | sort -rn
}

if [ "${SELF_TEST:-}" ]; then
  tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
  printf '%s\n' 'package x' '' '// c' '/* a' 'b */ var y = 1' 'var z = 2 /* q */ // r' '/* s */' '' > "$tmp/t.go"
  got=$(count "$tmp/t.go" | cut -f1)
  [ "$got" = 3 ] && echo "self-test ok" || { echo "self-test: got $got want 3" >&2; exit 1; }
  exit 0
fi

[ $# -gt 0 ] || set -- cmd internal pkg
files=$(find "$@" -name '*.go' -type f)
[ -n "$files" ] || { echo "no Go files under: $*" >&2; exit 2; }

# shellcheck disable=SC2086
out=$(count $files)
[ "$ALL" ] && printf '%s\n' "$out"
over=$(awk -v max="$MAXLOC" '$1 > max' <<<"$out")
[ -z "$over" ] && exit 0
echo "files over $MAXLOC lines of code:" >&2
printf '%s\n' "$over" >&2
exit 1
