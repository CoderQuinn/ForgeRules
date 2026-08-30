#!/usr/bin/env bash

set -euo pipefail

readonly minimum_coverage="95.0"
readonly coverage_root="$(mktemp -d /tmp/forgerules-go-coverage.XXXXXX)"
readonly coverage_profile="${coverage_root}/coverage.out"

cleanup() {
    rm -rf "${coverage_root}"
}
trap cleanup EXIT

# First-party coverage includes every handwritten Go production package under
# cmd/forgerules and pkg. The generated proto/*.pb.go files are compiler output
# from the checked-in .proto contracts and are deliberately not instrumented.
# All packages still compile and run through ./... below.
go test \
    -count=1 \
    -covermode=atomic \
    -coverpkg=./cmd/forgerules,./pkg/... \
    -coverprofile="${coverage_profile}" \
    ./...

readonly total_line="$(go tool cover -func="${coverage_profile}" | awk '$1 == "total:" { print $NF }')"
readonly actual_coverage="${total_line%%%}"
if [[ -z "${actual_coverage}" ]]; then
    echo "error: unable to read total first-party production coverage" >&2
    exit 1
fi

printf 'First-party handwritten production coverage: %s%% (required: %s%%)\n' \
    "${actual_coverage}" \
    "${minimum_coverage}"

if ! awk \
    -v actual="${actual_coverage}" \
    -v minimum="${minimum_coverage}" \
    'BEGIN { exit !(actual + 0 >= minimum + 0) }'; then
    echo "error: first-party handwritten production coverage is below ${minimum_coverage}%" >&2
    exit 1
fi
