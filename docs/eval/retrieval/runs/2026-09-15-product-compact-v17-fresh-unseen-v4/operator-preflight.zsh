#!/bin/zsh
set -euo pipefail

if (( $# != 2 )); then
  print -u2 'usage: operator-preflight.zsh <repo-root> <clean-pinned-cobra-checkout>'
  exit 2
fi

repo_root=$1
cobra_checkout=$2
run_rel='docs/eval/retrieval/runs/2026-09-15-product-compact-v17-fresh-unseen-v4'
candidate='26a36f0958cb16d925c4dd871cda76d0e117b15a'
candidate_tree='6ab1d7075c787b7a8884114d5d732be812cb325f'
corpus='a0a6ae020bb3899ff0276067863e50523f897370'
dataset_digest='c4115b6331e46f1a8c0b0daad020e86e4826a51de8e06a68e3211dea0770a3a6'

[[ $(git -C "$repo_root" rev-parse "$candidate") == "$candidate" ]]
[[ $(git -C "$repo_root" rev-parse "$candidate^{tree}") == "$candidate_tree" ]]
[[ $(git -C "$cobra_checkout" rev-parse HEAD) == "$corpus" ]]
git -C "$cobra_checkout" diff-index --quiet HEAD --
[[ -z $(git -C "$cobra_checkout" ls-files --others --exclude-standard) ]]
[[ $(shasum -a 256 "$repo_root/$run_rel/sealed-dataset.json" | awk '{print $1}') == "$dataset_digest" ]]

git -C "$repo_root" diff --exit-code "$candidate" HEAD -- . \
  ":(exclude)$run_rel/**"

candidate_view=$(git -C "$repo_root" ls-tree -r "$candidate" | shasum -a 256 | awk '{print $1}')
head_view=$(git -C "$repo_root" ls-tree -r HEAD | awk -v p="$run_rel/" 'index($4,p)!=1' | shasum -a 256 | awk '{print $1}')
[[ "$candidate_view" == "$head_view" ]]

git -C "$repo_root" diff-index --quiet HEAD --
[[ -z $(git -C "$repo_root" ls-files --others --exclude-standard) ]]

print "candidate=$candidate"
print "candidate_tree=$candidate_tree"
print "corpus=$corpus"
print "dataset_sha256=$dataset_digest"
print "outside_run_tree_sha256=$head_view"
print 'preflight=PASS'
