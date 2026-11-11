#!/usr/bin/env bash
# Picks the sqlite-race tests for one CI shard, balancing shards by measured time.
#
# Usage: sqlite-race-shard.sh <shard> <shards> <weights file> < test-list
#
# stdin holds one top-level test name per line (`go test -list` output).
# The weights file holds `<test name>\t<seconds>` lines; `#` lines are comments.
# A test missing from the weights file gets the mean of the known weights, and a
# weight line for a test not in the list is ignored. Tests are sorted by weight
# (heaviest first, then by name) and each goes to the lightest shard so far, the
# lowest shard number on ties (LPT). Every runner sees the same input, so every
# runner computes the same assignment, and each test lands in exactly one shard.
#
# Prints the chosen shard's test names, one per line. Writes the expected total
# of every shard and the count of tests without a weight to stderr.
# Plain POSIX awk only (no asort or other gawk extensions): runners may use mawk.
set -euo pipefail

if [ "$#" -ne 3 ]; then
  echo "usage: $0 <shard> <shards> <weights file> < test-list" >&2
  exit 2
fi

shard=$1
shards=$2
weights=$3

case $shards in '' | *[!0-9]*) echo "shards must be a positive integer: $shards" >&2; exit 2 ;; esac
case $shard in '' | *[!0-9]*) echo "shard must be a non-negative integer: $shard" >&2; exit 2 ;; esac
if [ "$shards" -lt 1 ] || [ "$shard" -ge "$shards" ]; then
  echo "shard $shard is outside 0..$((shards - 1))" >&2
  exit 2
fi
if [ ! -r "$weights" ]; then
  echo "weights file not readable: $weights" >&2
  exit 2
fi

# Pass 1: give every listed test a weight. Output: "<weight>\t<name>".
# Pass 2: sort heaviest first, ties by name, in a fixed byte order.
# Pass 3: assign greedily to the lightest shard and print the requested one.
awk -F '\t' -v wfile="$weights" '
  BEGIN {
    n = 0
    while ((rc = getline line < wfile) > 0) {
      n++
      if (line ~ /^#/ || line ~ /^[ \t]*$/) continue
      f = split(line, part, "\t")
      if (f != 2 || part[1] == "" || part[2] !~ /^[0-9]+$/ || part[2] + 0 < 1) {
        printf "bad weights line %d: %s\n", n, line > "/dev/stderr"
        bad = 1
        exit 1
      }
      weight[part[1]] = part[2] + 0
    }
    if (rc < 0) {
      printf "cannot read weights file %s\n", wfile > "/dev/stderr"
      bad = 1
      exit 1
    }
    close(wfile)
  }
  {
    name = $0
    sub(/\r$/, "", name)
    if (name ~ /^[ \t]*$/) next
    if (name in seen) next
    seen[name] = 1
    count++
    names[count] = name
    if (name in weight) {
      known++
      sum += weight[name]
    }
  }
  END {
    if (bad) exit 1
    mean = known > 0 ? int(sum / known + 0.5) : 1
    if (mean < 1) mean = 1
    missing = 0
    for (i = 1; i <= count; i++) {
      name = names[i]
      if (name in weight) {
        w = weight[name]
      } else {
        w = mean
        missing++
      }
      printf "%d\t%s\n", w, name
    }
    printf "sqlite-race-shard: %d tests, %d without a weight (given the mean %d s)\n", count, missing, mean > "/dev/stderr"
  }
' |
  LC_ALL=C sort -t "$(printf '\t')" -k1,1nr -k2,2 |
  awk -F '\t' -v shard="$shard" -v shards="$shards" '
    BEGIN {
      for (s = 0; s < shards; s++) load[s] = 0
    }
    {
      best = 0
      for (s = 1; s < shards; s++) if (load[s] < load[best]) best = s
      load[best] += $1
      if (best == shard) print $2
    }
    END {
      total = 0
      max = 0
      for (s = 0; s < shards; s++) {
        total += load[s]
        if (load[s] > max) max = load[s]
        printf "sqlite-race-shard: shard %d expected %d s\n", s, load[s] > "/dev/stderr"
      }
      if (total > 0) {
        printf "sqlite-race-shard: largest shard is %.2fx the mean\n", max * shards / total > "/dev/stderr"
      }
    }
  '
