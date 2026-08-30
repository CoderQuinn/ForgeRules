package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fixtureModule = "example.com/fixture"

func TestCalculateCountsMultilineExecutableTokenLines(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	source := `package sample

func evaluate(left, right bool) int {
	result := combine(
		left,
		right,
	)
	if result &&
		left {
		values := []int{
			1,
			2,
		}
		return values[0] +
			values[1]
	}
	return 0
}
`
	sourcePath := writeFixture(t, root, "pkg/sample.go", source)
	filesPath := writeFixture(t, root, "files.txt", sourcePath+"\n"+sourcePath+"\n")
	start, end := textSpan(t, source, "result := combine(", "return 0")
	profilePath := writeFixture(t, root, "coverage.out", "mode: atomic\n"+
		profileEntry("pkg/sample.go", source, start, end, 7, 1))

	report, err := calculate(fixtureConfig(root, filesPath, profilePath))
	if err != nil {
		t.Fatalf("calculate coverage: %v", err)
	}
	if report.covered != 11 || report.total != 11 {
		t.Fatalf("report = %#v, want 11/11 multiline executable token lines", report)
	}
}

func TestCalculateExcludesStructuralClauseAndCommentLines(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	source := `package sample

func classify(value int, ch <-chan int) int {
	// A comment inside an instrumented range is not executable.
	switch value {
	case 1:
		return identity(
			1,
		)
	default:
		return 0
	}
	select {
	case received := <-ch:
		return received
	default:
		return 0
	}
}
`
	sourcePath := writeFixture(t, root, "pkg/sample.go", source)
	filesPath := writeFixture(t, root, "files.txt", sourcePath+"\n")
	start := strings.Index(source, "switch value")
	end := strings.LastIndex(source, "return 0") + len("return 0")
	profilePath := writeFixture(t, root, "coverage.out", "mode: count\n"+
		profileEntry("pkg/sample.go", source, start, end, 7, 1))

	report, err := calculate(fixtureConfig(root, filesPath, profilePath))
	if err != nil {
		t.Fatalf("calculate coverage: %v", err)
	}
	if report.covered != 7 || report.total != 7 {
		t.Fatalf("report = %#v, want seven non-structural executable lines", report)
	}
}

func TestCalculateDeduplicatesLineAndORsCoveredBlocks(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	source := "package sample\nfunc same() { println(\"first\"); println(\"second\") }\n"
	sourcePath := writeFixture(t, root, "pkg/sample.go", source)
	filesPath := writeFixture(t, root, "files.txt", sourcePath+"\n")
	firstStart, firstEnd := textSpan(t, source, "println(\"first\")", "println(\"first\")")
	secondStart, secondEnd := textSpan(t, source, "println(\"second\")", "println(\"second\")")
	profile := "mode: set\n" +
		profileEntry("pkg/sample.go", source, firstStart, firstEnd, 1, 0) +
		profileEntry("pkg/sample.go", source, secondStart, secondEnd, 1, 1) +
		profileEntry("pkg/sample.go", source, firstStart, secondEnd, 0, 1)
	profilePath := writeFixture(t, root, "coverage.out", profile)

	report, err := calculate(fixtureConfig(root, filesPath, profilePath))
	if err != nil {
		t.Fatalf("calculate coverage: %v", err)
	}
	if report.covered != 1 || report.total != 1 {
		t.Fatalf("report = %#v, want one covered unique line", report)
	}
}

func TestCalculateRejectsZeroStatementOwnership(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	source := "package sample\nfunc work() { println(\"only\") }\n"
	sourcePath := writeFixture(t, root, "pkg/sample.go", source)
	filesPath := writeFixture(t, root, "files.txt", sourcePath+"\n")
	start, end := textSpan(t, source, "println(\"only\")", "println(\"only\")")
	profilePath := writeFixture(t, root, "coverage.out", "mode: atomic\n"+
		profileEntry("pkg/sample.go", source, start, end, 0, 1))

	_, err := calculate(fixtureConfig(root, filesPath, profilePath))
	if err == nil || !strings.Contains(err.Error(), "absent from coverage profile") {
		t.Fatalf("zero-statement ownership error = %v", err)
	}
}

func TestRunEnforcesThresholdAndPrintsUncoveredLines(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	source := `package sample

func work() {
	println("covered")
	println("missed")
}
`
	sourcePath := writeFixture(t, root, "pkg/sample.go", source)
	filesPath := writeFixture(t, root, "files.txt", sourcePath+"\n")
	coveredStart, coveredEnd := textSpan(t, source, "println(\"covered\")", "println(\"covered\")")
	missedStart, missedEnd := textSpan(t, source, "println(\"missed\")", "println(\"missed\")")
	profilePath := writeFixture(t, root, "coverage.out", "mode: set\n"+
		profileEntry("pkg/sample.go", source, coveredStart, coveredEnd, 1, 1)+
		profileEntry("pkg/sample.go", source, missedStart, missedEnd, 1, 0))
	baseArgs := []string{
		"-profile=" + profilePath,
		"-files=" + filesPath,
		"-module-root=" + root,
		"-module-path=" + fixtureModule,
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if status := run(append(baseArgs, "-minimum=50"), &stdout, &stderr); status != 0 {
		t.Fatalf("exact threshold status = %d, stderr = %q", status, stderr.String())
	}
	if !strings.Contains(stdout.String(), "1/2 (50.00%") ||
		!strings.Contains(stdout.String(), "- pkg/sample.go:5") {
		t.Errorf("stdout = %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if status := run(append(baseArgs, "-minimum=50.01"), &stdout, &stderr); status != 1 {
		t.Fatalf("below-threshold status = %d, want 1", status)
	}
	if !strings.Contains(stdout.String(), "- pkg/sample.go:5") {
		t.Fatalf("below-threshold stdout lacks uncovered line: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "below the required threshold") {
		t.Fatalf("below-threshold stderr = %q", stderr.String())
	}
}

func TestRunSortsUncoveredFilesDeterministically(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	aSource := "package sample\nfunc a() { println(\"a\") }\n"
	bSource := "package sample\nfunc b() { println(\"b\") }\n"
	aPath := writeFixture(t, root, "pkg/a.go", aSource)
	bPath := writeFixture(t, root, "pkg/b.go", bSource)
	filesPath := writeFixture(t, root, "files.txt", bPath+"\n"+aPath+"\n")
	aStart, aEnd := textSpan(t, aSource, "println(\"a\")", "println(\"a\")")
	bStart, bEnd := textSpan(t, bSource, "println(\"b\")", "println(\"b\")")
	profilePath := writeFixture(t, root, "coverage.out", "mode: atomic\n"+
		profileEntry("pkg/b.go", bSource, bStart, bEnd, 1, 0)+
		profileEntry("pkg/a.go", aSource, aStart, aEnd, 1, 0))

	var stdout bytes.Buffer
	status := run([]string{
		"-profile=" + profilePath,
		"-files=" + filesPath,
		"-module-root=" + root,
		"-module-path=" + fixtureModule,
		"-minimum=0",
	}, &stdout, &bytes.Buffer{})
	if status != 0 {
		t.Fatalf("status = %d", status)
	}
	aIndex := strings.Index(stdout.String(), "- pkg/a.go:2")
	bIndex := strings.Index(stdout.String(), "- pkg/b.go:2")
	if aIndex < 0 || bIndex < 0 || aIndex >= bIndex {
		t.Fatalf("uncovered output is not deterministically sorted: %q", stdout.String())
	}
}

func TestCalculateRejectsMissingProfileAndOutOfRootFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	sourcePath := writeFixture(t, root, "pkg/sample.go", "package sample\nfunc value() int { return 1 }\n")
	filesPath := writeFixture(t, root, "files.txt", sourcePath+"\n")
	emptyProfile := writeFixture(t, root, "coverage.out", "mode: atomic\n")
	_, err := calculate(fixtureConfig(root, filesPath, emptyProfile))
	if err == nil || !strings.Contains(err.Error(), "absent from coverage profile") {
		t.Fatalf("missing profile error = %v", err)
	}

	outside := writeFixture(t, t.TempDir(), "outside.go", "package outside\n")
	outsideFiles := writeFixture(t, root, "outside-files.txt", outside+"\n")
	_, err = calculate(fixtureConfig(root, outsideFiles, emptyProfile))
	if err == nil || !strings.Contains(err.Error(), "outside module root") {
		t.Fatalf("outside-root error = %v", err)
	}
}

func TestCalculateRejectsTruncatedProfile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	source := `package sample

func work() {
	println("first")
	println("later")
}
`
	sourcePath := writeFixture(t, root, "pkg/sample.go", source)
	filesPath := writeFixture(t, root, "files.txt", sourcePath+"\n")
	start, end := textSpan(t, source, "println(\"first\")", "println(\"first\")")
	profilePath := writeFixture(t, root, "coverage.out", "mode: atomic\n"+
		profileEntry("pkg/sample.go", source, start, end, 1, 1))

	_, err := calculate(fixtureConfig(root, filesPath, profilePath))
	if err == nil || !strings.Contains(err.Error(), "executable token has no coverage block") {
		t.Fatalf("truncated profile error = %v", err)
	}
}

func fixtureConfig(root, filesPath, profilePath string) calculatorConfig {
	return calculatorConfig{
		profilePath: profilePath,
		filesPath:   filesPath,
		moduleRoot:  root,
		modulePath:  fixtureModule,
		minimum:     0,
	}
}

func textSpan(t *testing.T, source, startText, endText string) (int, int) {
	t.Helper()
	start := strings.Index(source, startText)
	if start < 0 {
		t.Fatalf("start text %q not found", startText)
	}
	relativeEnd := strings.Index(source[start:], endText)
	if relativeEnd < 0 {
		t.Fatalf("end text %q not found after %q", endText, startText)
	}
	return start, start + relativeEnd + len(endText)
}

func profileEntry(moduleFile, source string, start, end, statements, count int) string {
	startLine, startColumn := fixtureSourcePosition(source, start)
	endLine, endColumn := fixtureSourcePosition(source, end)
	return fmt.Sprintf(
		"%s/%s:%d.%d,%d.%d %d %d\n",
		fixtureModule,
		moduleFile,
		startLine,
		startColumn,
		endLine,
		endColumn,
		statements,
		count,
	)
}

func fixtureSourcePosition(source string, offset int) (line, column int) {
	prefix := source[:offset]
	line = strings.Count(prefix, "\n") + 1
	lastNewline := strings.LastIndex(prefix, "\n")
	if lastNewline < 0 {
		return line, offset + 1
	}
	return line, offset - lastNewline
}

func writeFixture(t *testing.T, root, relative, contents string) string {
	t.Helper()
	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create fixture directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write fixture %s: %v", relative, err)
	}
	return path
}
