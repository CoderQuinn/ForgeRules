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
if [[ "${release_tag}" != "latest" && ! "${release_tag}" =~ ^rules-[0-9]{8}$ ]]; then
    echo "error: staging releases must be retagged to latest before publication" >&2
    exit 64
fi

readonly state_root="$(mktemp -d /tmp/forgerules-publish-draft.XXXXXX)"
cleanup() {
    rm -rf "${state_root}"
}
trap cleanup EXIT

if [[ "${release_tag}" == "latest" ]]; then
    readonly make_latest="true"
else
    readonly make_latest="false"
fi

optional_must_be_absent() {
    local endpoint="$1"
    local output_file="$2"
    if github_api_optional "${endpoint}" "${output_file}"; then
        echo "error: draft tag namespace is already public: ${endpoint}" >&2
        return 1
    else
        local query_status="$?"
        if [[ "${query_status}" -ne 4 ]]; then
            return "${query_status}"
        fi
    fi
}

require_draft_state() {
    local prefix="$1"
    require_expected_release_metadata \
        "${repository}" \
        "${release_id}" \
        "${release_tag}" \
        "${expected_commit}" \
        true \
        "${state_root}/${prefix}-metadata.tsv"
    query_releases_with_tag \
        "${repository}" \
        "${release_tag}" \
        "${state_root}/${prefix}-list.json" \
        "${state_root}"
    if [[ "$(jq 'length' "${state_root}/${prefix}-list.json")" -ne 1 || \
          "$(jq -r '.[0].id' "${state_root}/${prefix}-list.json")" != "${release_id}" || \
          "$(jq -r '.[0].draft' "${state_root}/${prefix}-list.json")" != "true" ]]; then
        echo "error: draft namespace does not contain exactly release ID ${release_id}" >&2
        return 1
    fi
    optional_must_be_absent \
        "repos/${repository}/releases/tags/${release_tag}" \
        "${state_root}/${prefix}-release-by-tag.json"
    optional_must_be_absent \
        "repos/${repository}/git/ref/tags/${release_tag}" \
        "${state_root}/${prefix}-tag-ref.json"
}

classify_exact_release() {
    local output_file="$1"
    gh api "repos/${repository}/releases/${release_id}" >"${output_file}"
    if jq -e \
        --argjson id "${release_id}" \
        --arg tag "${release_tag}" \
        --arg target "${expected_commit}" \
        '.id == $id and .tag_name == $tag and .target_commitish == $target and .draft == false' \
        "${output_file}" >/dev/null; then
        printf 'published\n'
    elif jq -e \
        --argjson id "${release_id}" \
        --arg tag "${release_tag}" \
        --arg target "${expected_commit}" \
        '.id == $id and .tag_name == $tag and .target_commitish == $target and .draft == true' \
        "${output_file}" >/dev/null; then
        printf 'draft\n'
    else
        printf 'other\n'
    fi
}

require_published_state() {
    local prefix="$1"
    local published_state_root="${state_root}/${prefix}-published"
    mkdir "${published_state_root}"
    require_expected_release_metadata \
        "${repository}" \
        "${release_id}" \
        "${release_tag}" \
        "${expected_commit}" \
        false \
        "${state_root}/${prefix}-metadata.tsv"
    query_releases_with_tag \
        "${repository}" \
        "${release_tag}" \
        "${state_root}/${prefix}-list.json" \
        "${state_root}"
    if [[ "$(jq 'length' "${state_root}/${prefix}-list.json")" -ne 1 || \
          "$(jq -r '.[0].id' "${state_root}/${prefix}-list.json")" != "${release_id}" || \
          "$(jq -r '.[0].draft' "${state_root}/${prefix}-list.json")" != "false" ]]; then
        echo "error: published namespace does not contain exactly release ID ${release_id}" >&2
        return 1
    fi
    verify_published_release_tag \
        "${repository}" \
        "${release_id}" \
        "${release_tag}" \
        "${expected_commit}" \
        "${published_state_root}"
}

require_draft_state before-publish

# Reconcile the exact ID after PATCH even when gh reports a transport error.
# A verified final state is success; an unchanged, still-private draft may be
# retried once after all tag namespace preconditions are rechecked.
for attempt in 1 2; do
    patch_succeeded=false
    if gh api \
        --method PATCH \
        -F draft=false \
        -f make_latest="${make_latest}" \
        "repos/${repository}/releases/${release_id}" >/dev/null; then
        patch_succeeded=true
    fi

    reconciled_state="$(classify_exact_release "${state_root}/reconcile-${attempt}.json")"
    if [[ "${reconciled_state}" == "published" ]]; then
        require_published_state "published-${attempt}"
        echo "Published verified draft release ID ${release_id}: ${repository}@${release_tag}"
        exit 0
    fi
    if [[ "${reconciled_state}" != "draft" ]]; then
        echo "error: exact release ID ${release_id} entered an unexpected state after publish PATCH" >&2
        exit 1
    fi
    if [[ "${attempt}" -eq 2 ]]; then
        echo "error: publish PATCH did not apply after one safe retry (last response success=${patch_succeeded})" >&2
        exit 1
    fi
    require_draft_state before-publish-retry
done
