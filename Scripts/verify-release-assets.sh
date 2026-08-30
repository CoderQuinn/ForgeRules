#!/usr/bin/env bash

set -euo pipefail

if [[ "$#" -ne 3 ]]; then
    echo "usage: $0 <owner/repository> <release-tag> <artifact-directory>" >&2
    exit 64
fi

readonly repository="$1"
readonly release_tag="$2"
readonly artifact_directory="$3"

if [[ ! "${repository}" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]]; then
    echo "error: repository must be owner/name" >&2
    exit 64
fi
if [[ -z "${release_tag}" ]]; then
    echo "error: release tag is empty" >&2
    exit 64
fi
if [[ ! -d "${artifact_directory}" ]]; then
    echo "error: artifact directory does not exist: ${artifact_directory}" >&2
    exit 64
fi

readonly expected_assets=(
    "SHA256SUMS"
    "loyalsoldier_geoip.mmdb"
    "loyalsoldier_geosite.json"
    "official_geoip.mmdb"
    "official_geosite.json"
    "rules-manifest.json"
    "rules.sources.lock.json"
)

readonly verification_root="$(mktemp -d /tmp/forgerules-release-verify.XXXXXX)"
cleanup() {
    rm -rf "${verification_root}"
}
trap cleanup EXIT

readonly expected_list="${verification_root}/expected-assets.txt"
readonly actual_list="${verification_root}/actual-assets.txt"
printf '%s\n' "${expected_assets[@]}" | LC_ALL=C sort >"${expected_list}"
gh release view "${release_tag}" \
    --repo "${repository}" \
    --json assets \
    --jq '.assets[].name' | LC_ALL=C sort >"${actual_list}"
if ! diff -u "${expected_list}" "${actual_list}"; then
    echo "error: published release asset set is incomplete or unexpected" >&2
    exit 1
fi

readonly download_directory="${verification_root}/download"
mkdir "${download_directory}"
gh release download "${release_tag}" \
    --repo "${repository}" \
    --dir "${download_directory}"

for asset in "${expected_assets[@]}"; do
    if ! cmp -s \
        "${artifact_directory}/${asset}" \
        "${download_directory}/${asset}"; then
        echo "error: published asset differs from the verified build output: ${asset}" >&2
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

echo "Verified published release assets: ${repository}@${release_tag}"
