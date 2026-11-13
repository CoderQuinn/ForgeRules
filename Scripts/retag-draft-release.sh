#!/usr/bin/env bash

set -euo pipefail

if [[ "$#" -ne 5 ]]; then
    echo "usage: $0 <owner/repository> <release-id> <staging-tag> <destination-tag> <expected-commit>" >&2
    exit 64
fi

readonly repository="$1"
readonly release_id="$2"
readonly staging_tag="$3"
readonly destination_tag="$4"
readonly expected_commit="$5"
readonly script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# shellcheck source=Scripts/lib/github-release.sh
source "${script_directory}/lib/github-release.sh"

validate_github_repository "${repository}"
validate_release_id "${release_id}"
validate_release_tag "${staging_tag}"
validate_release_tag "${destination_tag}"
validate_commit_sha "${expected_commit}"
if [[ ! "${staging_tag}" =~ ^latest-staging-[0-9]+-[0-9]+$ || "${destination_tag}" != "latest" ]]; then
    echo "error: retagging is restricted to latest-staging-RUN-ATTEMPT -> latest" >&2
    exit 64
fi

readonly state_root="$(mktemp -d /tmp/forgerules-retag-draft.XXXXXX)"
cleanup() {
    rm -rf "${state_root}"
}
trap cleanup EXIT

optional_must_be_absent() {
    local endpoint="$1"
    local output_file="$2"
    if github_api_optional "${endpoint}" "${output_file}"; then
        echo "error: namespace object unexpectedly exists: ${endpoint}" >&2
        return 1
    else
        local query_status="$?"
        if [[ "${query_status}" -ne 4 ]]; then
            return "${query_status}"
        fi
    fi
}

require_staging_state() {
    local prefix="$1"
    require_expected_release_metadata \
        "${repository}" \
        "${release_id}" \
        "${staging_tag}" \
        "${expected_commit}" \
        true \
        "${state_root}/${prefix}-metadata.tsv"
    query_releases_with_tag \
        "${repository}" \
        "${staging_tag}" \
        "${state_root}/${prefix}-list.json" \
        "${state_root}"
    if [[ "$(jq 'length' "${state_root}/${prefix}-list.json")" -ne 1 || \
          "$(jq -r '.[0].id' "${state_root}/${prefix}-list.json")" != "${release_id}" || \
          "$(jq -r '.[0].draft' "${state_root}/${prefix}-list.json")" != "true" ]]; then
        echo "error: staging namespace does not contain exactly draft release ID ${release_id}" >&2
        return 1
    fi
    optional_must_be_absent \
        "repos/${repository}/releases/tags/${staging_tag}" \
        "${state_root}/${prefix}-release-by-tag.json"
    optional_must_be_absent \
        "repos/${repository}/git/ref/tags/${staging_tag}" \
        "${state_root}/${prefix}-tag-ref.json"
}

require_destination_empty() {
    local prefix="$1"
    query_releases_with_tag \
        "${repository}" \
        "${destination_tag}" \
        "${state_root}/${prefix}-list.json" \
        "${state_root}"
    if [[ "$(jq 'length' "${state_root}/${prefix}-list.json")" -ne 0 ]]; then
        echo "error: destination release namespace ${destination_tag} is not empty" >&2
        return 1
    fi
    optional_must_be_absent \
        "repos/${repository}/releases/tags/${destination_tag}" \
        "${state_root}/${prefix}-release-by-tag.json"
    optional_must_be_absent \
        "repos/${repository}/git/ref/tags/${destination_tag}" \
        "${state_root}/${prefix}-tag-ref.json"
}

classify_exact_release() {
    local output_file="$1"
    gh api "repos/${repository}/releases/${release_id}" >"${output_file}"
    if jq -e \
        --argjson id "${release_id}" \
        --arg tag "${destination_tag}" \
        --arg target "${expected_commit}" \
        '.id == $id and .tag_name == $tag and .target_commitish == $target and .draft == true' \
        "${output_file}" >/dev/null; then
        printf 'destination\n'
    elif jq -e \
        --argjson id "${release_id}" \
        --arg tag "${staging_tag}" \
        --arg target "${expected_commit}" \
        '.id == $id and .tag_name == $tag and .target_commitish == $target and .draft == true' \
        "${output_file}" >/dev/null; then
        printf 'staging\n'
    else
        printf 'other\n'
    fi
}

require_destination_draft_state() {
    local prefix="$1"
    require_expected_release_metadata \
        "${repository}" \
        "${release_id}" \
        "${destination_tag}" \
        "${expected_commit}" \
        true \
        "${state_root}/${prefix}-metadata.tsv"
    query_releases_with_tag \
        "${repository}" \
        "${destination_tag}" \
        "${state_root}/${prefix}-list.json" \
        "${state_root}"
    if [[ "$(jq 'length' "${state_root}/${prefix}-list.json")" -ne 1 || \
          "$(jq -r '.[0].id' "${state_root}/${prefix}-list.json")" != "${release_id}" || \
          "$(jq -r '.[0].draft' "${state_root}/${prefix}-list.json")" != "true" ]]; then
        echo "error: retagged namespace does not contain exactly draft release ID ${release_id}" >&2
        return 1
    fi
    optional_must_be_absent \
        "repos/${repository}/releases/tags/${destination_tag}" \
        "${state_root}/${prefix}-release-by-tag.json"
    optional_must_be_absent \
        "repos/${repository}/git/ref/tags/${destination_tag}" \
        "${state_root}/${prefix}-tag-ref.json"
    query_releases_with_tag \
        "${repository}" \
        "${staging_tag}" \
        "${state_root}/${prefix}-staging-list.json" \
        "${state_root}"
    if [[ "$(jq 'length' "${state_root}/${prefix}-staging-list.json")" -ne 0 ]]; then
        echo "error: staging release namespace still exists after exact-ID retag" >&2
        return 1
    fi
    optional_must_be_absent \
        "repos/${repository}/git/ref/tags/${staging_tag}" \
        "${state_root}/${prefix}-staging-tag-ref.json"
}

require_staging_state before-retag
require_destination_empty before-retag

# GitHub does not provide conditional PATCH for releases. Reconcile the exact
# ID after every response; if the request failed before applying, retry once
# only after the complete pre-state is still intact.
for attempt in 1 2; do
    patch_succeeded=false
    if gh api \
        --method PATCH \
        -f tag_name="${destination_tag}" \
        -f target_commitish="${expected_commit}" \
        -F draft=true \
        -f make_latest=false \
        "repos/${repository}/releases/${release_id}" >/dev/null; then
        patch_succeeded=true
    fi

    reconciled_state="$(classify_exact_release "${state_root}/reconcile-${attempt}.json")"
    if [[ "${reconciled_state}" == "destination" ]]; then
        require_destination_draft_state "destination-${attempt}"
        echo "Retagged draft release ID ${release_id}: ${staging_tag} -> ${destination_tag}"
        exit 0
    fi
    if [[ "${reconciled_state}" != "staging" ]]; then
        echo "error: exact release ID ${release_id} entered an unexpected state after retag PATCH" >&2
        exit 1
    fi
    if [[ "${attempt}" -eq 2 ]]; then
        echo "error: retag PATCH did not apply after one safe retry (last response success=${patch_succeeded})" >&2
        exit 1
    fi
    require_staging_state before-retag-retry
    require_destination_empty before-retag-retry
done
