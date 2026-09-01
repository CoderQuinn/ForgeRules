package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CoderQuinn/ForgeRules/pkg/geoip"
)

func TestProductionDependenciesAreComplete(t *testing.T) {
	t.Parallel()

	dependencies := productionDependencies()
	if dependencies.download == nil ||
		dependencies.convertGeoSite == nil ||
		dependencies.convertGeoIP == nil ||
		dependencies.writeBuildMetadata == nil ||
		dependencies.runtimeVersion == nil {
		t.Fatalf("production dependencies are incomplete: %#v", dependencies)
	}
}

func TestRunReportsUsageAndArgumentErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		args        []string
		wantStatus  int
		wantMessage string
	}{
		{
			name:        "help",
			args:        []string{"-h"},
			wantStatus:  0,
			wantMessage: "ForgeRules - Convert",
		},
		{
			name:        "unknown flag",
			args:        []string{"-unknown"},
			wantStatus:  2,
			wantMessage: "flag provided but not defined",
		},
		{
			name: "auto build epoch override",
			args: []string{
				"-geoip-build-epoch=1",
				"-converter-revision=" + strings.Repeat("a", 40),
			},
			wantStatus:  2,
			wantMessage: "cannot override a pinned source lock",
		},
		{
			name:        "auto missing converter revision",
			args:        nil,
			wantStatus:  2,
			wantMessage: "must be a lowercase 40-character commit SHA",
		},
		{
			name: "missing source lock",
			args: []string{
				"-sources-lock=missing.json",
				"-converter-revision=" + strings.Repeat("a", 40),
			},
			wantStatus:  1,
			wantMessage: "Error loading source lock",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			status := run(test.args, &stdout, &stderr, successfulCLIDependencies())
			if status != test.wantStatus {
				t.Errorf("status = %d, want %d", status, test.wantStatus)
			}
			if !strings.Contains(stderr.String(), test.wantMessage) {
				t.Errorf("stderr = %q, want substring %q", stderr.String(), test.wantMessage)
			}
		})
	}
}

func TestRunAutomaticPinnedBuild(t *testing.T) {
	t.Parallel()

	lockPath := writeCLITestSourceLock(t)
	revision := strings.Repeat("c", 40)
	var downloads []string
	var convertedGeoSite bool
	var convertedGeoIP bool
	var wroteMetadata bool
	dependencies := successfulCLIDependencies()
	dependencies.download = func(_ string, output string, _ string, _ int64) error {
		downloads = append(downloads, output)
		return nil
	}
	dependencies.convertGeoSite = func(input, output string) error {
		convertedGeoSite = input == "official_geosite.dat" && output == "official_geosite.json"
		return nil
	}
	dependencies.convertGeoIP = func(input, output string, options geoip.MMDBOptions) error {
		convertedGeoIP = input == "official_geoip.dat" &&
			output == "official_geoip.mmdb" &&
			options.BuildEpoch == 1_700_000_000
		return nil
	}
	dependencies.writeBuildMetadata = func(
		outputDirectory, sourceLockPath string,
		lock sourceLock,
		converterRevision, goVersion string,
	) error {
		wroteMetadata = outputDirectory == "." &&
			sourceLockPath == lockPath &&
			len(lock.Sources) == 1 &&
			converterRevision == revision &&
			goVersion == "go-test"
		return nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	status := run(
		[]string{"-sources-lock=" + lockPath, "-converter-revision=" + revision},
		&stdout,
		&stderr,
		dependencies,
	)
	if status != 0 {
		t.Fatalf("status = %d, stderr = %q", status, stderr.String())
	}
	if len(downloads) != 2 || downloads[0] != "official_geosite.dat" || downloads[1] != "official_geoip.dat" {
		t.Errorf("downloads = %v", downloads)
	}
	if !convertedGeoSite || !convertedGeoIP || !wroteMetadata {
		t.Errorf(
			"stages: geosite=%t geoip=%t metadata=%t",
			convertedGeoSite,
			convertedGeoIP,
			wroteMetadata,
		)
	}
	if !strings.Contains(stdout.String(), "Upstream conversion completed!") {
		t.Errorf("stdout = %q", stdout.String())
	}
}

func TestRunAutomaticPinnedBuildFailsClosedAtEveryStage(t *testing.T) {
	t.Parallel()

	lockPath := writeCLITestSourceLock(t)
	baseArgs := []string{
		"-sources-lock=" + lockPath,
		"-converter-revision=" + strings.Repeat("d", 40),
	}
	tests := []struct {
		name        string
		configure   func(*cliDependencies)
		wantMessage string
	}{
		{
			name: "geosite download",
			configure: func(dependencies *cliDependencies) {
				dependencies.download = func(_ string, output string, _ string, _ int64) error {
					if strings.HasSuffix(output, "_geosite.dat") {
						return errors.New("download geosite failed")
					}
					return nil
				}
			},
			wantMessage: "Error downloading official geosite",
		},
		{
			name: "geoip download",
			configure: func(dependencies *cliDependencies) {
				dependencies.download = func(_ string, output string, _ string, _ int64) error {
					if strings.HasSuffix(output, "_geoip.dat") {
						return errors.New("download geoip failed")
					}
					return nil
				}
			},
			wantMessage: "Error downloading official geoip",
		},
		{
			name: "geosite conversion",
			configure: func(dependencies *cliDependencies) {
				dependencies.convertGeoSite = func(string, string) error {
					return errors.New("convert geosite failed")
				}
			},
			wantMessage: "Error converting official geosite",
		},
		{
			name: "geoip conversion",
			configure: func(dependencies *cliDependencies) {
				dependencies.convertGeoIP = func(string, string, geoip.MMDBOptions) error {
					return errors.New("convert geoip failed")
				}
			},
			wantMessage: "Error converting official geoip",
		},
		{
			name: "metadata",
			configure: func(dependencies *cliDependencies) {
				dependencies.writeBuildMetadata = func(string, string, sourceLock, string, string) error {
					return errors.New("metadata failed")
				}
			},
			wantMessage: "Error writing build metadata",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			dependencies := successfulCLIDependencies()
			test.configure(&dependencies)
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			status := run(baseArgs, &stdout, &stderr, dependencies)
			if status != 1 {
				t.Errorf("status = %d, want 1", status)
			}
			if !strings.Contains(stderr.String(), test.wantMessage) {
				t.Errorf("stderr = %q, want substring %q", stderr.String(), test.wantMessage)
			}
			if strings.Contains(stdout.String(), "Upstream conversion completed!") {
				t.Errorf("failed build reported success: %q", stdout.String())
			}
		})
	}
}

func TestRunManualConversion(t *testing.T) {
	t.Parallel()

	var geositeArguments []string
	var geoipInput string
	var geoipOutput string
	var buildEpoch int64
	dependencies := successfulCLIDependencies()
	dependencies.convertGeoSite = func(input, output string) error {
		geositeArguments = []string{input, output}
		return nil
	}
	dependencies.convertGeoIP = func(input, output string, options geoip.MMDBOptions) error {
		geoipInput = input
		geoipOutput = output
		buildEpoch = options.BuildEpoch
		return nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	status := run([]string{
		"-geosite-input=input-geosite.dat",
		"-geosite-output=output-geosite.json",
		"-geoip-input=input-geoip.dat",
		"-geoip-output=output-geoip.mmdb",
		"-geoip-build-epoch=1700000000",
	}, &stdout, &stderr, dependencies)
	if status != 0 {
		t.Fatalf("status = %d, stderr = %q", status, stderr.String())
	}
	if len(geositeArguments) != 2 || geositeArguments[0] != "input-geosite.dat" || geositeArguments[1] != "output-geosite.json" {
		t.Errorf("geosite arguments = %v", geositeArguments)
	}
	if geoipInput != "input-geoip.dat" || geoipOutput != "output-geoip.mmdb" || buildEpoch != 1_700_000_000 {
		t.Errorf("geoip arguments = %q, %q, %d", geoipInput, geoipOutput, buildEpoch)
	}
	if !strings.Contains(stdout.String(), "Conversion completed successfully!") {
		t.Errorf("stdout = %q", stdout.String())
	}
}

func TestRunManualConversionReportsErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		args        []string
		configure   func(*cliDependencies)
		wantMessage string
	}{
		{
			name: "geosite",
			args: []string{"-geosite-input=input.dat"},
			configure: func(dependencies *cliDependencies) {
				dependencies.convertGeoSite = func(string, string) error {
					return errors.New("geosite failed")
				}
			},
			wantMessage: "Error converting geosite",
		},
		{
			name: "geoip",
			args: []string{"-geoip-input=input.dat"},
			configure: func(dependencies *cliDependencies) {
				dependencies.convertGeoIP = func(string, string, geoip.MMDBOptions) error {
					return errors.New("geoip failed")
				}
			},
			wantMessage: "Error converting geoip",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			dependencies := successfulCLIDependencies()
			test.configure(&dependencies)
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			status := run(test.args, &stdout, &stderr, dependencies)
			if status != 1 {
				t.Errorf("status = %d, want 1", status)
			}
			if !strings.Contains(stderr.String(), test.wantMessage) {
				t.Errorf("stderr = %q, want substring %q", stderr.String(), test.wantMessage)
			}
			if strings.Contains(stdout.String(), "Conversion completed successfully!") {
				t.Errorf("failed conversion reported success: %q", stdout.String())
			}
		})
	}
}

func successfulCLIDependencies() cliDependencies {
	return cliDependencies{
		download:       func(string, string, string, int64) error { return nil },
		convertGeoSite: func(string, string) error { return nil },
		convertGeoIP:   func(string, string, geoip.MMDBOptions) error { return nil },
		writeBuildMetadata: func(string, string, sourceLock, string, string) error {
			return nil
		},
		runtimeVersion: func() string { return "go-test" },
	}
}

func writeCLITestSourceLock(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rules.sources.lock.json")
	if err := os.WriteFile(path, []byte(validSourceLockJSON()), 0o600); err != nil {
		t.Fatalf("write source lock: %v", err)
	}
	return path
}
