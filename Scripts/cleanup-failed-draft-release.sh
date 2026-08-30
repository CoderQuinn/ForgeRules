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

readonly state_root="$(mktemp -d /tmp/forgerules-cleanup-draft.XXXXXX)"
cleanup() {
    rm -rf "${state_root}"
}
trap cleanup EXIT

release_exists=false
release_query_status=0
if github_api_optional \
    "repos/${repository}/releases/${release_id}" \
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

tagged_release_exists=false
tagged_release_query_status=0
if github_api_optional \
    "repos/${repository}/releases/tags/${release_tag}" \
    "${state_root}/tagged-release.json"; then
    tagged_release_exists=true
else
    tagged_release_query_status="$?"
    if [[ "${tagged_release_query_status}" -ne 4 ]]; then
        exit "${tagged_release_query_status}"
    fi
fi

if [[ "${tagged_release_exists}" == "true" ]]; then
    tagged_release_id="$(jq -r '.id' "${state_root}/tagged-release.json")"
    if [[ "${tagged_release_id}" != "${release_id}" ]]; then
        echo "error: refusing cleanup because tag belongs to release ID ${tagged_release_id}, not ${release_id}" >&2
        exit 1
    fi
fi

if [[ "${release_exists}" != "true" ]]; then
    if [[ "${tag_exists}" == "true" || "${tagged_release_exists}" == "true" ]]; then
        echo "error: refusing tag cleanup because exact release ID ${release_id} is absent" >&2
        exit 1
    fi
    echo "Failed draft ID ${release_id} is already absent: ${repository}@${release_tag}"
    exit 0
fi

actual_id="$(jq -r '.id' "${state_root}/release.json")"
actual_tag="$(jq -r '.tag_name' "${state_root}/release.json")"
actual_draft="$(jq -r '.draft' "${state_root}/release.json")"
target_commitish="$(jq -r '.target_commitish' "${state_root}/release.json")"
if [[ "${actual_id}" != "${release_id}" || "${actual_tag}" != "${release_tag}" ]]; then
    echo "error: refusing cleanup of unexpected release identity ${actual_id}/${actual_tag}" >&2
    exit 1
fi
if [[ "${actual_draft}" != "true" ]]; then
    echo "error: refusing to delete non-draft release ID ${release_id}" >&2
    exit 1
fi
if [[ "${target_commitish}" != "${expected_commit}" ]]; then
    echo "error: refusing to delete draft with target ${target_commitish}, want ${expected_commit}" >&2
    exit 1
fi

if [[ "${tag_exists}" == "true" ]]; then
    resolved_commit="$(peel_exact_tag_ref_to_commit \
        "${repository}" \
        "${state_root}/tag-ref.json" \
        "${state_root}")"
    if [[ "${resolved_commit}" != "${expected_commit}" ]]; then
        echo "error: refusing to delete tag at ${resolved_commit}, want ${expected_commit}" >&2
        exit 1
    fi
fi

if [[ "${tag_exists}" == "true" ]]; then
    gh api \
        --method DELETE \
        "repos/${repository}/git/refs/tags/${release_tag}"
fi
gh api \
    --method DELETE \
    "repos/${repository}/releases/${release_id}"

echo "Cleaned recoverable failed draft ID ${release_id}: ${repository}@${release_tag}"
