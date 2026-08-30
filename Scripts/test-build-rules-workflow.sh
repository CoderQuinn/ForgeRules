#!/usr/bin/env bash

set -euo pipefail

readonly script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly workflow="${script_directory}/../.github/workflows/build-rules.yml"

step_line() {
    local step_name="$1"
    awk -v needle="- name: ${step_name}" 'index($0, needle) { print NR; exit }' "${workflow}"
}

require_step() {
    local step_name="$1"
    local line
    line="$(step_line "${step_name}")"
    if [[ -z "${line}" ]]; then
        echo "release workflow test failed: missing step: ${step_name}" >&2
        exit 1
    fi
    printf '%s' "${line}"
}

readonly create_dated_line="$(require_step "Create dated release")"
readonly verify_dated_line="$(require_step "Verify dated release")"
readonly delete_latest_line="$(require_step "Delete previous latest release")"
readonly create_latest_line="$(require_step "Create latest release")"
readonly verify_latest_line="$(require_step "Verify latest release")"

if ! ((
    create_dated_line < verify_dated_line &&
    verify_dated_line < delete_latest_line &&
    delete_latest_line < create_latest_line &&
    create_latest_line < verify_latest_line
)); then
    echo "release workflow test failed: immutable dated release must be created and verified before latest moves" >&2
    exit 1
fi

if [[ "$(grep -c 'Scripts/verify-release-assets.sh' "${workflow}")" -ne 2 ]]; then
    echo "release workflow test failed: dated and latest releases must both verify uploaded assets" >&2
    exit 1
fi

echo "Build Rules publication ordering tests passed"
