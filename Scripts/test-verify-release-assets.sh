#!/usr/bin/env bash

set -euo pipefail

readonly script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly verifier="${script_directory}/verify-release-assets.sh"
readonly creator="${script_directory}/create-draft-release.sh"
readonly publisher="${script_directory}/publish-draft-release.sh"
readonly draft_cleanup="${script_directory}/cleanup-failed-draft-release.sh"
readonly latest_delete="${script_directory}/delete-latest-release.sh"
readonly test_root="$(mktemp -d /tmp/forgerules-release-assets-test.XXXXXX)"
readonly artifact_directory="${test_root}/artifacts"
readonly state_directory="${test_root}/state"
readonly fake_bin="${test_root}/bin"
readonly repository="CoderQuinn/ForgeRules"
readonly release_id="101"
readonly release_tag="rules-20260830"
readonly expected_commit="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

cleanup() {
    rm -rf "${test_root}"
}
trap cleanup EXIT

fail() {
    echo "release lifecycle test failed: $*" >&2
    exit 1
}

write_release_state() {
    local draft="$1"
    local tag="$2"
    local target="$3"
    local id="${4:-${release_id}}"
    jq -n \
        --argjson id "${id}" \
        --arg tag "${tag}" \
        --argjson draft "${draft}" \
        --arg target "${target}" \
        '{id: $id, tag_name: $tag, draft: $draft, target_commitish: $target}' \
        >"${state_directory}/release.json"
}

write_tag_state() {
    local target="$1"
    jq -n --arg sha "${target}" '{sha: $sha}' >"${state_directory}/tag.json"
}

write_annotated_tag_state() {
    local target="$1"
    local tag_object_sha="cccccccccccccccccccccccccccccccccccccccc"
    jq -n \
        --arg type tag \
        --arg sha "${tag_object_sha}" \
        '{type: $type, sha: $sha}' >"${state_directory}/tag.json"
    jq -n \
        --arg tag_sha "${tag_object_sha}" \
        --arg target "${target}" \
        '{tag_sha: $tag_sha, object: {type: "commit", sha: $target}}' \
        >"${state_directory}/annotated-tag.json"
}

reset_state() {
    rm -f \
        "${state_directory}/annotated-tag.json" \
        "${state_directory}/release.json" \
        "${state_directory}/tag.json" \
        "${state_directory}/uploads.txt"
}

run_with_fake_gh() {
    PATH="${fake_bin}:${PATH}" \
        FORGERULES_TEST_ARTIFACTS="${artifact_directory}" \
        FORGERULES_TEST_STATE_DIR="${state_directory}" \
        "$@"
}

mkdir "${artifact_directory}" "${state_directory}" "${fake_bin}"
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
cp "${script_directory}/fixtures/fake-curl-release.sh" "${fake_bin}/curl"
chmod +x "${fake_bin}/gh"
chmod +x "${fake_bin}/curl"

# Draft creation is a fresh REST POST. It records the returned ID before
# uploads and refuses to upsert an existing draft.
reset_state
github_output="${test_root}/github-output.txt"
: >"${github_output}"
PATH="${fake_bin}:${PATH}" \
    FORGERULES_TEST_ARTIFACTS="${artifact_directory}" \
    FORGERULES_TEST_STATE_DIR="${state_directory}" \
    GH_TOKEN=test-token \
    GITHUB_OUTPUT="${github_output}" \
    "${creator}" \
    "${repository}" \
    "${release_tag}" \
    "Test Release" \
    "${expected_commit}" \
    "${artifact_directory}" >/dev/null
if [[ "$(<"${github_output}")" != "id=${release_id}" ]]; then
    fail "fresh draft creation did not record the exact REST response ID"
fi
if [[ "$(wc -l <"${state_directory}/uploads.txt" | tr -d ' ')" -ne 7 ]]; then
    fail "fresh draft creation did not upload the exact asset count"
fi
if PATH="${fake_bin}:${PATH}" \
    FORGERULES_TEST_ARTIFACTS="${artifact_directory}" \
    FORGERULES_TEST_STATE_DIR="${state_directory}" \
    GH_TOKEN=test-token \
    GITHUB_OUTPUT="${github_output}" \
    "${creator}" \
    "${repository}" \
    "${release_tag}" \
    "Must Not Upsert" \
    "${expected_commit}" \
    "${artifact_directory}" >/dev/null 2>&1; then
    fail "draft creation upserted a pre-existing release instead of failing"
fi
if [[ "$(jq -r '.name' "${state_directory}/release.json")" != "Test Release" ]]; then
    fail "failed duplicate creation mutated the pre-existing draft"
fi

# A failed upload still exposes the fresh ID for exact cleanup.
reset_state
: >"${github_output}"
if PATH="${fake_bin}:${PATH}" \
    FORGERULES_TEST_ARTIFACTS="${artifact_directory}" \
    FORGERULES_TEST_STATE_DIR="${state_directory}" \
    FORGERULES_TEST_FAIL_UPLOAD_ASSET="official_geoip.mmdb" \
    GH_TOKEN=test-token \
    GITHUB_OUTPUT="${github_output}" \
    "${creator}" \
    "${repository}" \
    "${release_tag}" \
    "Upload Failure" \
    "${expected_commit}" \
    "${artifact_directory}" >/dev/null 2>&1; then
    fail "injected draft upload failure was accepted"
fi
if [[ "$(<"${github_output}")" != "id=${release_id}" ]]; then
    fail "failed upload did not preserve the newly-created release ID"
fi
if [[ -e "${state_directory}/release.json" ]]; then
    fail "failed upload did not automatically clean the exact fresh draft"
fi
run_with_fake_gh \
    "${draft_cleanup}" \
    "${repository}" \
    "${release_id}" \
    "${release_tag}" \
    "${expected_commit}" >/dev/null

# Draft verification uses the exact release ID and does not require a tag ref.
write_release_state true "${release_tag}" "${expected_commit}"
run_with_fake_gh \
    "${verifier}" \
    "${repository}" \
    "${release_id}" \
    "${release_tag}" \
    "${expected_commit}" \
    draft \
    "${artifact_directory}" >/dev/null

# Publishing patches that exact ID. Published verification additionally
# requires tag -> release ID and tag -> commit agreement.
run_with_fake_gh \
    "${publisher}" \
    "${repository}" \
    "${release_id}" \
    "${release_tag}" \
    "${expected_commit}" >/dev/null
run_with_fake_gh \
    "${verifier}" \
    "${repository}" \
    "${release_id}" \
    "${release_tag}" \
    "${expected_commit}" \
    published \
    "${artifact_directory}" >/dev/null

write_tag_state "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
if run_with_fake_gh \
    "${verifier}" \
    "${repository}" \
    "${release_id}" \
    "${release_tag}" \
    "${expected_commit}" \
    published \
    "${artifact_directory}" >/dev/null 2>&1; then
    fail "published release with a mismatched resolved tag was accepted"
fi

write_tag_state "${expected_commit}"
if PATH="${fake_bin}:${PATH}" \
    FORGERULES_TEST_ARTIFACTS="${artifact_directory}" \
    FORGERULES_TEST_STATE_DIR="${state_directory}" \
    FORGERULES_TEST_OMIT_ASSET="official_geoip.mmdb" \
    "${verifier}" \
    "${repository}" \
    "${release_id}" \
    "${release_tag}" \
    "${expected_commit}" \
    published \
    "${artifact_directory}" >/dev/null 2>&1; then
    fail "incomplete remote asset set was accepted"
fi

if PATH="${fake_bin}:${PATH}" \
    FORGERULES_TEST_ARTIFACTS="${artifact_directory}" \
    FORGERULES_TEST_STATE_DIR="${state_directory}" \
    FORGERULES_TEST_CORRUPT_ASSET="official_geoip.mmdb" \
    "${verifier}" \
    "${repository}" \
    "${release_id}" \
    "${release_tag}" \
    "${expected_commit}" \
    published \
    "${artifact_directory}" >/dev/null 2>&1; then
    fail "remote bytes differing from build output were accepted"
fi

# Exact annotated tags are peeled to their commit. A same-named branch at a
# different SHA cannot override the exact tag identity.
write_annotated_tag_state "${expected_commit}"
PATH="${fake_bin}:${PATH}" \
    FORGERULES_TEST_ARTIFACTS="${artifact_directory}" \
    FORGERULES_TEST_STATE_DIR="${state_directory}" \
    FORGERULES_TEST_BRANCH_SHA="bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" \
    "${verifier}" \
    "${repository}" \
    "${release_id}" \
    "${release_tag}" \
    "${expected_commit}" \
    published \
    "${artifact_directory}" >/dev/null

# A same-named branch is not a substitute for the exact published tag ref.
rm -f "${state_directory}/tag.json"
if PATH="${fake_bin}:${PATH}" \
    FORGERULES_TEST_ARTIFACTS="${artifact_directory}" \
    FORGERULES_TEST_STATE_DIR="${state_directory}" \
    FORGERULES_TEST_BRANCH_SHA="${expected_commit}" \
    "${verifier}" \
    "${repository}" \
    "${release_id}" \
    "${release_tag}" \
    "${expected_commit}" \
    published \
    "${artifact_directory}" >/dev/null 2>&1; then
    fail "published release accepted a same-named branch without an exact tag ref"
fi

# Publishing the mutable alias uses the same ID checks but explicitly elects
# it as GitHub's latest release.
reset_state
write_release_state true latest "${expected_commit}"
run_with_fake_gh \
    "${publisher}" \
    "${repository}" \
    "${release_id}" \
    latest \
    "${expected_commit}" >/dev/null
if [[ "$(jq -r '.draft' "${state_directory}/release.json")" != "false" ]]; then
    fail "latest draft was not published"
fi

# Cleanup removes only a draft with the exact ID, tag, and target.
reset_state
write_release_state true "${release_tag}" "${expected_commit}"
write_tag_state "${expected_commit}"
run_with_fake_gh \
    "${draft_cleanup}" \
    "${repository}" \
    "${release_id}" \
    "${release_tag}" \
    "${expected_commit}" >/dev/null
if [[ -e "${state_directory}/release.json" || -e "${state_directory}/tag.json" ]]; then
    fail "recoverable draft state was not fully removed"
fi

# A branch with the release-tag name must not be mistaken for an exact tag ref
# during cleanup.
write_release_state true "${release_tag}" "${expected_commit}"
PATH="${fake_bin}:${PATH}" \
    FORGERULES_TEST_ARTIFACTS="${artifact_directory}" \
    FORGERULES_TEST_STATE_DIR="${state_directory}" \
    FORGERULES_TEST_BRANCH_SHA="${expected_commit}" \
    "${draft_cleanup}" \
    "${repository}" \
    "${release_id}" \
    "${release_tag}" \
    "${expected_commit}" >/dev/null
if [[ -e "${state_directory}/release.json" ]]; then
    fail "branch-name collision prevented exact draft cleanup"
fi

# An expected-SHA tag is never deleted when the exact release ID is absent.
reset_state
write_tag_state "${expected_commit}"
if run_with_fake_gh \
    "${draft_cleanup}" \
    "${repository}" \
    "${release_id}" \
    "${release_tag}" \
    "${expected_commit}" >/dev/null 2>&1; then
    fail "cleanup guessed tag ownership after the exact release ID disappeared"
fi
if [[ ! -e "${state_directory}/tag.json" ]]; then
    fail "cleanup deleted an unassociated same-SHA tag"
fi

# Cleanup must never delete a published release.
write_release_state false "${release_tag}" "${expected_commit}"
write_tag_state "${expected_commit}"
if run_with_fake_gh \
    "${draft_cleanup}" \
    "${repository}" \
    "${release_id}" \
    "${release_tag}" \
    "${expected_commit}" >/dev/null 2>&1; then
    fail "published release was accepted for draft cleanup"
fi
if [[ ! -e "${state_directory}/release.json" || ! -e "${state_directory}/tag.json" ]]; then
    fail "published release state changed during refused cleanup"
fi

# A different release owning the tag also blocks cleanup of an absent ID.
write_release_state false "${release_tag}" "${expected_commit}" 202
if run_with_fake_gh \
    "${draft_cleanup}" \
    "${repository}" \
    "${release_id}" \
    "${release_tag}" \
    "${expected_commit}" >/dev/null 2>&1; then
    fail "cleanup accepted a tag owned by a different release ID"
fi
if [[ ! -e "${state_directory}/release.json" || ! -e "${state_directory}/tag.json" ]]; then
    fail "different published release state changed during refused cleanup"
fi

# Latest deletion distinguishes true absence from API failures and removes both
# objects when present.
reset_state
run_with_fake_gh "${latest_delete}" "${repository}" >/dev/null
write_release_state false latest "${expected_commit}"
write_tag_state "${expected_commit}"
run_with_fake_gh "${latest_delete}" "${repository}" >/dev/null
if [[ -e "${state_directory}/release.json" || -e "${state_directory}/tag.json" ]]; then
    fail "previous latest release state was not removed"
fi
if PATH="${fake_bin}:${PATH}" \
    FORGERULES_TEST_ARTIFACTS="${artifact_directory}" \
    FORGERULES_TEST_STATE_DIR="${state_directory}" \
    FORGERULES_TEST_API_ERROR_MATCH="releases/tags/latest" \
    "${latest_delete}" "${repository}" >/dev/null 2>&1; then
    fail "non-404 latest lookup failure was treated as absence"
fi
if PATH="${fake_bin}:${PATH}" \
    FORGERULES_TEST_ARTIFACTS="${artifact_directory}" \
    FORGERULES_TEST_STATE_DIR="${state_directory}" \
    FORGERULES_TEST_API_ERROR_MATCH="releases/tags/latest" \
    FORGERULES_TEST_API_ERROR_MESSAGE="gh: proxy target not found" \
    "${latest_delete}" "${repository}" >/dev/null 2>&1; then
    fail "generic not-found API failure was treated as an HTTP 404 absence"
fi
if PATH="${fake_bin}:${PATH}" \
    FORGERULES_TEST_ARTIFACTS="${artifact_directory}" \
    FORGERULES_TEST_STATE_DIR="${state_directory}" \
    FORGERULES_TEST_API_ERROR_MATCH="releases/tags/latest" \
    FORGERULES_TEST_API_ERROR_MESSAGE="gh: authentication required" \
    FORGERULES_TEST_API_ERROR_STATUS=4 \
    "${latest_delete}" "${repository}" >/dev/null 2>&1; then
    fail "gh authentication exit status 4 was mistaken for the 404 sentinel"
fi

echo "Release ID, draft publication, cleanup, and latest replacement tests passed"
