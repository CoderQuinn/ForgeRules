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

printf '%s %s\n' "${method}" "${endpoint}" >>"${FORGERULES_TEST_STATE_DIR}/curl.log"
if [[ -n "${FORGERULES_TEST_CURL_PLAN:-}" ]]; then
    ordinal="$(awk -v request="${method} ${endpoint}" '$0 == request { n++ } END { print n + 0 }' "${FORGERULES_TEST_STATE_DIR}/curl.log")"
    action="$(jq -c --arg method "${method}" --arg endpoint "${endpoint}" --argjson ordinal "${ordinal}" \
        '.[] | select(.method == $method and .endpoint == $endpoint and .on == $ordinal)' "${FORGERULES_TEST_CURL_PLAN}")"
    if [[ -n "${action}" ]]; then
        if jq -e 'has("mutate_release")' <<<"${action}" >/dev/null; then
            original='{}'
            if [[ -f "${release_file}" ]]; then original="$(cat "${release_file}")"; fi
            jq -n --argjson original "${original}" --argjson action "${action}" \
                '$original + $action.mutate_release' >"${release_file}"
        fi
        if jq -e 'has("error")' <<<"${action}" >/dev/null; then jq -r '.error' <<<"${action}" >&2; fi
        if jq -e 'has("raw")' <<<"${action}" >/dev/null; then
            jq -r '.raw' <<<"${action}" >"${output_file}"
        elif jq -e 'has("response")' <<<"${action}" >/dev/null; then
            jq -c '.response' <<<"${action}" >"${output_file}"
        fi
        if jq -e 'has("response") or has("raw") or has("status") or has("exit")' <<<"${action}" >/dev/null; then
            printf '%s' "$(jq -r '.status // 201' <<<"${action}")"
            exit "$(jq -r '.exit // 0' <<<"${action}")"
        fi
    fi
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
