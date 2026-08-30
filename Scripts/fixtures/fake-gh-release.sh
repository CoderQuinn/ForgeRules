#!/usr/bin/env bash

set -euo pipefail

if [[ -z "${FORGERULES_TEST_ARTIFACTS:-}" || -z "${FORGERULES_TEST_STATE_DIR:-}" ]]; then
    echo "FORGERULES_TEST_ARTIFACTS and FORGERULES_TEST_STATE_DIR are required" >&2
    exit 64
fi

readonly release_file="${FORGERULES_TEST_STATE_DIR}/release.json"
readonly tag_file="${FORGERULES_TEST_STATE_DIR}/tag.json"
readonly annotated_tag_file="${FORGERULES_TEST_STATE_DIR}/annotated-tag.json"

not_found() {
    echo "gh: Not Found (HTTP 404)" >&2
    exit 1
}

if [[ "$#" -lt 1 || "$1" != "api" ]]; then
    echo "unsupported fake gh invocation: $*" >&2
    exit 64
fi
shift

method="GET"
jq_filter=""
endpoint=""
draft_field=""
make_latest_field=""
while [[ "$#" -gt 0 ]]; do
    case "$1" in
        --method|-X)
            method="$2"
            shift 2
            ;;
        --jq)
            jq_filter="$2"
            shift 2
            ;;
        -F)
            if [[ "$2" == draft=* ]]; then
                draft_field="${2#draft=}"
            fi
            shift 2
            ;;
        -f)
            if [[ "$2" == make_latest=* ]]; then
                make_latest_field="${2#make_latest=}"
            fi
            shift 2
            ;;
        -H)
            shift 2
            ;;
        --paginate|--silent)
            shift
            ;;
        repos/*)
            endpoint="$1"
            shift
            ;;
        *)
            shift
            ;;
    esac
done

if [[ -z "${endpoint}" ]]; then
    echo "fake gh api endpoint is missing" >&2
    exit 64
fi
if [[ -n "${FORGERULES_TEST_API_ERROR_MATCH:-}" && "${endpoint}" == *"${FORGERULES_TEST_API_ERROR_MATCH}"* ]]; then
    echo "${FORGERULES_TEST_API_ERROR_MESSAGE:-gh: injected API failure (HTTP 503)}" >&2
    exit "${FORGERULES_TEST_API_ERROR_STATUS:-1}"
fi

release_id_from_state() {
    jq -r '.id' "${release_file}"
}

release_tag_from_state() {
    jq -r '.tag_name' "${release_file}"
}

emit_release() {
    if [[ ! -f "${release_file}" ]]; then
        not_found
    fi
    case "${jq_filter}" in
        '.id')
            release_id_from_state
            ;;
        *'@tsv'*)
            jq -r '[.id, .tag_name, .draft, .target_commitish] | @tsv' "${release_file}"
            ;;
        *)
            cat "${release_file}"
            ;;
    esac
}

if [[ "${method}" == "PATCH" && "${endpoint}" =~ /releases/([0-9]+)$ ]]; then
    if [[ ! -f "${release_file}" || "$(release_id_from_state)" != "${BASH_REMATCH[1]}" ]]; then
        not_found
    fi
    expected_make_latest="false"
    if [[ "$(release_tag_from_state)" == "latest" ]]; then
        expected_make_latest="true"
    fi
    if [[ "${draft_field}" != "false" || "${make_latest_field}" != "${expected_make_latest}" ]]; then
        echo "fake gh: invalid publish fields draft=${draft_field:-missing} make_latest=${make_latest_field:-missing}; expected false/${expected_make_latest}" >&2
        exit 1
    fi
    temp_release="${FORGERULES_TEST_STATE_DIR}/release.next.json"
    jq '.draft = false' "${release_file}" >"${temp_release}"
    mv "${temp_release}" "${release_file}"
    jq -n --arg sha "$(jq -r '.target_commitish' "${release_file}")" '{sha: $sha}' >"${tag_file}"
    cat "${release_file}"
    exit 0
fi

if [[ "${method}" == "DELETE" && "${endpoint}" =~ /releases/([0-9]+)$ ]]; then
    if [[ ! -f "${release_file}" || "$(release_id_from_state)" != "${BASH_REMATCH[1]}" ]]; then
        not_found
    fi
    rm -f "${release_file}"
    exit 0
fi

if [[ "${method}" == "DELETE" && "${endpoint}" == */git/refs/tags/* ]]; then
    if [[ ! -f "${tag_file}" ]]; then
        not_found
    fi
    rm -f "${tag_file}"
    exit 0
fi

if [[ "${endpoint}" =~ /releases/([0-9]+)/assets\?per_page=100$ ]]; then
    if [[ ! -f "${release_file}" || "$(release_id_from_state)" != "${BASH_REMATCH[1]}" ]]; then
        not_found
    fi
    printf '['
    first=true
    asset_id=1000
    for asset_path in "${FORGERULES_TEST_ARTIFACTS}"/*; do
        asset_name="$(basename "${asset_path}")"
        if [[ "${asset_name}" == "${FORGERULES_TEST_OMIT_ASSET:-}" ]]; then
            asset_id="$((asset_id + 1))"
            continue
        fi
        if [[ "${first}" == "false" ]]; then
            printf ','
        fi
        first=false
        jq -cn --argjson id "${asset_id}" --arg name "${asset_name}" '{id: $id, name: $name}'
        asset_id="$((asset_id + 1))"
    done
    printf ']\n'
    exit 0
fi

if [[ "${endpoint}" =~ /releases/assets/([0-9]+)$ ]]; then
    requested_id="${BASH_REMATCH[1]}"
    asset_id=1000
    for asset_path in "${FORGERULES_TEST_ARTIFACTS}"/*; do
        if [[ "${asset_id}" -eq "${requested_id}" ]]; then
            cat "${asset_path}"
            if [[ "$(basename "${asset_path}")" == "${FORGERULES_TEST_CORRUPT_ASSET:-}" ]]; then
                printf 'corrupt\n'
            fi
            exit 0
        fi
        asset_id="$((asset_id + 1))"
    done
    not_found
fi

if [[ "${endpoint}" =~ /releases/tags/(.+)$ ]]; then
    if [[ ! -f "${release_file}" || "$(release_tag_from_state)" != "${BASH_REMATCH[1]}" ]]; then
        not_found
    fi
    emit_release
    exit 0
fi

if [[ "${endpoint}" =~ /releases/([0-9]+)$ ]]; then
    if [[ ! -f "${release_file}" || "$(release_id_from_state)" != "${BASH_REMATCH[1]}" ]]; then
        not_found
    fi
    emit_release
    exit 0
fi

if [[ "${endpoint}" =~ /commits/(.+)$ ]]; then
    resolved_sha=""
    if [[ -n "${FORGERULES_TEST_BRANCH_SHA:-}" ]]; then
        resolved_sha="${FORGERULES_TEST_BRANCH_SHA}"
    elif [[ -f "${tag_file}" ]]; then
        resolved_sha="$(jq -r '.sha' "${tag_file}")"
    else
        not_found
    fi
    if [[ "${jq_filter}" == ".sha" ]]; then
        printf '%s\n' "${resolved_sha}"
    else
        jq -n --arg sha "${resolved_sha}" '{sha: $sha}'
    fi
    exit 0
fi

if [[ "${endpoint}" == */git/ref/tags/* ]]; then
    if [[ ! -f "${tag_file}" ]]; then
        not_found
    fi
    jq -n \
        --arg type "$(jq -r '.type // "commit"' "${tag_file}")" \
        --arg sha "$(jq -r '.sha' "${tag_file}")" \
        '{object: {type: $type, sha: $sha}}'
    exit 0
fi

if [[ "${endpoint}" =~ /git/tags/([0-9a-f]{40})$ ]]; then
    if [[ ! -f "${annotated_tag_file}" || "$(jq -r '.tag_sha' "${annotated_tag_file}")" != "${BASH_REMATCH[1]}" ]]; then
        not_found
    fi
    jq '{object}' "${annotated_tag_file}"
    exit 0
fi

echo "unsupported fake gh api invocation: ${method} ${endpoint}" >&2
exit 64
