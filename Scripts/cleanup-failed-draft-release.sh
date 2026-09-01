#!/usr/bin/env bash

set -euo pipefail

if [[ "$#" -lt 4 ]]; then
    echo "usage: $0 <owner/repository> <release-id> <expected-tag> <expected-commit> [<alternate-allowed-tag> ...]" >&2
    exit 64
fi

readonly repository="$1"
readonly release_id="$2"
readonly expected_tag="$3"
readonly expected_commit="$4"
shift 4
readonly allowed_tags=("${expected_tag}" "$@")
readonly script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# shellcheck source=Scripts/lib/github-release.sh
source "${script_directory}/lib/github-release.sh"

validate_github_repository "${repository}"
validate_release_id "${release_id}"
validate_commit_sha "${expected_commit}"
for allowed_tag in "${allowed_tags[@]}"; do
    validate_release_tag "${allowed_tag}"
done

readonly state_root="$(mktemp -d /tmp/forgerules-cleanup-release.XXXXXX)"
cleanup() {
    rm -rf "${state_root}"
}
trap cleanup EXIT

tag_is_allowed() {
    local candidate="$1"
    local allowed
    for allowed in "${allowed_tags[@]}"; do
        if [[ "${candidate}" == "${allowed}" ]]; then
            return 0
        fi
    done
    return 1
}

optional_query() {
    local endpoint="$1"
    local output_file="$2"
    if github_api_optional "${endpoint}" "${output_file}"; then
        return 0
    else
        local status="$?"
        if [[ "${status}" -eq 4 ]]; then
            return 4
        fi
        return "${status}"
    fi
}

release_fingerprint() {
    jq -cer '[
        .id,
        .node_id,
        .tag_name,
        .name,
        .draft,
        .prerelease,
        .immutable,
        .target_commitish,
        .updated_at
    ]' "$1"
}

confirm_release_absent() {
    local output_file="$1"
    if optional_query "repos/${repository}/releases/${release_id}" "${output_file}"; then
        echo "error: exact release ID ${release_id} still exists after deletion" >&2
        return 1
    else
        local status="$?"
        [[ "${status}" -eq 4 ]]
    fi
}

confirm_tag_absent() {
    local tag="$1"
    local output_file="$2"
    if optional_query "repos/${repository}/git/ref/tags/${tag}" "${output_file}"; then
        echo "error: exact tag ${tag} still exists after deletion" >&2
        return 1
    else
        local status="$?"
        [[ "${status}" -eq 4 ]]
    fi
}

release_exists=false
if optional_query \
    "repos/${repository}/releases/${release_id}" \
    "${state_root}/release.json"; then
    release_exists=true
else
    query_status="$?"
    if [[ "${query_status}" -ne 4 ]]; then
        exit "${query_status}"
    fi
fi

if [[ "${release_exists}" != "true" ]]; then
    # The exact fresh ID is the ownership proof. If it is gone, never guess
    # ownership from a matching tag or commit; report every surviving namespace
    # object for manual recovery instead.
    remnants=false
    for allowed_tag in "${allowed_tags[@]}"; do
        query_releases_with_tag \
            "${repository}" \
            "${allowed_tag}" \
            "${state_root}/absent-id-releases-${allowed_tag}.json" \
            "${state_root}"
        if [[ "$(jq 'length' "${state_root}/absent-id-releases-${allowed_tag}.json")" -ne 0 ]]; then
            echo "error: exact release ID ${release_id} is absent while release tag ${allowed_tag} remains" >&2
            remnants=true
        fi
        if optional_query \
            "repos/${repository}/git/ref/tags/${allowed_tag}" \
            "${state_root}/absent-id-ref-${allowed_tag}.json"; then
            echo "error: exact release ID ${release_id} is absent while tag ref ${allowed_tag} remains" >&2
            remnants=true
        else
            query_status="$?"
            if [[ "${query_status}" -ne 4 ]]; then
                exit "${query_status}"
            fi
        fi
    done
    if [[ "${remnants}" == "true" ]]; then
        exit 1
    fi
    echo "Failed release ID ${release_id} is already absent and no allowed tag remains"
    exit 0
fi

actual_id="$(jq -r '.id' "${state_root}/release.json")"
actual_tag="$(jq -r '.tag_name' "${state_root}/release.json")"
actual_draft="$(jq -r '.draft' "${state_root}/release.json")"
target_commitish="$(jq -r '.target_commitish' "${state_root}/release.json")"
if [[ "${actual_id}" != "${release_id}" ]]; then
    echo "error: refusing cleanup of unexpected release ID ${actual_id:-missing}" >&2
    exit 1
fi
if ! tag_is_allowed "${actual_tag}"; then
    echo "error: refusing cleanup of release tag ${actual_tag:-missing}; allowed tags: ${allowed_tags[*]}" >&2
    exit 1
fi
if [[ "${actual_draft}" != "true" && "${actual_draft}" != "false" ]]; then
    echo "error: refusing cleanup of release with invalid draft state ${actual_draft:-missing}" >&2
    exit 1
fi
if [[ "${target_commitish}" != "${expected_commit}" ]]; then
    echo "error: refusing cleanup of release target ${target_commitish}, want ${expected_commit}" >&2
    exit 1
fi
readonly original_release_fingerprint="$(release_fingerprint "${state_root}/release.json")"

tag_delete_authorized=false
published_association_valid=true
published_ref_exists=false
published_tag_fingerprint=""
if [[ "${actual_draft}" == "false" ]]; then
    if query_releases_with_tag \
        "${repository}" \
        "${actual_tag}" \
        "${state_root}/published-tag-list.json" \
        "${state_root}"; then
        if [[ "$(jq 'length' "${state_root}/published-tag-list.json")" -ne 1 || \
              "$(jq -r '.[0].id' "${state_root}/published-tag-list.json")" != "${release_id}" ]]; then
            echo "warning: published tag ${actual_tag} is not uniquely owned by release ID ${release_id}; its ref will be preserved" >&2
            published_association_valid=false
        fi
    else
        echo "warning: unable to list published tag ${actual_tag}; exact release will still be withdrawn and its ref preserved" >&2
        published_association_valid=false
    fi

    if optional_query \
        "repos/${repository}/releases/tags/${actual_tag}" \
        "${state_root}/published-release-by-tag.json"; then
        if [[ "$(jq -r '.id' "${state_root}/published-release-by-tag.json")" != "${release_id}" ]]; then
            echo "warning: release-by-tag ${actual_tag} belongs to another ID; its ref will be preserved" >&2
            published_association_valid=false
        fi
    else
        query_status="$?"
        if [[ "${query_status}" -eq 4 ]]; then
            echo "warning: published release ${release_id} is not reachable by tag ${actual_tag}; its ref will be preserved" >&2
        else
            echo "warning: unable to verify release-by-tag ${actual_tag}; exact release will still be withdrawn and its ref preserved" >&2
        fi
        published_association_valid=false
    fi

    if optional_query \
        "repos/${repository}/git/ref/tags/${actual_tag}" \
        "${state_root}/published-tag-ref.json"; then
        published_ref_exists=true
        if published_tag_fingerprint="$(exact_tag_ref_fingerprint "${state_root}/published-tag-ref.json")" && \
           published_commit="$(peel_exact_tag_ref_to_commit \
            "${repository}" \
            "${state_root}/published-tag-ref.json" \
            "${state_root}")"; then
            if [[ "${published_commit}" != "${expected_commit}" ]]; then
                echo "warning: published tag ${actual_tag} resolves to ${published_commit}, not ${expected_commit}; its ref will be preserved" >&2
                published_association_valid=false
            fi
        else
            echo "warning: unable to fingerprint or peel published tag ${actual_tag}; exact release will still be withdrawn and its ref preserved" >&2
            published_association_valid=false
        fi
    else
        query_status="$?"
        if [[ "${query_status}" -ne 4 ]]; then
            echo "warning: unable to query published tag ref ${actual_tag}; exact release will still be withdrawn and its ref preserved" >&2
            published_association_valid=false
        fi
    fi
    if [[ "${published_association_valid}" == "true" && "${published_ref_exists}" == "true" ]]; then
        tag_delete_authorized=true
    fi
fi

# Re-read the exact release immediately before the first mutation. The ID is
# owned by this workflow, but a changed tag/target/draft fingerprint is not.
if optional_query \
    "repos/${repository}/releases/${release_id}" \
    "${state_root}/release-recheck.json"; then
    :
else
    query_status="$?"
    if [[ "${query_status}" -eq 4 ]]; then
        echo "error: exact release ID ${release_id} disappeared before cleanup" >&2
        exit 1
    fi
    exit "${query_status}"
fi
if [[ "$(release_fingerprint "${state_root}/release-recheck.json")" != "${original_release_fingerprint}" ]]; then
    echo "error: exact release ID ${release_id} changed before cleanup" >&2
    exit 1
fi

if [[ "${actual_draft}" == "false" ]]; then
    if query_releases_with_tag \
        "${repository}" \
        "${actual_tag}" \
        "${state_root}/published-tag-list-recheck.json" \
        "${state_root}"; then
        if [[ "$(jq 'length' "${state_root}/published-tag-list-recheck.json")" -ne 1 || \
              "$(jq -r '.[0].id' "${state_root}/published-tag-list-recheck.json")" != "${release_id}" ]]; then
            echo "warning: published tag listing changed before exact-ID cleanup; its ref will be preserved" >&2
            published_association_valid=false
            tag_delete_authorized=false
        fi
    else
        echo "warning: unable to recheck published tag listing; exact release will still be withdrawn and its ref preserved" >&2
        published_association_valid=false
        tag_delete_authorized=false
    fi
    if optional_query \
        "repos/${repository}/releases/tags/${actual_tag}" \
        "${state_root}/published-release-by-tag-recheck.json"; then
        if [[ "$(jq -r '.id' "${state_root}/published-release-by-tag-recheck.json")" != "${release_id}" ]]; then
            echo "warning: release-by-tag identity changed before exact-ID cleanup; its ref will be preserved" >&2
            published_association_valid=false
            tag_delete_authorized=false
        fi
    else
        query_status="$?"
        if [[ "${query_status}" -eq 4 ]]; then
            echo "warning: release-by-tag disappeared before exact-ID cleanup; its ref will be preserved" >&2
        else
            echo "warning: unable to recheck release-by-tag; exact release will still be withdrawn and its ref preserved" >&2
        fi
        published_association_valid=false
        tag_delete_authorized=false
    fi
    if optional_query \
        "repos/${repository}/git/ref/tags/${actual_tag}" \
        "${state_root}/published-tag-ref-before-delete.json"; then
        if before_delete_fingerprint="$(exact_tag_ref_fingerprint "${state_root}/published-tag-ref-before-delete.json")" && \
           before_delete_commit="$(peel_exact_tag_ref_to_commit \
            "${repository}" \
            "${state_root}/published-tag-ref-before-delete.json" \
            "${state_root}")"; then
            if [[ "${published_ref_exists}" != "true" || \
                  "${before_delete_fingerprint}" != "${published_tag_fingerprint}" || \
                  "${before_delete_commit}" != "${expected_commit}" ]]; then
                echo "warning: published tag ref changed before exact-ID cleanup; it will be preserved" >&2
                published_association_valid=false
                tag_delete_authorized=false
            fi
        else
            echo "warning: unable to recheck published tag identity; exact release will still be withdrawn and its ref preserved" >&2
            published_association_valid=false
            tag_delete_authorized=false
        fi
    else
        query_status="$?"
        if [[ "${query_status}" -ne 4 ]]; then
            echo "warning: unable to recheck published tag ref; exact release will still be withdrawn and its ref preserved" >&2
            published_association_valid=false
            tag_delete_authorized=false
        fi
        if [[ "${published_ref_exists}" == "true" ]]; then
            echo "warning: published tag ref disappeared before exact-ID cleanup" >&2
            published_association_valid=false
            tag_delete_authorized=false
        fi
    fi
fi

# Always withdraw the exact fresh release first. This is safe for either a
# draft or an ambiguously published release and never relies on tag discovery.
if ! gh api \
    --method DELETE \
    "repos/${repository}/releases/${release_id}"; then
    if ! confirm_release_absent "${state_root}/release-after-ambiguous-delete.json"; then
        echo "error: unable to confirm deletion of exact release ID ${release_id}" >&2
        exit 1
    fi
fi
confirm_release_absent "${state_root}/release-after-delete.json"

query_releases_with_tag \
    "${repository}" \
    "${actual_tag}" \
    "${state_root}/release-list-after-delete.json" \
    "${state_root}"
if [[ "$(jq 'length' "${state_root}/release-list-after-delete.json")" -ne 0 ]]; then
    echo "error: exact release was removed, but another release now uses tag ${actual_tag}; preserving its ref" >&2
    exit 1
fi

if [[ "${actual_draft}" == "true" ]]; then
    # A draft has no public tag association that can prove ownership. Never
    # infer it from a matching SHA.
    if optional_query \
        "repos/${repository}/git/ref/tags/${actual_tag}" \
        "${state_root}/draft-tag-after-delete.json"; then
        echo "error: exact draft was removed, but unassociated tag ref ${actual_tag} was preserved for manual review" >&2
        exit 1
    else
        query_status="$?"
        if [[ "${query_status}" -ne 4 ]]; then
            exit "${query_status}"
        fi
    fi
    echo "Cleaned failed draft release ID ${release_id} (${actual_tag})"
    exit 0
fi

if [[ "${published_association_valid}" != "true" ]]; then
    echo "error: exact published release was removed, but tag ${actual_tag} lacked a stable exact-ID association and was preserved" >&2
    exit 1
fi

if [[ "${published_ref_exists}" != "true" ]]; then
    if optional_query \
        "repos/${repository}/git/ref/tags/${actual_tag}" \
        "${state_root}/new-tag-after-release-delete.json"; then
        echo "error: exact published release was removed, but a new tag ${actual_tag} appeared and was preserved" >&2
        exit 1
    else
        query_status="$?"
        if [[ "${query_status}" -ne 4 ]]; then
            exit "${query_status}"
        fi
    fi
    echo "Cleaned failed published release ID ${release_id}; tag ${actual_tag} was already absent"
    exit 0
fi

if optional_query \
    "repos/${repository}/git/ref/tags/${actual_tag}" \
    "${state_root}/published-tag-recheck.json"; then
    rechecked_tag_fingerprint="$(exact_tag_ref_fingerprint "${state_root}/published-tag-recheck.json")"
    rechecked_commit="$(peel_exact_tag_ref_to_commit \
        "${repository}" \
        "${state_root}/published-tag-recheck.json" \
        "${state_root}")"
    if [[ "${rechecked_tag_fingerprint}" != "${published_tag_fingerprint}" || \
          "${rechecked_commit}" != "${expected_commit}" ]]; then
        echo "error: exact published release was removed, but tag ${actual_tag} changed and was preserved" >&2
        exit 1
    fi
else
    query_status="$?"
    if [[ "${query_status}" -eq 4 ]]; then
        echo "Cleaned failed published release ID ${release_id}; tag ${actual_tag} was already absent"
        exit 0
    fi
    exit "${query_status}"
fi

if [[ "${tag_delete_authorized}" != "true" ]]; then
    echo "error: internal cleanup invariant rejected tag deletion" >&2
    exit 1
fi
if ! gh api \
    --method DELETE \
    "repos/${repository}/git/refs/tags/${actual_tag}"; then
    if ! confirm_tag_absent \
        "${actual_tag}" \
        "${state_root}/tag-after-ambiguous-delete.json"; then
        echo "error: unable to confirm deletion of exact tag ${actual_tag}" >&2
        exit 1
    fi
fi
confirm_tag_absent "${actual_tag}" "${state_root}/tag-after-delete.json"

echo "Cleaned failed published release ID ${release_id} and its verified tag ${actual_tag}"
