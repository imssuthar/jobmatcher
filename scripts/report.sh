#!/usr/bin/env bash
# Filters `go test -v` output down to the useful lines and fails if any test failed.
status=0
while IFS= read -r line; do
  case "$line" in
    "=== RUN"*|"=== PAUSE"*|"=== CONT"*|"=== NAME"*) continue ;;
  esac
  printf '%s\n' "$line"
  case "$line" in
    FAIL*|*"--- FAIL"*|panic:*) status=1 ;;
  esac
done
exit $status
