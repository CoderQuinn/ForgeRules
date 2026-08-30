#!/usr/bin/env bash

set -euo pipefail

if [[ -z "${FORGERULES_TEST_ARTIFACTS:-}" ]]; then
    echo "FORGERULES_TEST_ARTIFACTS is required" >&2
    exit 64
fi

if [[ "$#" -ge 2 && "$1" == "release" && "$2" == "view" ]]; then
    for asset_path in "${FORGERULES_TEST_ARTIFACTS}"/*; do
        asset_name="$(basename "${asset_path}")"
        if [[ "${asset_name}" != "${FORGERULES_TEST_OMIT_ASSET:-}" ]]; then
            printf '%s\n' "${asset_name}"
        fi
    done
    exit 0
fi

if [[ "$#" -ge 2 && "$1" == "release" && "$2" == "download" ]]; then
    shift 2
    download_directory=""
    while [[ "$#" -gt 0 ]]; do
        if [[ "$1" == "--dir" && "$#" -ge 2 ]]; then
            download_directory="$2"
            shift 2
        else
            shift
        fi
    done
    if [[ -z "${download_directory}" ]]; then
        echo "fake gh release download requires --dir" >&2
        exit 64
    fi
    cp "${FORGERULES_TEST_ARTIFACTS}"/* "${download_directory}/"
    if [[ -n "${FORGERULES_TEST_CORRUPT_ASSET:-}" ]]; then
        printf 'corrupt\n' >>"${download_directory}/${FORGERULES_TEST_CORRUPT_ASSET}"
    fi
    exit 0
fi

echo "unsupported fake gh invocation: $*" >&2
exit 64
