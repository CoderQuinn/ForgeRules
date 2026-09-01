#!/usr/bin/env bash

set -euo pipefail

readonly script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly verifier="${script_directory}/verify-release-assets.sh"
readonly creator="${script_directory}/create-draft-release.sh"
readonly publisher="${script_directory}/publish-draft-release.sh"
readonly draft_cleanup="${script_directory}/cleanup-failed-draft-release.sh"
readonly latest_delete="${script_directory}/delete-latest-release.sh"
readonly retagger="${script_directory}/retag-draft-release.sh"
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
    local make_latest="false"
    if [[ "${draft}" == "false" && "${tag}" == "latest" ]]; then
        make_latest="true"
    fi
    jq -n \
        --argjson id "${id}" \
        --arg tag "${tag}" \
        --argjson draft "${draft}" \
        --arg target "${target}" \
        --arg make_latest "${make_latest}" \
        '{
            id: $id,
            node_id: ("R_" + ($id | tostring)),
            tag_name: $tag,
            name: "Fixture Release",
            draft: $draft,
            prerelease: false,
            immutable: false,
            target_commitish: $target,
            updated_at: "2026-08-30T00:00:00Z",
            make_latest: $make_latest
        }' \
        >"${state_directory}/release.json"
}

write_tag_state() {
    local target="$1"
    local tag="${2:-$(jq -r '.tag_name' "${state_directory}/release.json")}"
    jq -n \
        --arg name "${tag}" \
        --arg sha "${target}" \
        '{name: $name, type: "commit", sha: $sha}' >"${state_directory}/tag.json"
}

write_annotated_tag_state() {
    local target="$1"
    local tag="${2:-$(jq -r '.tag_name' "${state_directory}/release.json")}"
    local tag_object_sha="cccccccccccccccccccccccccccccccccccccccc"
    jq -n \
        --arg name "${tag}" \
        --arg type tag \
        --arg sha "${tag_object_sha}" \
        '{name: $name, type: $type, sha: $sha}' >"${state_directory}/tag.json"
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
        "${state_directory}/patch-publish-count" \
        "${state_directory}/patch-retag-count" \
        "${state_directory}/tag-ref-query-count" \
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
    FORGERULES_TEST_HIDE_RELEASE_FROM_LIST=true \
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

printf 'unexpected remote fixture\n' >"${artifact_directory}/unexpected.bin"
if run_with_fake_gh \
    "${verifier}" \
    "${repository}" \
    "${release_id}" \
    "${release_tag}" \
    "${expected_commit}" \
    published \
    "${artifact_directory}" >/dev/null 2>&1; then
    fail "unexpected remote release asset was accepted"
fi
rm -f "${artifact_directory}/unexpected.bin"

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

# The mutable alias is built as a unique staging-tag draft. The same exact ID
# is retagged only after staging verification, remains private, and is then
# published with an explicit GitHub latest pointer.
readonly staging_tag="latest-staging-123-1"
reset_state
write_release_state true "${staging_tag}" "${expected_commit}"
run_with_fake_gh \
    "${verifier}" \
    "${repository}" \
    "${release_id}" \
    "${staging_tag}" \
    "${expected_commit}" \
    draft \
    "${artifact_directory}" >/dev/null
run_with_fake_gh \
    "${retagger}" \
    "${repository}" \
    "${release_id}" \
    "${staging_tag}" \
    latest \
    "${expected_commit}" >/dev/null
if [[ "$(jq -r '[.id, .tag_name, .draft] | @tsv' "${state_directory}/release.json")" != $'101\tlatest\ttrue' || \
      -e "${state_directory}/tag.json" ]]; then
    fail "staging draft was not privately retagged by exact ID"
fi
run_with_fake_gh \
    "${publisher}" \
    "${repository}" \
    "${release_id}" \
    latest \
    "${expected_commit}" >/dev/null
run_with_fake_gh \
    "${verifier}" \
    "${repository}" \
    "${release_id}" \
    latest \
    "${expected_commit}" \
    published \
    "${artifact_directory}" >/dev/null
if [[ "$(jq -r '[.draft, .make_latest] | @tsv' "${state_directory}/release.json")" != $'false\ttrue' ]]; then
    fail "latest draft was not explicitly published as GitHub latest"
fi
if PATH="${fake_bin}:${PATH}" \
    FORGERULES_TEST_ARTIFACTS="${artifact_directory}" \
    FORGERULES_TEST_STATE_DIR="${state_directory}" \
    FORGERULES_TEST_LATEST_ID_OVERRIDE=202 \
    "${verifier}" \
    "${repository}" \
    "${release_id}" \
    latest \
    "${expected_commit}" \
    published \
    "${artifact_directory}" >/dev/null 2>&1; then
    fail "published latest verification ignored GitHub's latest-release pointer"
fi

# A transport failure after either PATCH applied is reconciled as success; a
# failure before mutation is retried once only from the intact pre-state.
reset_state
write_release_state true "${staging_tag}" "${expected_commit}"
PATH="${fake_bin}:${PATH}" \
    FORGERULES_TEST_ARTIFACTS="${artifact_directory}" \
    FORGERULES_TEST_STATE_DIR="${state_directory}" \
    FORGERULES_TEST_PATCH_ERROR_AFTER_MUTATION=retag \
    "${retagger}" \
    "${repository}" \
    "${release_id}" \
    "${staging_tag}" \
    latest \
    "${expected_commit}" >/dev/null 2>&1
if [[ "$(jq -r '.tag_name' "${state_directory}/release.json")" != "latest" ]]; then
    fail "retag timeout-after-apply was not reconciled by exact ID"
fi
PATH="${fake_bin}:${PATH}" \
    FORGERULES_TEST_ARTIFACTS="${artifact_directory}" \
    FORGERULES_TEST_STATE_DIR="${state_directory}" \
    FORGERULES_TEST_PATCH_ERROR_AFTER_MUTATION=publish \
    "${publisher}" \
    "${repository}" \
    "${release_id}" \
    latest \
    "${expected_commit}" >/dev/null 2>&1
if [[ "$(jq -r '.draft' "${state_directory}/release.json")" != "false" ]]; then
    fail "publish timeout-after-apply was not reconciled by exact ID"
fi

reset_state
write_release_state true "${staging_tag}" "${expected_commit}"
PATH="${fake_bin}:${PATH}" \
    FORGERULES_TEST_ARTIFACTS="${artifact_directory}" \
    FORGERULES_TEST_STATE_DIR="${state_directory}" \
    FORGERULES_TEST_PATCH_ERROR_BEFORE_MUTATION=retag \
    "${retagger}" \
    "${repository}" \
    "${release_id}" \
    "${staging_tag}" \
    latest \
    "${expected_commit}" >/dev/null 2>&1
if [[ "$(<"${state_directory}/patch-retag-count")" -ne 2 ]]; then
    fail "retag failure-before-apply did not use exactly one safe retry"
fi
PATH="${fake_bin}:${PATH}" \
    FORGERULES_TEST_ARTIFACTS="${artifact_directory}" \
    FORGERULES_TEST_STATE_DIR="${state_directory}" \
    FORGERULES_TEST_PATCH_ERROR_BEFORE_MUTATION=publish \
    "${publisher}" \
    "${repository}" \
    "${release_id}" \
    latest \
    "${expected_commit}" >/dev/null 2>&1
if [[ "$(<"${state_directory}/patch-publish-count")" -ne 2 ]]; then
    fail "publish failure-before-apply did not use exactly one safe retry"
fi

# Draft cleanup removes only the exact fresh release. It never infers ownership
# of a public tag from a matching SHA.
reset_state
write_release_state true "${release_tag}" "${expected_commit}"
run_with_fake_gh \
    "${draft_cleanup}" \
    "${repository}" \
    "${release_id}" \
    "${release_tag}" \
    "${expected_commit}" >/dev/null
if [[ -e "${state_directory}/release.json" ]]; then
    fail "exact failed draft was not removed"
fi

reset_state
write_release_state true "${release_tag}" "${expected_commit}"
write_tag_state "${expected_commit}"
if run_with_fake_gh \
    "${draft_cleanup}" \
    "${repository}" \
    "${release_id}" \
    "${release_tag}" \
    "${expected_commit}" >/dev/null 2>&1; then
    fail "draft cleanup claimed an unassociated tag ref"
fi
if [[ -e "${state_directory}/release.json" || ! -e "${state_directory}/tag.json" ]]; then
    fail "draft cleanup did not remove only the exact release ID"
fi

# A same-named branch is not an exact tag ref and cannot block exact-ID draft
# cleanup.
reset_state
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

# Cleanup accepts either owned stage name for the exact latest release ID, but
# an unlisted tag or changed target fails before mutation.
reset_state
write_release_state true latest "${expected_commit}"
run_with_fake_gh \
    "${draft_cleanup}" \
    "${repository}" \
    "${release_id}" \
    "${staging_tag}" \
    "${expected_commit}" \
    latest >/dev/null
if [[ -e "${state_directory}/release.json" ]]; then
    fail "cleanup rejected the exact ID after staging-to-latest retag"
fi
write_release_state true "rules-20260831" "${expected_commit}"
if run_with_fake_gh \
    "${draft_cleanup}" \
    "${repository}" \
    "${release_id}" \
    "${staging_tag}" \
    "${expected_commit}" \
    latest >/dev/null 2>&1; then
    fail "cleanup accepted a tag outside its exact-ID allowlist"
fi
if [[ ! -e "${state_directory}/release.json" ]]; then
    fail "tag identity mismatch mutated the exact release"
fi
write_release_state true "${staging_tag}" "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
if run_with_fake_gh \
    "${draft_cleanup}" \
    "${repository}" \
    "${release_id}" \
    "${staging_tag}" \
    "${expected_commit}" \
    latest >/dev/null 2>&1; then
    fail "cleanup accepted a changed target for the exact release ID"
fi
if [[ ! -e "${state_directory}/release.json" ]]; then
    fail "target identity mismatch mutated the exact release"
fi

# If the exact ID is gone, neither a same-SHA ref nor a release owned by a
# different ID may be guessed as this workflow's object.
reset_state
write_tag_state "${expected_commit}" "${release_tag}"
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

# A valid failed publication is withdrawn release-first, then its still-stable
# exact ref is deleted. Ambiguous DELETE responses are reconciled by absence.
reset_state
write_release_state false "${release_tag}" "${expected_commit}"
write_tag_state "${expected_commit}"
run_with_fake_gh \
    "${draft_cleanup}" \
    "${repository}" \
    "${release_id}" \
    "${release_tag}" \
    "${expected_commit}" >/dev/null
if [[ -e "${state_directory}/release.json" || -e "${state_directory}/tag.json" ]]; then
    fail "verified failed publication state was not removed"
fi

reset_state
write_release_state false "${release_tag}" "${expected_commit}"
write_tag_state "${expected_commit}"
PATH="${fake_bin}:${PATH}" \
    FORGERULES_TEST_ARTIFACTS="${artifact_directory}" \
    FORGERULES_TEST_STATE_DIR="${state_directory}" \
    FORGERULES_TEST_DELETE_ERROR_AFTER_MUTATION=release \
    "${draft_cleanup}" \
    "${repository}" \
    "${release_id}" \
    "${release_tag}" \
    "${expected_commit}" >/dev/null 2>&1
if [[ -e "${state_directory}/release.json" || -e "${state_directory}/tag.json" ]]; then
    fail "ambiguous published-release delete was not reconciled"
fi

# If the public association or raw ref changes, the exact fresh release is
# still withdrawn, but the now-unowned tag is preserved and cleanup fails.
reset_state
write_release_state false "${release_tag}" "${expected_commit}"
write_tag_state "${expected_commit}"
if PATH="${fake_bin}:${PATH}" \
    FORGERULES_TEST_ARTIFACTS="${artifact_directory}" \
    FORGERULES_TEST_STATE_DIR="${state_directory}" \
    FORGERULES_TEST_RELEASE_BY_TAG_ID_OVERRIDE=202 \
    "${draft_cleanup}" \
    "${repository}" \
    "${release_id}" \
    "${release_tag}" \
    "${expected_commit}" >/dev/null 2>&1; then
    fail "published cleanup accepted a different release-by-tag owner"
fi
if [[ -e "${state_directory}/release.json" || ! -e "${state_directory}/tag.json" ]]; then
    fail "published identity mismatch did not withdraw only the exact release"
fi

reset_state
write_release_state false "${release_tag}" "${expected_commit}"
write_tag_state "${expected_commit}"
if PATH="${fake_bin}:${PATH}" \
    FORGERULES_TEST_ARTIFACTS="${artifact_directory}" \
    FORGERULES_TEST_STATE_DIR="${state_directory}" \
    FORGERULES_TEST_API_ERROR_MATCH="git/ref/tags/${release_tag}" \
    "${draft_cleanup}" \
    "${repository}" \
    "${release_id}" \
    "${release_tag}" \
    "${expected_commit}" >/dev/null 2>&1; then
    fail "published cleanup treated unavailable tag evidence as sufficient"
fi
if [[ -e "${state_directory}/release.json" || ! -e "${state_directory}/tag.json" ]]; then
    fail "tag-evidence API failure prevented exact public release withdrawal"
fi

reset_state
write_release_state false "${release_tag}" "${expected_commit}"
write_tag_state "${expected_commit}"
if PATH="${fake_bin}:${PATH}" \
    FORGERULES_TEST_ARTIFACTS="${artifact_directory}" \
    FORGERULES_TEST_STATE_DIR="${state_directory}" \
    FORGERULES_TEST_MUTATE_TAG_ON_REF_QUERY=3 \
    "${draft_cleanup}" \
    "${repository}" \
    "${release_id}" \
    "${release_tag}" \
    "${expected_commit}" >/dev/null 2>&1; then
    fail "published cleanup ignored a post-release-delete tag replacement"
fi
if [[ -e "${state_directory}/release.json" || ! -e "${state_directory}/tag.json" ]]; then
    fail "changed public tag was not preserved after exact release withdrawal"
fi

# Old latest deletion permits only a complete published release/ref pair or
# complete absence. Draft, immutable, and asymmetric states are fail closed.
reset_state
run_with_fake_gh "${latest_delete}" "${repository}" >/dev/null
write_release_state false latest "${expected_commit}"
write_tag_state "${expected_commit}"
run_with_fake_gh "${latest_delete}" "${repository}" >/dev/null
if [[ -e "${state_directory}/release.json" || -e "${state_directory}/tag.json" ]]; then
    fail "previous latest release/ref pair was not removed"
fi

reset_state
write_release_state false latest "${expected_commit}"
if run_with_fake_gh "${latest_delete}" "${repository}" >/dev/null 2>&1; then
    fail "release-only latest state was accepted"
fi
if [[ ! -e "${state_directory}/release.json" ]]; then
    fail "release-only latest state was mutated"
fi
reset_state
write_tag_state "${expected_commit}" latest
if run_with_fake_gh "${latest_delete}" "${repository}" >/dev/null 2>&1; then
    fail "tag-only latest state was accepted"
fi
if [[ ! -e "${state_directory}/tag.json" ]]; then
    fail "tag-only latest state was mutated"
fi
reset_state
write_release_state true latest "${expected_commit}"
if run_with_fake_gh "${latest_delete}" "${repository}" >/dev/null 2>&1; then
    fail "hidden latest draft collision was accepted"
fi
if [[ ! -e "${state_directory}/release.json" ]]; then
    fail "hidden latest draft collision was mutated"
fi
reset_state
write_release_state false latest "${expected_commit}"
jq '.immutable = true' "${state_directory}/release.json" >"${state_directory}/release.next.json"
mv "${state_directory}/release.next.json" "${state_directory}/release.json"
write_tag_state "${expected_commit}"
if run_with_fake_gh "${latest_delete}" "${repository}" >/dev/null 2>&1; then
    fail "immutable latest release was accepted for mutable replacement"
fi
if [[ ! -e "${state_directory}/release.json" || ! -e "${state_directory}/tag.json" ]]; then
    fail "immutable latest state was mutated"
fi

# The raw ref object is rechecked before deletion. A pre-delete change blocks
# all mutation; a change after exact release deletion preserves the new ref.
reset_state
write_release_state false latest "${expected_commit}"
write_tag_state "${expected_commit}"
jq -n \
    --arg tag_sha "cccccccccccccccccccccccccccccccccccccccc" \
    --arg target "${expected_commit}" \
    '{tag_sha: $tag_sha, object: {type: "commit", sha: $target}}' \
    >"${state_directory}/annotated-tag.json"
if PATH="${fake_bin}:${PATH}" \
    FORGERULES_TEST_ARTIFACTS="${artifact_directory}" \
    FORGERULES_TEST_STATE_DIR="${state_directory}" \
    FORGERULES_TEST_MUTATE_TAG_ON_REF_QUERY=2 \
    FORGERULES_TEST_MUTATED_TAG_TYPE=tag \
    FORGERULES_TEST_MUTATED_TAG_SHA=cccccccccccccccccccccccccccccccccccccccc \
    "${latest_delete}" "${repository}" >/dev/null 2>&1; then
    fail "pre-delete raw latest ref replacement with the same peeled commit was accepted"
fi
if [[ ! -e "${state_directory}/release.json" || ! -e "${state_directory}/tag.json" ]]; then
    fail "pre-delete latest ref replacement mutated state"
fi

reset_state
write_release_state false latest "${expected_commit}"
write_tag_state "${expected_commit}"
if PATH="${fake_bin}:${PATH}" \
    FORGERULES_TEST_ARTIFACTS="${artifact_directory}" \
    FORGERULES_TEST_STATE_DIR="${state_directory}" \
    FORGERULES_TEST_MUTATE_TAG_ON_REF_QUERY=3 \
    "${latest_delete}" "${repository}" >/dev/null 2>&1; then
    fail "post-release-delete latest ref replacement was accepted"
fi
if [[ -e "${state_directory}/release.json" || ! -e "${state_directory}/tag.json" ]]; then
    fail "post-release-delete latest ref replacement was not preserved"
fi

# Ambiguous old-state DELETE responses are accepted only after the exact
# release/ref is confirmed absent.
reset_state
write_release_state false latest "${expected_commit}"
write_tag_state "${expected_commit}"
PATH="${fake_bin}:${PATH}" \
    FORGERULES_TEST_ARTIFACTS="${artifact_directory}" \
    FORGERULES_TEST_STATE_DIR="${state_directory}" \
    FORGERULES_TEST_DELETE_ERROR_AFTER_MUTATION=release \
    "${latest_delete}" "${repository}" >/dev/null 2>&1
if [[ -e "${state_directory}/release.json" || -e "${state_directory}/tag.json" ]]; then
    fail "ambiguous old latest release delete was not reconciled"
fi
reset_state
write_release_state false latest "${expected_commit}"
write_tag_state "${expected_commit}"
PATH="${fake_bin}:${PATH}" \
    FORGERULES_TEST_ARTIFACTS="${artifact_directory}" \
    FORGERULES_TEST_STATE_DIR="${state_directory}" \
    FORGERULES_TEST_DELETE_ERROR_AFTER_MUTATION=tag \
    "${latest_delete}" "${repository}" >/dev/null 2>&1
if [[ -e "${state_directory}/release.json" || -e "${state_directory}/tag.json" ]]; then
    fail "ambiguous old latest tag delete was not reconciled"
fi

reset_state
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

echo "Fresh-ID draft, PATCH reconciliation, safe cleanup, and latest replacement tests passed"
