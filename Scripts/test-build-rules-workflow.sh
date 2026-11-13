#!/usr/bin/env bash

set -euo pipefail

readonly script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly build_workflow="${script_directory}/../.github/workflows/build-rules.yml"
readonly manual_workflow="${script_directory}/../.github/workflows/release-manual.yml"

step_line() {
    local workflow="$1"
    local step_name="$2"
    awk -v needle="- name: ${step_name}" 'index($0, needle) { print NR; exit }' "${workflow}"
}

require_step() {
    local workflow="$1"
    local step_name="$2"
    local line
    line="$(step_line "${workflow}" "${step_name}")"
    if [[ -z "${line}" ]]; then
        echo "release workflow test failed: missing step in ${workflow}: ${step_name}" >&2
        exit 1
    fi
    printf '%s' "${line}"
}

readonly create_dated_line="$(require_step "${build_workflow}" "Create dated draft release")"
readonly verify_dated_draft_line="$(require_step "${build_workflow}" "Verify dated draft release")"
readonly publish_dated_line="$(require_step "${build_workflow}" "Publish dated release")"
readonly verify_dated_published_line="$(require_step "${build_workflow}" "Verify published dated release")"
readonly cleanup_dated_line="$(require_step "${build_workflow}" "Clean up failed dated release")"
readonly prepare_latest_line="$(require_step "${build_workflow}" "Prepare latest staging tag")"
readonly create_latest_line="$(require_step "${build_workflow}" "Create latest staging draft")"
readonly verify_latest_staging_line="$(require_step "${build_workflow}" "Verify latest staging draft")"
readonly delete_latest_line="$(require_step "${build_workflow}" "Delete previous latest release explicitly")"
readonly retag_latest_line="$(require_step "${build_workflow}" "Retag verified staging draft to latest")"
readonly verify_latest_draft_line="$(require_step "${build_workflow}" "Verify retagged latest draft")"
readonly publish_latest_line="$(require_step "${build_workflow}" "Publish latest release")"
readonly verify_latest_published_line="$(require_step "${build_workflow}" "Verify published latest release")"
readonly cleanup_latest_line="$(require_step "${build_workflow}" "Clean up failed latest release")"

if ! ((
    create_dated_line < verify_dated_draft_line &&
    verify_dated_draft_line < publish_dated_line &&
    publish_dated_line < verify_dated_published_line &&
    verify_dated_published_line < cleanup_dated_line &&
    cleanup_dated_line < prepare_latest_line &&
    prepare_latest_line < create_latest_line &&
    create_latest_line < verify_latest_staging_line &&
    verify_latest_staging_line < delete_latest_line &&
    delete_latest_line < retag_latest_line &&
    retag_latest_line < verify_latest_draft_line &&
    verify_latest_draft_line < publish_latest_line &&
    publish_latest_line < verify_latest_published_line &&
    verify_latest_published_line < cleanup_latest_line
)); then
    echo "release workflow test failed: draft verification and publication ordering is unsafe" >&2
    exit 1
fi

readonly manual_create_line="$(require_step "${manual_workflow}" "Create dated draft release")"
readonly manual_verify_draft_line="$(require_step "${manual_workflow}" "Verify dated draft release")"
readonly manual_publish_line="$(require_step "${manual_workflow}" "Publish dated release")"
readonly manual_verify_published_line="$(require_step "${manual_workflow}" "Verify published dated release")"
readonly manual_cleanup_line="$(require_step "${manual_workflow}" "Clean up failed dated release")"
if ! ((
    manual_create_line < manual_verify_draft_line &&
    manual_verify_draft_line < manual_publish_line &&
    manual_publish_line < manual_verify_published_line &&
    manual_verify_published_line < manual_cleanup_line
)); then
    echo "release workflow test failed: manual dated release ordering is unsafe" >&2
    exit 1
fi

if [[ "$(grep -c 'Scripts/verify-release-assets.sh' "${build_workflow}")" -ne 5 ]]; then
    echo "release workflow test failed: build workflow must verify dated draft/published and staging/retagged/published latest" >&2
    exit 1
fi
if [[ "$(grep -c 'Scripts/verify-release-assets.sh' "${manual_workflow}")" -ne 2 ]]; then
    echo "release workflow test failed: manual workflow must verify draft and published release" >&2
    exit 1
fi
if [[ "$(grep -c 'Scripts/create-draft-release.sh' "${build_workflow}")" -ne 2 || \
      "$(grep -c 'Scripts/create-draft-release.sh' "${manual_workflow}")" -ne 1 ]]; then
    echo "release workflow test failed: every release must be created by the fresh-ID REST script" >&2
    exit 1
fi
if [[ "$(grep -c 'Scripts/retag-draft-release.sh' "${build_workflow}")" -ne 1 ]]; then
    echo "release workflow test failed: latest must be promoted by exact fresh release ID" >&2
    exit 1
fi
if grep -q 'continue-on-error' "${build_workflow}" "${manual_workflow}"; then
    echo "release workflow test failed: release API failures must not be ignored" >&2
    exit 1
fi
if [[ "$(grep 'Scripts/create-draft-release.sh' "${build_workflow}" | grep -c '"${GITHUB_SHA}"')" -ne 2 || \
      "$(grep 'Scripts/create-draft-release.sh' "${manual_workflow}" | grep -c '"${GITHUB_SHA}"')" -ne 1 ]]; then
    echo "release workflow test failed: every fresh draft must target the workflow SHA explicitly" >&2
    exit 1
fi
if grep -q 'softprops/action-gh-release' "${build_workflow}" "${manual_workflow}"; then
    echo "release workflow test failed: draft creation must not use tag-based upsert actions" >&2
    exit 1
fi
if ! grep -q 'steps.dated_draft.outputs.id' "${build_workflow}" || \
   ! grep -q 'steps.latest_draft.outputs.id' "${build_workflow}" || \
   ! grep -q 'steps.dated_draft.outputs.id' "${manual_workflow}"; then
    echo "release workflow test failed: release verification must use the exact action output ID" >&2
    exit 1
fi
if [[ "$(grep -c 'persist-credentials: false' "${build_workflow}")" -ne 1 || \
      "$(grep -c 'persist-credentials: false' "${manual_workflow}")" -ne 1 ]]; then
    echo "release workflow test failed: release jobs must not persist the write credential in checkout" >&2
    exit 1
fi
while IFS= read -r action_line; do
    action_reference="${action_line##*@}"
    action_reference="${action_reference%% *}"
    if [[ ! "${action_reference}" =~ ^[0-9a-f]{40}$ ]]; then
        echo "release workflow test failed: action is not pinned to a full commit SHA: ${action_line}" >&2
        exit 1
    fi
done < <(grep -h '^[[:space:]]*uses:' "${build_workflow}" "${manual_workflow}")

if ! grep -q 'latest-staging-${GITHUB_RUN_ID}-${GITHUB_RUN_ATTEMPT}' "${build_workflow}"; then
    echo "release workflow test failed: latest staging tag is not unique to the run attempt" >&2
    exit 1
fi
if ! grep -q 'release_scope:' "${build_workflow}" || \
   ! grep -q 'latest-only' "${build_workflow}" || \
   ! grep -q "inputs.release_scope == 'full'" "${build_workflow}"; then
    echo "release workflow test failed: workflow_dispatch lacks a full/latest-only recovery choice" >&2
    exit 1
fi
if [[ "$(grep -c "env.FULL_RELEASE == 'true'" "${build_workflow}")" -ne 7 ]]; then
    echo "release workflow test failed: every dated-only step, including cleanup, must be gated from latest-only recovery" >&2
    exit 1
fi
if ! grep -Fq '"${RELEASE_ID}" "${STAGING_TAG}" "${GITHUB_SHA}" latest' "${build_workflow}"; then
    echo "release workflow test failed: latest cleanup must allow both staging and final tag names for the same ID" >&2
    exit 1
fi

echo "Scheduled, latest-only recovery, and manual release ordering tests passed"
