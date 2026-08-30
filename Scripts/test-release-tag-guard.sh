#!/usr/bin/env bash

set -euo pipefail

readonly script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly guard="${script_directory}/check-release-tag-available.sh"
readonly test_root="$(mktemp -d /tmp/forgerules-release-tag-test.XXXXXX)"
readonly remote="${test_root}/remote.git"
readonly seed="${test_root}/seed"

cleanup() {
    rm -rf "${test_root}"
}
trap cleanup EXIT

fail() {
    echo "release-tag guard test failed: $*" >&2
    exit 1
}

git init --bare --quiet "${remote}"
git init --quiet "${seed}"
git -C "${seed}" config user.name "ForgeRules CI"
git -C "${seed}" config user.email "ci@invalid.example"
git -C "${seed}" commit --allow-empty --quiet -m "seed"
git -C "${seed}" tag rules-20260830
git -C "${seed}" remote add origin "${remote}"
git -C "${seed}" push --quiet origin refs/tags/rules-20260830

if "${guard}" "${remote}" rules-20260830 >/dev/null 2>&1; then
    fail "existing dated tag was accepted"
fi

"${guard}" "${remote}" rules-20260831 >/dev/null

if "${guard}" "${remote}" latest >/dev/null 2>&1; then
    fail "mutable latest tag was accepted"
fi

if "${guard}" "${test_root}/missing.git" rules-20260831 >/dev/null 2>&1; then
    fail "unreachable remote was treated as an available tag"
fi

echo "Release-tag guard tests passed"
