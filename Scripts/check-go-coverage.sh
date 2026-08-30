#!/usr/bin/env bash

set -euo pipefail

readonly minimum_coverage="95.0"
readonly coverage_root="$(mktemp -d /tmp/forgerules-go-coverage.XXXXXX)"
readonly coverage_profile="${coverage_root}/coverage.out"
readonly production_files="${coverage_root}/production-files.txt"
readonly script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly repository_root="$(cd "${script_directory}/.." && pwd)"

cleanup() {
    rm -rf "${coverage_root}"
}
trap cleanup EXIT

(
    cd "${repository_root}"

    # go list defines the complete active handwritten production source set.
    # cmd/forgerules and every package below pkg are mandatory roots. Generated
    # proto/*.pb.go stays outside the measured roots, while ./... still compiles
    # and tests it below.
    go list \
        -f '{{range .GoFiles}}{{printf "%s/%s\n" $.Dir .}}{{end}}{{range .CgoFiles}}{{printf "%s/%s\n" $.Dir .}}{{end}}' \
        ./cmd/forgerules \
        ./pkg/... | LC_ALL=C sort -u >"${production_files}"

    go test \
        -count=1 \
        -covermode=atomic \
        -coverpkg=./cmd/forgerules,./pkg/... \
        -coverprofile="${coverage_profile}" \
        ./...

    go run ./Scripts/coverage \
        -profile="${coverage_profile}" \
        -files="${production_files}" \
        -module-root="${repository_root}" \
        -module-path="$(go list -m -f '{{.Path}}')" \
        -minimum="${minimum_coverage}"
)
