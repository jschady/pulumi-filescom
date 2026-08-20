#!/usr/bin/env bash
# Usage: scripts/lint-prose.sh [file ...]
# No argument: every prose page of the repository, and every committed schema description.

set -euo pipefail

cd "$(dirname "$0")/.."

MAX_WORDS=25
MAX_STEP_WORDS=20

# An item of a comma chain is a short phrase. A longer segment is a clause, and a sentence of
# clauses is not the list this rule looks for.
MAX_CHAIN_ITEM_WORDS=6

BANNED_WORDS='utilize|utilizes|leverage|leverages|employ|employs|via|in order to|prior to'
BANNED_WORDS="$BANNED_WORDS"'|subsequent to|following|ensure|ensures|may|might|functionality'
BANNED_WORDS="$BANNED_WORDS"'|capabilities|i\.e\.|e\.g\.|etc\.|please|simply|just|easily|merely'
BANNED_WORDS="$BANNED_WORDS"'|note that|it should be noted that|allows you to|enables you to'
BANNED_WORDS="$BANNED_WORDS"'|in the event that|at this time|currently|a number of|several'
BANNED_WORDS="$BANNED_WORDS"'|desired|aforementioned|the above|in terms of|with respect to'
BANNED_WORDS="$BANNED_WORDS"'|respectively|powerful|seamless|robust|blazing|effortless'
# The words below name a concept that already has its word. The glossary of the contribution page
# carries the whole set; these are the ones a description reaches for most.
BANNED_WORDS="$BANNED_WORDS"'|field|fields|attribute|attributes|token|tokens|credential|credentials'

# Two patterns, never one string: this repo must not carry the writing standard's own name, not
# even inside this script. The prefix is optional; the three letters alone would match "system".
STANDARD_PATTERN='([aA][sS][dD][ -]?)?[sS][tT][eE][ -]?100|simplified[ -]technical[ -]english'
STANDARD_PATTERN="$STANDARD_PATTERN"'|controlled[ -]english'

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT
findings="$tmpdir/findings.txt"
: > "$findings"

check_files=()
check_labels=()
check_indexes=()

add_target() {
	check_files+=("$1")
	check_labels+=("$2")
	check_indexes+=("${3:-}")
}

refuse() {
	echo "$1"
	exit 1
}

# A bridged provider converts its entity prose from the upstream submodule, which this repo does
# not author and must not reword. Only the summary and the config properties are ours.
schema_selector() {
	if [ -d upstream ]; then
		echo 'map(select(.value == ["description"]
			or (.value | length == 4 and .[0] == "config" and .[1] == "variables")))'
	else
		echo '.'
	fi
}

# Every description key owns one line of the schema, because a JSON string holds its newlines as
# an escape. So the keys that grep counts and the paths that jq walks are the same list, in order.
add_schema_descriptions() {
	local schema="$1" selector stem extracted index meta keys
	selector="$(schema_selector)"
	stem="$tmpdir/schema-$(echo "$schema" | tr '/' '-')"
	extracted="$stem.txt"
	index="$stem.index"
	meta="$stem.meta"
	keys="$stem.keys"

	local program='. as $doc | [paths | select(.[-1] == "description")] | to_entries
		| map(. as $entry | select(($doc | getpath($entry.value)) | type == "string"))
		| '"$selector"' | .[] | . as $entry | ($doc | getpath($entry.value)) as $text | '

	jq -r "$program"'$text + "\n"' "$schema" > "$extracted"
	jq -r "$program"'"\($entry.key)\t\(($text + "\n") | split("\n") | length)"' "$schema" > "$meta"
	grep -n '^[[:space:]]*"description"[[:space:]]*:' "$schema" | cut -d: -f1 > "$keys" || true

	local counted walked
	counted="$(wc -l < "$keys" | tr -d '[:space:]')"
	walked="$(jq '[paths | select(.[-1] == "description")] | length' "$schema")"
	if [ "$counted" -ne "$walked" ]; then
		refuse "$schema: the description keys and the schema lines disagree. Write the schema with one key per line."
	fi

	awk -F'\t' 'NR == FNR { line[FNR - 1] = $1; next }
		{ for (i = 0; i < $2; i++) print line[$1] }' "$keys" "$meta" > "$index"
	add_target "$extracted" "$schema" "$index"
}

# The standard's name must appear in no file this repo ships, not only in the prose targets.
# --others reaches a page that is written but not yet added.
check_repository_for_the_standard_name() {
	local files="$tmpdir/repo-files.txt" hits="$tmpdir/repo-standard.txt" file
	if ! git ls-files --cached --others --exclude-standard -z > "$files"; then
		refuse "The prose check cannot list the files of this repository. Run it inside the repository."
	fi
	if [ ! -s "$files" ]; then
		refuse "The prose check found no file to read. Run it inside the repository."
	fi
	xargs -0 grep -I -l -i -E "$STANDARD_PATTERN" < "$files" > "$hits" 2>/dev/null || true
	while IFS= read -r file; do
		printf '%s: delete the reference to the writing standard\n' "$file" >> "$findings"
	done < "$hits"
}

collect_default_targets() {
	local page
	for page in README.md CONTRIBUTING.md .github/workflows/README.md; do
		if [ -f "$page" ]; then
			add_target "$page" "$page"
		fi
	done
	if [ -d docs ]; then
		while IFS= read -r doc; do
			add_target "$doc" "$doc"
		done < <(find docs -name '*.md' | sort)
	fi
	local schema
	for schema in provider/cmd/pulumi-resource-*/schema.json; do
		if [ -f "$schema" ]; then
			add_schema_descriptions "$schema"
		fi
	done
	if [ "${#check_files[@]}" -eq 0 ]; then
		refuse "The prose check found no page and no schema to read. Run it inside the repository."
	fi
}

# Code literals are exempt from the word rules, so this blanks fenced blocks and
# inline code spans. Blank lines keep every remaining line number correct.
strip_code() {
	awk '
		/^[[:space:]]*```/ { fence = !fence; print ""; next }
		fence { print ""; next }
		{ gsub(/`[^`]*`/, "CODE"); print }
	' "$1"
}

check_banned_words() {
	local label="$2" hits="$tmpdir/banned.txt" file="$tmpdir/prose.txt"
	strip_code "$1" > "$file"
	if grep -n -i -o -w -E "$BANNED_WORDS" "$file" > "$hits"; then
		while IFS=: read -r line word; do
			printf '%s:%s: replace the banned word "%s"\n' "$label" "$line" "$word" >> "$findings"
		done < "$hits"
	fi
}

check_standard_name() {
	local file="$1" label="$2" hits="$tmpdir/standard.txt"
	if grep -n -i -E "$STANDARD_PATTERN" "$file" > "$hits"; then
		while IFS=: read -r line _rest; do
			printf '%s:%s: delete the reference to the writing standard\n' "$label" "$line" >> "$findings"
		done < "$hits"
	fi
}

check_sentences() {
	local file="$1" label="$2"
	awk -v label="$label" -v maxd="$MAX_WORDS" -v maxs="$MAX_STEP_WORDS" \
		-v maxitem="$MAX_CHAIN_ITEM_WORDS" '
		function trim(text) { gsub(/^[[:space:]]+|[[:space:]]+$/, "", text); return text }
		function count_words(text,   parts) { return split(trim(text), parts, /[[:space:]]+/) }
		# A chain of three or more short items is a list that lost its bullets. A chain of code
		# literals is a set of names, which the writing rules leave alone.
		function is_chain(sentence,   copy, segments, count, index_, tail, item) {
			copy = sentence
			if (gsub(/CODE/, "&", copy) >= 3) return 0
			count = split(sentence, segments, /,[[:space:]]*/)
			if (count < 3) return 0
			tail = trim(segments[count])
			if (tail !~ /^(and|or)[[:space:]]/) return 0
			sub(/^(and|or)[[:space:]]+/, "", tail)
			if (count_words(tail) > maxitem) return 0
			for (index_ = 2; index_ < count; index_++) {
				item = trim(segments[index_])
				if (item == "" || count_words(item) > maxitem) return 0
			}
			return 1
		}
		function check_text(text, line, max, chainable,   sentences, count, index_, sentence, size) {
			gsub(/([.!?])[[:space:]]+/, "\n", text)
			sub(/[.!?][[:space:]]*$/, "", text)
			count = split(text, sentences, "\n")
			for (index_ = 1; index_ <= count; index_++) {
				sentence = trim(sentences[index_])
				if (sentence == "") continue
				size = count_words(sentence)
				if (size > max) {
					printf "%s:%d: split the sentence of %d words; the limit is %d words\n", \
						label, line, size, max
				}
				if (chainable && is_chain(sentence)) {
					printf "%s:%d: write the comma chain as a list\n", label, line
				}
			}
		}
		function flush() {
			if (buf !~ /^[[:space:]]*$/) check_text(buf, start, limit, 1)
			buf = ""
		}
		# A heading is a short noun phrase or one instruction, so it carries the shorter limit.
		# A table cell holds reference prose, which carries the descriptive limit.
		function check_heading(   text) {
			text = $0
			gsub(/`[^`]*`/, "CODE", text)
			sub(/^[[:space:]]*#+[[:space:]]*/, "", text)
			check_text(text, NR, maxs, 0)
		}
		function check_row(   text, cells, count, index_) {
			text = $0
			gsub(/`[^`]*`/, "CODE", text)
			count = split(text, cells, /\|/)
			for (index_ = 1; index_ <= count; index_++) check_text(cells[index_], NR, maxd, 0)
		}
		/^[[:space:]]*```/ { flush(); fence = !fence; next }
		fence { next }
		/^[[:space:]]*$/ { flush(); next }
		/^[[:space:]]*#/ { flush(); check_heading(); next }
		/^[[:space:]]*\|/ { flush(); check_row(); next }
		{
			line = $0
			gsub(/`[^`]*`/, "CODE", line)
			if (line ~ /^[[:space:]]*([-*+]|[0-9]+\.)[[:space:]]+/) {
				flush()
				limit = (line ~ /^[[:space:]]*[0-9]+\.[[:space:]]+/) ? maxs : maxd
				sub(/^[[:space:]]*([-*+]|[0-9]+\.)[[:space:]]+/, "", line)
			} else if (buf == "") {
				limit = maxd
			}
			if (buf == "") start = NR
			buf = buf " " line
		}
		END { flush() }
	' "$file" >> "$findings"
}

# A finding of a schema names the line of the extracted stream, and the reader opens the schema.
# The index carries the line of the description in the file, so the report names that line.
relabel_findings() {
	local label="$1" index="$2" rewritten="$tmpdir/relabelled.txt"
	awk -v label="$label" -v indexfile="$index" '
		BEGIN { while ((getline line < indexfile) > 0) source[++streamed] = line }
		{
			head = label ":"
			if (substr($0, 1, length(head)) == head) {
				rest = substr($0, length(head) + 1)
				number = rest + 0
				if (number > 0 && number in source) {
					sub(/^[0-9]+/, source[number], rest)
					print head rest
					next
				}
			}
			print
		}
	' "$findings" > "$rewritten"
	mv "$rewritten" "$findings"
}

if [ "$#" -gt 0 ]; then
	for arg in "$@"; do
		add_target "$arg" "$arg"
	done
else
	collect_default_targets
	check_repository_for_the_standard_name
fi

index=0
while [ "$index" -lt "${#check_files[@]}" ]; do
	file="${check_files[$index]}"
	label="${check_labels[$index]}"
	check_banned_words "$file" "$label"
	check_standard_name "$file" "$label"
	check_sentences "$file" "$label"
	if [ -n "${check_indexes[$index]}" ]; then
		relabel_findings "$label" "${check_indexes[$index]}"
	fi
	index=$((index + 1))
done

if [ -s "$findings" ]; then
	sort -u "$findings"
	echo "The prose check found problems."
	exit 1
fi

echo "The prose check passed."
