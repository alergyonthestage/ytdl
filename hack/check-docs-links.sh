#!/usr/bin/env bash
#
# Structural link checker for the documentation tree.
#
# Verifies four things across every Markdown file in the repo - tracked ones and
# newly written ones alike:
#
#   1. every relative link target resolves to a file that exists;
#   2. every "#anchor" on a local target matches a heading or an explicit
#      <a id="..."> in that file;
#   3. ADR numbers are unique across all decisions/ folders — the numbering is
#      one global sequence even though the files sit under several domains;
#   4. no document links *to* a handoff. The handoff is ephemeral and is deleted
#      when its cycle closes, so an inbound link is a guaranteed future dangle;
#      it links out to the durable documents, never the reverse.
#
# External http(s) links are ignored on purpose: this is a structural check of
# the repository, not a network one.
#
# Exit 0 when clean, 1 on any failure. One line per failure.
#
# Findings accumulate in a file, never in a shell variable: the reporting runs
# inside pipelines, and a variable incremented in a subshell does not survive it.

set -u
LC_ALL=C
export LC_ALL

# The repo root is derived from this script's own location, not from git: the
# containerised checkout trips git's "dubious ownership" guard, and a checker
# that needs the caller to pre-export GIT_CONFIG_* is one that silently does not
# run.
repo_root=$(cd "$(dirname "$0")/.." && pwd) || exit 2
cd "$repo_root" || exit 2

# Every git call goes through this wrapper. The -c carries the exception per
# invocation instead of per environment, so nothing has to be exported and the
# caller's own GIT_CONFIG_* is left alone.
vcs() { git -c safe.directory="$repo_root" "$@"; }

if ! vcs rev-parse --show-toplevel >/dev/null 2>&1; then
	printf 'not a usable git work tree: %s\n' "$repo_root" >&2
	exit 2
fi

tmpdir=$(mktemp -d) || exit 2
trap 'rm -rf "$tmpdir"' EXIT

findings="$tmpdir/findings"
: >"$findings"

report() { printf '%s\n' "$1" >>"$findings"; }

# slugify <text> — GitHub's heading-to-anchor rule: lowercase, drop everything
# that is not alphanumeric/space/hyphen/underscore, spaces become hyphens.
# The trailing newline is not cosmetic: without it every slug is appended to the
# same line of the cache and no anchor ever matches.
slugify() {
	printf '%s\n' "$1" |
		tr 'A-Z' 'a-z' |
		sed 's/[^a-z0-9 _-]//g; s/ /-/g'
}

# normalise <path> — collapse "." and ".." segments in a repo-relative path.
normalise() {
	printf '%s' "$1" | awk -F/ '{
		n = 0
		for (i = 1; i <= NF; i++) {
			if ($i == "." || $i == "") continue
			if ($i == "..") { if (n > 0) n--; continue }
			out[++n] = $i
		}
		s = ""
		for (i = 1; i <= n; i++) s = s (i > 1 ? "/" : "") out[i]
		print s
	}'
}

# anchor_file <repo-relative path> — path of the cached anchor list for a file,
# built on first use. Headings inside fenced code blocks are not anchors.
anchor_file() {
	local f=$1 cache
	cache="$tmpdir/$(printf '%s' "$f" | tr '/.' '__').anchors"
	if [ ! -f "$cache" ]; then
		: >"$cache"
		awk '
			/^```/         { fence = !fence; next }
			fence          { next }
			/^#+[ \t]/    { h = $0; sub(/^#+[ \t]*/, "", h); sub(/[ \t]+$/, "", h); print "H" h }
			{
				s = $0
				while (match(s, /<a[ \t]+id="[^"]*"/)) {
					a = substr(s, RSTART, RLENGTH)
					sub(/^<a[ \t]+id="/, "", a)
					sub(/"$/, "", a)
					print "A" a
					s = substr(s, RSTART + RLENGTH)
				}
			}
		' "$f" >"$cache.raw"
		while IFS= read -r line; do
			case $line in
			A*) printf '%s\n' "${line#A}" >>"$cache" ;;
			H*) slugify "${line#H}" >>"$cache" ;;
			esac
		done <"$cache.raw"
	fi
	printf '%s' "$cache"
}

is_handoff() {
	case ${1##*/} in
	handoff.md | handoff-*.md) return 0 ;;
	*) return 1 ;;
	esac
}

# ------------------------------------------------------------- checks 1, 2, 4

# --cached --others --exclude-standard: a document that has just been written is
# exactly the one most likely to carry a bad link, and checking only tracked files
# would let it through until after it was committed. Ignored paths stay out.
vcs ls-files --cached --others --exclude-standard '*.md' >"$tmpdir/files"

while IFS= read -r src; do
	src_dir=${src%/*}
	[ "$src_dir" = "$src" ] && src_dir=.

	grep -on '\]([^)]*)' "$src" 2>/dev/null >"$tmpdir/hits" || continue

	while IFS= read -r hit; do
		lineno=${hit%%:*}
		target=${hit#*:}
		target=${target#](}
		target=${target%)}
		target=${target#<}
		target=${target%>}
		case $target in
		*' '*) target=${target%% *} ;;
		esac

		case $target in
		http://* | https://* | mailto:* | '') continue ;;
		esac

		anchor=
		path=$target
		case $target in
		'#'*)
			anchor=${target#\#}
			path=$src
			;;
		*'#'*)
			anchor=${target#*\#}
			path=${target%%#*}
			;;
		esac

		if [ "$path" != "$src" ]; then
			path=$(normalise "$src_dir/$path")
			if [ ! -e "$path" ]; then
				report "$src:$lineno -> $target (target does not exist)"
				continue
			fi
			# A link to a directory is legitimate Markdown; it has no anchors.
			if [ -d "$path" ]; then
				continue
			fi
			if ! is_handoff "$src" && is_handoff "$path"; then
				report "$src:$lineno -> $target (links INTO an ephemeral handoff)"
			fi
		fi

		if [ -n "$anchor" ] && ! grep -qxF "$anchor" "$(anchor_file "$path")"; then
			report "$src:$lineno -> $target (no such anchor in ${path##*/})"
		fi
	done <"$tmpdir/hits"
done <"$tmpdir/files"

# ------------------------------------------------------------------- check 3

vcs ls-files '*/decisions/*.md' | sed 's#.*/##' | grep -o '^[0-9]\{4\}' |
	sort | uniq -d >"$tmpdir/dupes"

while IFS= read -r dup; do
	[ -n "$dup" ] || continue
	report "ADR number $dup is used by more than one file: $(vcs ls-files "*/decisions/${dup}-*.md" | tr '\n' ' ')"
done <"$tmpdir/dupes"

# ------------------------------------------------------------------- verdict

if [ -s "$findings" ]; then
	sort "$findings"
	printf '\n%s failure(s).\n' "$(wc -l <"$findings" | tr -d ' ')"
	exit 1
fi

printf 'All documentation links resolve (%s files checked).\n' "$(wc -l <"$tmpdir/files" | tr -d ' ')"
exit 0
