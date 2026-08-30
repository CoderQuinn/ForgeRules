#!/usr/bin/env bash

set -euo pipefail

if [[ "$#" -ne 1 ]]; then
    echo "usage: $0 <owner/repository>" >&2
    exit 64
fi

readonly repository="$1"
readonly release_tag="latest"
readonly script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# shellcheck source=Scripts/lib/github-release.sh
source "${script_directory}/lib/github-release.sh"

validate_github_repository "${repository}"

readonly state_root="$(mktemp -d /tmp/forgerules-delete-latest.XXXXXX)"
cleanup() {
    rm -rf "${state_root}"
}
trap cleanup EXIT

release_exists=false
release_query_status=0
if github_api_optional \
    "repos/${repository}/releases/tags/${release_tag}" \
    "${state_root}/release.json"; then
    release_exists=true
else
    release_query_status="$?"
    if [[ "${release_query_status}" -ne 4 ]]; then
        exit "${release_query_status}"
    fi
fi

tag_exists=false
tag_query_status=0
if github_api_optional \
    "repos/${repository}/git/ref/tags/${release_tag}" \
    "${state_root}/tag-ref.json"; then
    tag_exists=true
else
    tag_query_status="$?"
    if [[ "${tag_query_status}" -ne 4 ]]; then
        exit "${tag_query_status}"
    fi
fi

if [[ "${release_exists}" == "true" ]]; then
    release_id="$(jq -r '.id' "${state_root}/release.json")"
    validate_release_id "${release_id}"
    gh api \
        --method DELETE \
        "repos/${repository}/releases/${release_id}"
fi
if [[ "${tag_exists}" == "true" ]]; then
    gh api \
        --method DELETE \
        "repos/${repository}/git/refs/tags/${release_tag}"
fi

echo "Previous latest release state removed (absence is allowed): ${repository}"
