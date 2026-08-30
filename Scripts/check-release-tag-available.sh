#!/usr/bin/env bash

set -euo pipefail

if [[ "$#" -ne 2 ]]; then
    echo "usage: $0 <git-remote> <rules-YYYYMMDD>" >&2
    exit 64
fi

readonly remote="$1"
readonly release_tag="$2"

if [[ -z "${remote}" ]]; then
    echo "error: git remote is empty" >&2
    exit 64
fi
if [[ ! "${release_tag}" =~ ^rules-[0-9]{8}$ ]]; then
    echo "error: release tag must match rules-YYYYMMDD" >&2
    exit 64
fi

set +e
git ls-remote \
    --exit-code \
    --tags \
    "${remote}" \
    "refs/tags/${release_tag}" >/dev/null 2>&1
readonly query_status="$?"
set -e

case "${query_status}" in
    0)
        echo "error: immutable release tag already exists: ${release_tag}" >&2
        exit 1
        ;;
    2)
        echo "Release tag is available: ${release_tag}"
        ;;
    *)
        echo "error: unable to query release tag from remote: ${remote}" >&2
        exit 1
        ;;
esac
