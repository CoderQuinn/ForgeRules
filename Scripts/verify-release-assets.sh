#!/usr/bin/env bash

set -euo pipefail

if [[ "$#" -ne 6 ]]; then
    echo "usage: $0 <owner/repository> <release-id> <release-tag> <expected-commit> <draft|published> <artifact-directory>" >&2
    exit 64
fi

readonly repository="$1"
readonly release_id="$2"
readonly release_tag="$3"
readonly expected_commit="$4"
readonly expected_state="$5"
readonly artifact_directory="$6"
readonly script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# shellcheck source=Scripts/lib/github-release.sh
source "${script_directory}/lib/github-release.sh"

validate_github_repository "${repository}"
validate_release_id "${release_id}"
validate_release_tag "${release_tag}"
validate_commit_sha "${expected_commit}"
case "${expected_state}" in
    draft)
        readonly expected_draft="true"
        ;;
    published)
        readonly expected_draft="false"
        ;;
    *)
        echo "error: release state must be draft or published" >&2
        exit 64
        ;;
esac
if [[ ! -d "${artifact_directory}" ]]; then
    echo "error: artifact directory does not exist: ${artifact_directory}" >&2
    exit 64
fi

readonly verification_root="$(mktemp -d /tmp/forgerules-release-verify.XXXXXX)"
cleanup() {
    rm -rf "${verification_root}"
}
trap cleanup EXIT

require_expected_release_metadata \
    "${repository}" \
    "${release_id}" \
    "${release_tag}" \
    "${expected_commit}" \
    "${expected_draft}" \
    "${verification_root}/release-metadata.tsv"
if [[ "${expected_state}" == "published" ]]; then
    verify_published_release_tag \
        "${repository}" \
        "${release_id}" \
        "${release_tag}" \
        "${expected_commit}" \
        "${verification_root}"
fi

readonly expected_list="${verification_root}/expected-assets.txt"
readonly actual_list="${verification_root}/actual-assets.txt"
readonly assets_json="${verification_root}/assets.json"
printf '%s\n' "${FORGERULES_RELEASE_ASSETS[@]}" | LC_ALL=C sort >"${expected_list}"
gh api \
    --paginate \
    "repos/${repository}/releases/${release_id}/assets?per_page=100" >"${assets_json}"
jq -r '.[].name' "${assets_json}" | LC_ALL=C sort >"${actual_list}"
if ! diff -u "${expected_list}" "${actual_list}"; then
    echo "error: release asset set is incomplete or unexpected" >&2
    exit 1
fi

readonly download_directory="${verification_root}/download"
mkdir "${download_directory}"
for asset in "${FORGERULES_RELEASE_ASSETS[@]}"; do
    asset_id="$(jq -r --arg name "${asset}" '.[] | select(.name == $name) | .id' "${assets_json}")"
    if [[ ! "${asset_id}" =~ ^[0-9]+$ ]]; then
        echo "error: release asset has no numeric API ID: ${asset}" >&2
        exit 1
    fi
    gh api \
        -H "Accept: application/octet-stream" \
        "repos/${repository}/releases/assets/${asset_id}" >"${download_directory}/${asset}"
    if ! cmp -s \
        "${artifact_directory}/${asset}" \
        "${download_directory}/${asset}"; then
        echo "error: remote asset differs from the verified build output: ${asset}" >&2
        exit 1
    fi
done

(
    cd "${download_directory}"
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum -c SHA256SUMS
    else
        shasum -a 256 -c SHA256SUMS
    fi
)

echo "Verified ${expected_state} release ID ${release_id}: ${repository}@${release_tag}"
