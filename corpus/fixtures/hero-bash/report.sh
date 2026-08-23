#!/usr/bin/env bash
# report.sh — second script, also defines init (creating ambiguity for
# `callers(init)` — both serve.sh and report.sh define init; bash has no
# askable cross-file construct per §5.5 so the cross-file callers
# operation is honest-empty regardless).

init() {
  echo "init from report.sh"
}
