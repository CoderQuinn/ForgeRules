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

query_latest_list() {
    local output_file="$1"
    local _prefix="$2"
    query_releases_with_tag \
        "${repository}" \
        "${release_tag}" \
        "${output_file}" \
        "${state_root}"
}

release_exists=false
if optional_query \
    "repos/${repository}/releases/tags/${release_tag}" \
    "${state_root}/release-snapshot.json"; then
    release_exists=true
else
    query_status="$?"
    if [[ "${query_status}" -ne 4 ]]; then
        exit "${query_status}"
    fi
fi

tag_exists=false
if optional_query \
    "repos/${repository}/git/ref/tags/${release_tag}" \
    "${state_root}/tag-snapshot.json"; then
    tag_exists=true
else
    query_status="$?"
    if [[ "${query_status}" -ne 4 ]]; then
        exit "${query_status}"
    fi
fi

query_latest_list "${state_root}/release-list-snapshot.json" snapshot
readonly listed_release_count="$(jq 'length' "${state_root}/release-list-snapshot.json")"

# A mutable latest replacement is safe only from an entirely absent namespace
# or a complete published release/ref pair. Single-sided state and hidden draft
# collisions require manual intervention.
if [[ "${listed_release_count}" -eq 0 && \
      "${release_exists}" != "true" && \
      "${tag_exists}" != "true" ]]; then
    query_latest_list "${state_root}/release-list-absence-recheck.json" absence-recheck
    if [[ "$(jq 'length' "${state_root}/release-list-absence-recheck.json")" -ne 0 ]]; then
        echo "error: latest release appeared after the absence snapshot" >&2
        exit 1
    fi
    if optional_query \
        "repos/${repository}/releases/tags/${release_tag}" \
        "${state_root}/release-absence-recheck.json"; then
        echo "error: latest release appeared after the absence snapshot" >&2
        exit 1
    else
        query_status="$?"
        if [[ "${query_status}" -ne 4 ]]; then
            exit "${query_status}"
        fi
    fi
    if optional_query \
        "repos/${repository}/git/ref/tags/${release_tag}" \
        "${state_root}/tag-absence-recheck.json"; then
        echo "error: latest tag appeared after the absence snapshot" >&2
        exit 1
    else
        query_status="$?"
        if [[ "${query_status}" -ne 4 ]]; then
            exit "${query_status}"
        fi
    fi
    echo "Previous latest release state is already absent after identity rechecks: ${repository}"
    exit 0
fi

if [[ "${listed_release_count}" -ne 1 || \
      "${release_exists}" != "true" || \
      "${tag_exists}" != "true" ]]; then
    echo "error: refusing to mutate asymmetric or ambiguous latest state (listed=${listed_release_count}, release=${release_exists}, tag=${tag_exists})" >&2
    exit 1
fi

readonly release_id="$(jq -r '.id' "${state_root}/release-snapshot.json")"
readonly release_tag_name="$(jq -r '.tag_name' "${state_root}/release-snapshot.json")"
readonly release_draft="$(jq -r '.draft' "${state_root}/release-snapshot.json")"
readonly release_immutable="$(jq -r '.immutable // false' "${state_root}/release-snapshot.json")"
validate_release_id "${release_id}"
if [[ "${release_tag_name}" != "${release_tag}" || "${release_draft}" != "false" ]]; then
    echo "error: latest release lookup did not return a published latest release" >&2
    exit 1
fi
if [[ "${release_immutable}" == "true" ]]; then
    echo "error: immutable latest release cannot participate in mutable alias replacement" >&2
    exit 1
fi
if [[ "$(jq -r '.[0].id' "${state_root}/release-list-snapshot.json")" != "${release_id}" || \
      "$(jq -r '.[0].draft' "${state_root}/release-list-snapshot.json")" != "false" ]]; then
    echo "error: authenticated release listing disagrees with release-by-tag identity" >&2
    exit 1
fi

readonly original_release_fingerprint="$(release_fingerprint "${state_root}/release-snapshot.json")"
readonly original_tag_fingerprint="$(exact_tag_ref_fingerprint "${state_root}/tag-snapshot.json")"

# Re-read both objects before the first mutation. GitHub offers no conditional
# DELETE for releases or refs, so this narrows but cannot eliminate an external
# writer race; the operations guide documents that residual.
query_latest_list "${state_root}/release-list-recheck.json" recheck
if [[ "$(jq 'length' "${state_root}/release-list-recheck.json")" -ne 1 || \
      "$(jq -r '.[0].id' "${state_root}/release-list-recheck.json")" != "${release_id}" ]]; then
    echo "error: latest release listing changed before deletion" >&2
    exit 1
fi
if optional_query \
    "repos/${repository}/releases/tags/${release_tag}" \
    "${state_root}/release-recheck.json"; then
    if [[ "$(release_fingerprint "${state_root}/release-recheck.json")" != "${original_release_fingerprint}" ]]; then
        echo "error: latest release identity changed before deletion" >&2
        exit 1
    fi
else
    query_status="$?"
    if [[ "${query_status}" -eq 4 ]]; then
        echo "error: latest release disappeared before exact-ID deletion" >&2
        exit 1
    fi
    exit "${query_status}"
fi
if optional_query \
    "repos/${repository}/git/ref/tags/${release_tag}" \
    "${state_root}/tag-recheck-before-delete.json"; then
    if [[ "$(exact_tag_ref_fingerprint "${state_root}/tag-recheck-before-delete.json")" != "${original_tag_fingerprint}" ]]; then
        echo "error: latest tag object changed before deletion" >&2
        exit 1
    fi
else
    query_status="$?"
    if [[ "${query_status}" -eq 4 ]]; then
        echo "error: latest tag disappeared before deletion" >&2
        exit 1
    fi
    exit "${query_status}"
fi

# Reconcile ambiguous DELETE responses by exact ID. If the old object is still
# byte-for-byte the snapshot, retry once after the release/ref pair is rechecked.
for attempt in 1 2; do
    gh api \
        --method DELETE \
        "repos/${repository}/releases/${release_id}" >/dev/null || true
    if optional_query \
        "repos/${repository}/releases/${release_id}" \
        "${state_root}/release-after-delete-${attempt}.json"; then
        if [[ "$(release_fingerprint "${state_root}/release-after-delete-${attempt}.json")" != "${original_release_fingerprint}" ]]; then
            echo "error: exact latest release changed after an ambiguous delete; refusing retry" >&2
            exit 1
        fi
        if [[ "${attempt}" -eq 2 ]]; then
            echo "error: exact latest release ID ${release_id} survived one safe delete retry" >&2
            exit 1
        fi
        query_latest_list "${state_root}/release-list-delete-retry.json" delete-retry
        if [[ "$(jq 'length' "${state_root}/release-list-delete-retry.json")" -ne 1 || \
              "$(jq -r '.[0].id' "${state_root}/release-list-delete-retry.json")" != "${release_id}" ]]; then
            echo "error: latest release listing changed before delete retry" >&2
            exit 1
        fi
        if ! optional_query \
            "repos/${repository}/git/ref/tags/${release_tag}" \
            "${state_root}/tag-before-delete-retry.json"; then
            query_status="$?"
            echo "error: latest tag lookup failed before delete retry (status ${query_status})" >&2
            exit 1
        fi
        if [[ "$(exact_tag_ref_fingerprint "${state_root}/tag-before-delete-retry.json")" != "${original_tag_fingerprint}" ]]; then
            echo "error: latest tag changed before delete retry" >&2
            exit 1
        fi
    else
        query_status="$?"
        if [[ "${query_status}" -eq 4 ]]; then
            break
        fi
        exit "${query_status}"
    fi
done

query_latest_list "${state_root}/release-list-after-delete.json" after-delete
if [[ "$(jq 'length' "${state_root}/release-list-after-delete.json")" -ne 0 ]]; then
    echo "error: another latest release appeared after exact-ID deletion; preserving its tag" >&2
    exit 1
fi
if optional_query \
    "repos/${repository}/releases/tags/${release_tag}" \
    "${state_root}/release-by-tag-after-delete.json"; then
    echo "error: another latest release appeared before tag cleanup" >&2
    exit 1
else
    query_status="$?"
    if [[ "${query_status}" -ne 4 ]]; then
        exit "${query_status}"
    fi
fi

if optional_query \
    "repos/${repository}/git/ref/tags/${release_tag}" \
    "${state_root}/tag-after-release-delete.json"; then
    if [[ "$(exact_tag_ref_fingerprint "${state_root}/tag-after-release-delete.json")" != "${original_tag_fingerprint}" ]]; then
        echo "error: latest tag object changed after release deletion; preserving it" >&2
        exit 1
    fi
else
    query_status="$?"
    if [[ "${query_status}" -eq 4 ]]; then
        echo "Previous latest release ID ${release_id} removed; its tag was already absent"
        exit 0
    fi
    exit "${query_status}"
fi

for attempt in 1 2; do
    gh api \
        --method DELETE \
        "repos/${repository}/git/refs/tags/${release_tag}" >/dev/null || true
    if optional_query \
        "repos/${repository}/git/ref/tags/${release_tag}" \
        "${state_root}/tag-after-delete-${attempt}.json"; then
        if [[ "$(exact_tag_ref_fingerprint "${state_root}/tag-after-delete-${attempt}.json")" != "${original_tag_fingerprint}" ]]; then
            echo "error: latest tag changed after an ambiguous delete; preserving it" >&2
            exit 1
        fi
        if [[ "${attempt}" -eq 2 ]]; then
            echo "error: latest tag survived one safe delete retry" >&2
            exit 1
        fi
        query_latest_list "${state_root}/release-list-tag-retry.json" tag-retry
        if [[ "$(jq 'length' "${state_root}/release-list-tag-retry.json")" -ne 0 ]]; then
            echo "error: latest release appeared before tag delete retry" >&2
            exit 1
        fi
    else
        query_status="$?"
        if [[ "${query_status}" -eq 4 ]]; then
            break
        fi
        exit "${query_status}"
    fi
done

query_latest_list "${state_root}/release-list-final.json" final
if [[ "$(jq 'length' "${state_root}/release-list-final.json")" -ne 0 ]]; then
    echo "error: latest release namespace is not empty after deletion" >&2
    exit 1
fi
if optional_query \
    "repos/${repository}/git/ref/tags/${release_tag}" \
    "${state_root}/tag-final.json"; then
    echo "error: latest tag still exists after deletion" >&2
    exit 1
else
    query_status="$?"
    if [[ "${query_status}" -ne 4 ]]; then
        exit "${query_status}"
    fi
fi

echo "Previous latest release ID ${release_id} and its snapshotted tag were removed: ${repository}"
