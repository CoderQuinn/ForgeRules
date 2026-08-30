#!/usr/bin/env bash

set -euo pipefail

if [[ -z "${FORGERULES_TEST_STATE_DIR:-}" ]]; then
    echo "FORGERULES_TEST_STATE_DIR is required" >&2
    exit 64
fi

readonly release_file="${FORGERULES_TEST_STATE_DIR}/release.json"
readonly upload_log="${FORGERULES_TEST_STATE_DIR}/uploads.txt"

output_file=""
data_file=""
endpoint=""
method="GET"
while [[ "$#" -gt 0 ]]; do
    case "$1" in
        --output)
            output_file="$2"
            shift 2
            ;;
        --data-binary)
            data_file="${2#@}"
            shift 2
            ;;
        --request)
            method="$2"
            shift 2
            ;;
        --write-out|--header)
            shift 2
            ;;
        --location|--silent|--show-error)
            shift
            ;;
        https://*)
            endpoint="$1"
            shift
            ;;
        *)
            echo "unsupported fake curl argument: $1" >&2
            exit 64
            ;;
    esac
done

if [[ -z "${output_file}" || -z "${data_file}" || -z "${endpoint}" ]]; then
    echo "fake curl invocation is incomplete" >&2
    exit 64
fi

if [[ "${method}" == "POST" && "${endpoint}" =~ ^https://api\.github\.com/repos/[^/]+/[^/]+/releases$ ]]; then
    if [[ -f "${release_file}" ]]; then
        jq -n '{message: "Validation Failed", errors: [{code: "already_exists"}]}' >"${output_file}"
        printf '422'
        exit 0
    fi
    release_id="${FORGERULES_TEST_NEXT_RELEASE_ID:-101}"
    jq \
        --argjson id "${release_id}" \
        --arg upload_url "https://uploads.github.com/repos/CoderQuinn/ForgeRules/releases/${release_id}/assets{?name,label}" \
        '. + {id: $id, upload_url: $upload_url}' \
        "${data_file}" >"${release_file}"
    cp "${release_file}" "${output_file}"
    : >"${upload_log}"
    printf '201'
    exit 0
fi

if [[ "${method}" == "POST" && "${endpoint}" =~ ^https://uploads\.github\.com/repos/[^/]+/[^/]+/releases/([0-9]+)/assets\?name=([A-Za-z0-9._-]+)$ ]]; then
    requested_id="${BASH_REMATCH[1]}"
    asset_name="${BASH_REMATCH[2]}"
    if [[ ! -f "${release_file}" || "$(jq -r '.id' "${release_file}")" != "${requested_id}" ]]; then
        jq -n '{message: "Not Found"}' >"${output_file}"
        printf '404'
        exit 0
    fi
    if [[ "$(basename "${data_file}")" != "${asset_name}" ]]; then
        jq -n '{message: "asset name mismatch"}' >"${output_file}"
        printf '400'
        exit 0
    fi
    if [[ "${asset_name}" == "${FORGERULES_TEST_FAIL_UPLOAD_ASSET:-}" ]]; then
        jq -n '{message: "injected upload failure"}' >"${output_file}"
        printf '500'
        exit 0
    fi
    printf '%s\n' "${asset_name}" >>"${upload_log}"
    asset_id="$((1000 + $(wc -l <"${upload_log}")))"
    jq -n \
        --argjson id "${asset_id}" \
        --arg name "${asset_name}" \
        '{id: $id, name: $name, state: "uploaded"}' >"${output_file}"
    printf '201'
    exit 0
fi

echo "unsupported fake curl endpoint: ${endpoint}" >&2
exit 64
