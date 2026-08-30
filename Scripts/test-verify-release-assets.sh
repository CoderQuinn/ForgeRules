#!/usr/bin/env bash

set -euo pipefail

readonly script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly verifier="${script_directory}/verify-release-assets.sh"
readonly test_root="$(mktemp -d /tmp/forgerules-release-assets-test.XXXXXX)"
readonly artifact_directory="${test_root}/artifacts"
readonly fake_bin="${test_root}/bin"

cleanup() {
    rm -rf "${test_root}"
}
trap cleanup EXIT

fail() {
    echo "release asset verifier test failed: $*" >&2
    exit 1
}

mkdir "${artifact_directory}" "${fake_bin}"
for asset in \
    loyalsoldier_geoip.mmdb \
    loyalsoldier_geosite.json \
    official_geoip.mmdb \
    official_geosite.json \
    rules-manifest.json \
    rules.sources.lock.json; do
    printf 'fixture:%s\n' "${asset}" >"${artifact_directory}/${asset}"
done
(
    cd "${artifact_directory}"
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum \
            loyalsoldier_geoip.mmdb \
            loyalsoldier_geosite.json \
            official_geoip.mmdb \
            official_geosite.json \
            rules-manifest.json \
            rules.sources.lock.json >SHA256SUMS
    else
        shasum -a 256 \
            loyalsoldier_geoip.mmdb \
            loyalsoldier_geosite.json \
            official_geoip.mmdb \
            official_geosite.json \
            rules-manifest.json \
            rules.sources.lock.json >SHA256SUMS
    fi
)

cp "${script_directory}/fixtures/fake-gh-release.sh" "${fake_bin}/gh"
chmod +x "${fake_bin}/gh"

PATH="${fake_bin}:${PATH}" \
    FORGERULES_TEST_ARTIFACTS="${artifact_directory}" \
    "${verifier}" CoderQuinn/ForgeRules rules-20260830 "${artifact_directory}" >/dev/null

if PATH="${fake_bin}:${PATH}" \
    FORGERULES_TEST_ARTIFACTS="${artifact_directory}" \
    FORGERULES_TEST_OMIT_ASSET="official_geoip.mmdb" \
    "${verifier}" CoderQuinn/ForgeRules rules-20260830 "${artifact_directory}" >/dev/null 2>&1; then
    fail "incomplete published asset set was accepted"
fi

if PATH="${fake_bin}:${PATH}" \
    FORGERULES_TEST_ARTIFACTS="${artifact_directory}" \
    FORGERULES_TEST_CORRUPT_ASSET="official_geoip.mmdb" \
    "${verifier}" CoderQuinn/ForgeRules rules-20260830 "${artifact_directory}" >/dev/null 2>&1; then
    fail "published bytes differing from build output were accepted"
fi

echo "Release asset verifier tests passed"
