#!/usr/bin/env bash

set -euo pipefail

if [[ "$#" -ne 5 ]]; then
    echo "usage: $0 <owner/repository> <release-tag> <release-name> <expected-commit> <artifact-directory>" >&2
    exit 64
fi

readonly repository="$1"
readonly release_tag="$2"
readonly release_name="$3"
readonly expected_commit="$4"
readonly artifact_directory="$5"
readonly script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# shellcheck source=Scripts/lib/github-release.sh
source "${script_directory}/lib/github-release.sh"

validate_github_repository "${repository}"
validate_release_tag "${release_tag}"
validate_commit_sha "${expected_commit}"
if [[ -z "${release_name}" ]]; then
    echo "error: release name must not be empty" >&2
    exit 64
fi
if [[ ! -d "${artifact_directory}" ]]; then
    echo "error: artifact directory does not exist: ${artifact_directory}" >&2
    exit 64
fi
if [[ -z "${GH_TOKEN:-}" ]]; then
    echo "error: GH_TOKEN is required" >&2
    exit 64
fi
if [[ -z "${GITHUB_OUTPUT:-}" ]]; then
    echo "error: GITHUB_OUTPUT is required to preserve the newly-created release identity" >&2
    exit 64
fi
if ! : >>"${GITHUB_OUTPUT}"; then
    echo "error: GITHUB_OUTPUT is not writable: ${GITHUB_OUTPUT}" >&2
    exit 64
fi
for asset in "${FORGERULES_RELEASE_ASSETS[@]}"; do
    if [[ ! -f "${artifact_directory}/${asset}" ]]; then
        echo "error: release asset does not exist: ${artifact_directory}/${asset}" >&2
        exit 1
    fi
done

readonly state_root="$(mktemp -d /tmp/forgerules-create-draft.XXXXXX)"
created_release_id=""
cleanup() {
    local status="$?"
    trap - EXIT
    if [[ "${status}" -ne 0 && -n "${created_release_id}" ]]; then
        if ! "${script_directory}/cleanup-failed-draft-release.sh" \
            "${repository}" \
            "${created_release_id}" \
            "${release_tag}" \
            "${expected_commit}"; then
            echo "error: automatic cleanup refused or failed for new release ID ${created_release_id}; inspect that exact ID manually" >&2
        fi
    fi
    rm -rf "${state_root}"
    exit "${status}"
}
trap cleanup EXIT

readonly curl_headers="${state_root}/curl-headers.txt"
(
    umask 077
    printf 'Authorization: Bearer %s\n' "${GH_TOKEN}"
    printf 'Accept: application/vnd.github+json\n'
    printf 'X-GitHub-Api-Version: 2022-11-28\n'
) >"${curl_headers}"

readonly create_request="${state_root}/create-request.json"
readonly create_response="${state_root}/create-response.json"
jq -n \
    --arg tag "${release_tag}" \
    --arg name "${release_name}" \
    --arg target "${expected_commit}" \
    '{
        tag_name: $tag,
        target_commitish: $target,
        name: $name,
        body: "",
        draft: true,
        prerelease: false,
        make_latest: "false"
    }' >"${create_request}"

create_status="$(curl \
    --location \
    --silent \
    --show-error \
    --output "${create_response}" \
    --write-out '%{http_code}' \
    --request POST \
    --header "@${curl_headers}" \
    --header 'Content-Type: application/json' \
    --data-binary "@${create_request}" \
    "https://api.github.com/repos/${repository}/releases")"
if [[ "${create_status}" != "201" ]]; then
    echo "error: create draft release returned HTTP ${create_status}" >&2
    jq -c '{message, errors}' "${create_response}" >&2 || true
    exit 1
fi

release_id="$(jq -er '
    select((.id | type) == "number" and (.id | floor) == .id)
    | .id
    | tostring
' "${create_response}")"
validate_release_id "${release_id}"
created_release_id="${release_id}"

# Record the ID as soon as the successful HTTP 201 response yields it. Even if
# a later response-identity or upload check fails, workflow cleanup can inspect
# only this exact release instead of rediscovering anything by tag.
printf 'id=%s\n' "${release_id}" >>"${GITHUB_OUTPUT}"

if ! jq -e \
    --argjson id "${release_id}" \
    --arg tag "${release_tag}" \
    --arg target "${expected_commit}" \
    '.id == $id and .tag_name == $tag and .target_commitish == $target and .draft == true' \
    "${create_response}" >/dev/null; then
    echo "error: create response does not identify the requested draft" >&2
    exit 1
fi

require_expected_release_metadata \
    "${repository}" \
    "${release_id}" \
    "${release_tag}" \
    "${expected_commit}" \
    true \
    "${state_root}/release-metadata.tsv"

readonly upload_root="https://uploads.github.com/repos/${repository}/releases/${release_id}/assets"
asset_index=0
for asset in "${FORGERULES_RELEASE_ASSETS[@]}"; do
    upload_response="${state_root}/upload-${asset_index}.json"
    upload_status="$(curl \
        --location \
        --silent \
        --show-error \
        --output "${upload_response}" \
        --write-out '%{http_code}' \
        --request POST \
        --header "@${curl_headers}" \
        --header 'Content-Type: application/octet-stream' \
        --data-binary "@${artifact_directory}/${asset}" \
        "${upload_root}?name=${asset}")"
    if [[ "${upload_status}" != "201" ]]; then
        echo "error: upload release asset ${asset} returned HTTP ${upload_status}" >&2
        jq -c '{message, errors}' "${upload_response}" >&2 || true
        exit 1
    fi
    if ! jq -e \
        --arg name "${asset}" \
        '.name == $name and .state == "uploaded" and (.id | type) == "number"' \
        "${upload_response}" >/dev/null; then
        echo "error: upload response does not identify the requested asset: ${asset}" >&2
        exit 1
    fi
    asset_index="$((asset_index + 1))"
done

echo "Created draft release ID ${release_id}: ${repository}@${release_tag}"
