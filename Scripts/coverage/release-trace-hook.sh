#!/usr/bin/env bash

# This file is copied byte-for-byte to BASH_ENV. Its own coverage uses xtrace,
# independently of the DEBUG events it emits for the program under test.
_forgerules_mark_failure() {
    if [[ -n "${FORGERULES_COVERAGE_FAILURE:-}" ]]; then
        printf '%s\n' "${BASHPID:-$$}" 2>/dev/null >>"${FORGERULES_COVERAGE_FAILURE}" || :
    fi
    return 0
}

if [[ "$BASH_VERSION" != 5.3.15\(* ]]; then
    printf '%s\n' "$BASH_VERSION" 2>/dev/null >"${FORGERULES_COVERAGE_EVENTS}/${BASHPID:-$$}.unsupported" || _forgerules_mark_failure
    return 0
fi

# The collector pins this dependency. Standalone negative fixtures may inject
# a failing executable without changing these authored hook bytes.
readonly __forgerules_hasher="${FORGERULES_COVERAGE_HASHER:-/usr/bin/openssl}"

_forgerules_emit_debug() {
    local __forge_source="$1" __forge_row="$2" __forge_command="$3" __forge_fn="$4"
    local __forge_sha="missing" __forge_definition_id="" __forge_definition=""
    set +T
    if [[ -n "$__forge_source" && -f "$__forge_source" ]]; then
        if __forge_sha="$("${__forgerules_hasher}" dgst -sha256 -r "$__forge_source" 2>/dev/null)"; then
            __forge_sha="${__forge_sha%% *}"
        else
            __forge_sha="missing"
        fi
    fi
    if [[ -n "$__forge_fn" ]]; then
        if __forge_definition="$(declare -f "$__forge_fn")"; then
            if __forge_definition_id="$(printf '%s' "$__forge_definition" | "${__forgerules_hasher}" dgst -sha256 -r)"; then
                __forge_definition_id="${__forge_definition_id%% *}"
                if [[ ! -f "${FORGERULES_COVERAGE_EVENTS}/${__forge_definition_id}.definition" ]]; then
                    printf '%s' "$__forge_definition" 2>/dev/null >"${FORGERULES_COVERAGE_EVENTS}/${__forge_definition_id}.definition" || _forgerules_mark_failure
                fi
            fi
        elif [[ "$__forge_fn" == source || "$__forge_fn" == main ]]; then
            __forge_fn=""
        fi
    fi
    set -T
    printf '%s\0%s\0%s\0%s\0%s\0%s\0%s\0' "${__forge_source:-@bash-c:$0}" "$__forge_row" "$__forge_command" "$__forge_sha" "$BASH_VERSION" "$__forge_fn" "$__forge_definition_id" 2>/dev/null >>"${FORGERULES_COVERAGE_EVENTS}/${BASHPID}.events" || _forgerules_mark_failure
    return 0
}

set -T
trap '_forgerules_emit_debug "${BASH_SOURCE[0]-}" "$LINENO" "$BASH_COMMAND" "${FUNCNAME[0]-}"' DEBUG
