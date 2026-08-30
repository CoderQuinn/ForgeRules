#!/usr/bin/env bash

set -euo pipefail

if [[ "$#" -ne 4 ]]; then
    echo "usage: $0 <owner/repository> <release-id> <release-tag> <expected-commit>" >&2
    exit 64
fi

readonly repository="$1"
readonly release_id="$2"
readonly release_tag="$3"
readonly expected_commit="$4"
readonly script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# shellcheck source=Scripts/lib/github-release.sh
source "${script_directory}/lib/github-release.sh"

validate_github_repository "${repository}"
validate_release_id "${release_id}"
validate_release_tag "${release_tag}"
validate_commit_sha "${expected_commit}"

readonly state_root="$(mktemp -d /tmp/forgerules-publish-draft.XXXXXX)"
cleanup() {
    rm -rf "${state_root}"
}
trap cleanup EXIT

require_expected_release_metadata \
    "${repository}" \
    "${release_id}" \
    "${release_tag}" \
    "${expected_commit}" \
    true \
    "${state_root}/metadata.tsv"

if [[ "${release_tag}" == "latest" ]]; then
    readonly make_latest="true"
else
    readonly make_latest="false"
fi

gh api \
    --method PATCH \
    -F draft=false \
    -f make_latest="${make_latest}" \
    "repos/${repository}/releases/${release_id}" >/dev/null

echo "Published verified draft release ID ${release_id}: ${repository}@${release_tag}"
