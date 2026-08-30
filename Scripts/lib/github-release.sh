#!/usr/bin/env bash

declare -ar FORGERULES_RELEASE_ASSETS=(
    "SHA256SUMS"
    "loyalsoldier_geoip.mmdb"
    "loyalsoldier_geosite.json"
    "official_geoip.mmdb"
    "official_geosite.json"
    "rules-manifest.json"
    "rules.sources.lock.json"
)

validate_github_repository() {
    local value="$1"
    if [[ ! "${value}" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]]; then
        echo "error: repository must be owner/name" >&2
        return 64
    fi
}

validate_release_tag() {
    local value="$1"
    if [[ ! "${value}" =~ ^(latest|rules-[0-9]{8}|latest-staging-[0-9]+-[0-9]+)$ ]]; then
        echo "error: release tag must be latest, rules-YYYYMMDD, or latest-staging-RUN-ATTEMPT" >&2
        return 64
    fi
}

validate_commit_sha() {
    local value="$1"
    if [[ ! "${value}" =~ ^[0-9a-f]{40}$ ]]; then
        echo "error: expected commit must be a lowercase 40-character SHA" >&2
        return 64
    fi
}

validate_release_id() {
    local value="$1"
    if [[ ! "${value}" =~ ^[0-9]+$ ]]; then
        echo "error: release ID must be numeric" >&2
        return 64
    fi
}

# Writes a successful GitHub API response to the requested file. A confirmed
# HTTP 404 returns 4 and leaves an empty output file. Authentication, network,
# and all other API failures remain fatal instead of being treated as absence.
github_api_optional() {
    local api_endpoint="$1"
    local api_output_file="$2"
    local api_error_file
    api_error_file="$(mktemp /tmp/forgerules-gh-api-error.XXXXXX)"

    if gh api "${api_endpoint}" >"${api_output_file}" 2>"${api_error_file}"; then
        rm -f "${api_error_file}"
        return 0
    fi
    if grep -Eq '\(HTTP 404\)$' "${api_error_file}"; then
        : >"${api_output_file}"
        rm -f "${api_error_file}"
        return 4
    fi

    cat "${api_error_file}" >&2
    rm -f "${api_error_file}"
    # Normalize every non-404 failure. In particular, gh uses exit status 4 for
    # authentication errors, so propagating it would collide with our private
    # confirmed-absence sentinel above.
    return 1
}

require_expected_release_metadata() {
    local github_repository="$1"
    local expected_release_id="$2"
    local expected_release_tag="$3"
    local expected_release_commit="$4"
    local expected_release_draft="$5"
    local release_metadata_file="$6"

    gh api \
        "repos/${github_repository}/releases/${expected_release_id}" \
        --jq '[.id, .tag_name, .draft, .target_commitish] | @tsv' >"${release_metadata_file}"

    local actual_id actual_tag actual_draft target_commitish
    IFS=$'\t' read -r actual_id actual_tag actual_draft target_commitish <"${release_metadata_file}"
    if [[ "${actual_id}" != "${expected_release_id}" || "${actual_tag}" != "${expected_release_tag}" ]]; then
        echo "error: release identity = ${actual_id:-missing}/${actual_tag:-missing}, want ${expected_release_id}/${expected_release_tag}" >&2
        return 1
    fi
    if [[ "${actual_draft}" != "${expected_release_draft}" ]]; then
        echo "error: release ${expected_release_tag} draft state = ${actual_draft:-missing}, want ${expected_release_draft}" >&2
        return 1
    fi
    if [[ "${target_commitish}" != "${expected_release_commit}" ]]; then
        echo "error: release ${expected_release_tag} target = ${target_commitish}, want ${expected_release_commit}" >&2
        return 1
    fi
}

peel_exact_tag_ref_to_commit() {
    local github_repository="$1"
    local exact_tag_ref_file="$2"
    local tag_object_state_root="$3"

    local object_metadata object_type object_sha
    if ! object_metadata="$(jq -er '[.object.type, .object.sha] | @tsv' "${exact_tag_ref_file}")"; then
        echo "error: exact tag ref has no object identity" >&2
        return 1
    fi
    IFS=$'\t' read -r object_type object_sha <<<"${object_metadata}"
    local depth=0
    while [[ "${object_type}" == "tag" ]]; do
        if ((depth >= 16)); then
            echo "error: annotated tag chain exceeds 16 objects" >&2
            return 1
        fi
        validate_commit_sha "${object_sha}"
        local tag_object_file="${tag_object_state_root}/tag-object-${depth}.json"
        gh api \
            "repos/${github_repository}/git/tags/${object_sha}" \
            >"${tag_object_file}"
        if ! object_metadata="$(jq -er '[.object.type, .object.sha] | @tsv' "${tag_object_file}")"; then
            echo "error: annotated tag object ${object_sha} has no target identity" >&2
            return 1
        fi
        IFS=$'\t' read -r object_type object_sha <<<"${object_metadata}"
        depth="$((depth + 1))"
    done
    if [[ "${object_type}" != "commit" ]]; then
        echo "error: exact tag ref terminates at unsupported object type ${object_type:-missing}" >&2
        return 1
    fi
    validate_commit_sha "${object_sha}"
    printf '%s\n' "${object_sha}"
}

exact_tag_ref_fingerprint() {
    local exact_tag_ref_file="$1"
    local identity object_type object_sha
    if ! identity="$(jq -er '[.object.type, .object.sha] | @tsv' "${exact_tag_ref_file}")"; then
        echo "error: exact tag ref has no object identity" >&2
        return 1
    fi
    IFS=$'\t' read -r object_type object_sha <<<"${identity}"
    if [[ "${object_type}" != "commit" && "${object_type}" != "tag" ]]; then
        echo "error: exact tag ref has unsupported object type ${object_type:-missing}" >&2
        return 1
    fi
    validate_commit_sha "${object_sha}"
    printf '%s:%s\n' "${object_type}" "${object_sha}"
}

query_releases_with_tag() {
    local github_repository="$1"
    local queried_release_tag="$2"
    local matching_releases_file="$3"
    local listing_state_root="$4"
    local pages_file="${listing_state_root}/release-pages-$(basename "${matching_releases_file}")"

    validate_release_tag "${queried_release_tag}"
    gh api \
        --paginate \
        --slurp \
        "repos/${github_repository}/releases?per_page=100" \
        >"${pages_file}"
    if ! jq -e \
        --arg tag "${queried_release_tag}" \
        '[.[][] | select(.tag_name == $tag)]' \
        "${pages_file}" >"${matching_releases_file}"; then
        echo "error: release listing is not a paginated array" >&2
        return 1
    fi
}

verify_published_release_tag() {
    local github_repository="$1"
    local expected_release_id="$2"
    local expected_release_tag="$3"
    local expected_release_commit="$4"
    local verification_state_root="$5"

    gh api \
        "repos/${github_repository}/releases/tags/${expected_release_tag}" \
        --jq '.id' >"${verification_state_root}/release-by-tag.txt"
    local tagged_release_id
    tagged_release_id="$(<"${verification_state_root}/release-by-tag.txt")"
    if [[ "${tagged_release_id}" != "${expected_release_id}" ]]; then
        echo "error: tag ${expected_release_tag} resolves to release ${tagged_release_id}, want ${expected_release_id}" >&2
        return 1
    fi

    if [[ "${expected_release_tag}" == "latest" ]]; then
        gh api \
            "repos/${github_repository}/releases/latest" \
            --jq '.id' >"${verification_state_root}/latest-release.txt"
        local latest_release_id
        latest_release_id="$(<"${verification_state_root}/latest-release.txt")"
        if [[ "${latest_release_id}" != "${expected_release_id}" ]]; then
            echo "error: GitHub latest release is ${latest_release_id}, want ${expected_release_id}" >&2
            return 1
        fi
    fi

    gh api \
        "repos/${github_repository}/git/ref/tags/${expected_release_tag}" \
        >"${verification_state_root}/tag-ref.json"

    local resolved_commit
    resolved_commit="$(peel_exact_tag_ref_to_commit \
        "${github_repository}" \
        "${verification_state_root}/tag-ref.json" \
        "${verification_state_root}")"
    if [[ "${resolved_commit}" != "${expected_release_commit}" ]]; then
        echo "error: tag ${expected_release_tag} resolves to ${resolved_commit}, want ${expected_release_commit}" >&2
        return 1
    fi
}
