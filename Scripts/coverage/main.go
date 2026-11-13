package main

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var profileLinePattern = regexp.MustCompile(
	`^(.+):([0-9]+)\.([0-9]+),([0-9]+)\.([0-9]+)[[:space:]]+([0-9]+)[[:space:]]+([0-9]+)$`,
)

type sourcePosition struct {
	line   int
	column int
}

type coverageBlock struct {
	start         sourcePosition
	end           sourcePosition
	numStatements uint64
	count         uint64
}

type offsetRange struct {
	start int
	end   int
}

type offsetCoverageBlock struct {
	offsetRange
	numStatements uint64
	count         uint64
}

type clauseExpression struct {
	offsetRange
	profileAnchor int
}

type sourceAnalysis struct {
	contents           []byte
	lineStarts         []int
	executable         []offsetRange
	statements         []offsetRange
	excluded           []offsetRange
	uninstrumentedByGo []offsetRange
	clauseExpressions  []clauseExpression
	semanticTokens     []offsetRange
}

type lineKey struct {
	file string
	line int
}

type coverageReport struct {
	covered   int
	total     int
	uncovered []lineKey
}

func (report coverageReport) percentage() float64 {
	if report.total == 0 {
		return 0
	}
	return float64(report.covered) * 100 / float64(report.total)
}

type calculatorConfig struct {
	profilePath string
	filesPath   string
	moduleRoot  string
	modulePath  string
	minimum     float64
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("forgerules-line-coverage", flag.ContinueOnError)
	flags.SetOutput(stderr)
	config := calculatorConfig{}
	flags.StringVar(&config.profilePath, "profile", "", "Go coverage profile")
	flags.StringVar(&config.filesPath, "files", "", "newline-delimited active production Go files")
	flags.StringVar(&config.moduleRoot, "module-root", "", "absolute or relative module root")
	flags.StringVar(&config.modulePath, "module-path", "", "Go module import path")
	flags.Float64Var(&config.minimum, "minimum", 95, "minimum Go-cover-profile-owned line coverage percentage")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	report, err := calculate(config)
	if err != nil {
		fmt.Fprintf(stderr, "error: calculate Go-cover-profile-owned line coverage: %v\n", err)
		return 1
	}
	actual := report.percentage()
	fmt.Fprintf(
		stdout,
		"First-party unique Go-cover-profile-owned line coverage: %d/%d (%.2f%%; required: %.2f%%)\n",
		report.covered,
		report.total,
		actual,
		config.minimum,
	)
	writeUncoveredLines(stdout, report.uncovered, config.moduleRoot)
	if actual+1e-9 < config.minimum {
		fmt.Fprintln(stderr, "error: first-party unique Go-cover-profile-owned line coverage is below the required threshold")
		return 1
	}
	return 0
}

func calculate(config calculatorConfig) (coverageReport, error) {
	if config.profilePath == "" || config.filesPath == "" || config.moduleRoot == "" || config.modulePath == "" {
		return coverageReport{}, errors.New("profile, files, module-root, and module-path are required")
	}
	if config.minimum < 0 || config.minimum > 100 {
		return coverageReport{}, fmt.Errorf("minimum %.2f is outside 0...100", config.minimum)
	}
	moduleRoot, err := filepath.Abs(config.moduleRoot)
	if err != nil {
		return coverageReport{}, fmt.Errorf("resolve module root: %w", err)
	}
	files, err := readProductionFiles(config.filesPath, moduleRoot)
	if err != nil {
		return coverageReport{}, err
	}
	blocks, err := readCoverageProfile(config.profilePath, moduleRoot, config.modulePath)
	if err != nil {
		return coverageReport{}, err
	}

	lineCoverage := make(map[lineKey]bool)
	for _, file := range files {
		analysis, err := analyzeSource(file)
		if err != nil {
			return coverageReport{}, err
		}
		hasGoCoverableStatements := hasGoCoverableStatementTokens(analysis)
		profileBlocks := blocks[file]
		if hasGoCoverableStatements && len(positiveStatementCoverageBlocks(profileBlocks)) == 0 {
			return coverageReport{}, fmt.Errorf("active production file is absent from coverage profile: %s", file)
		}
		measured, err := measureSourceLines(file, analysis, profileBlocks)
		if err != nil {
			return coverageReport{}, err
		}
		if hasGoCoverableStatements && len(measured) == 0 {
			return coverageReport{}, fmt.Errorf(
				"active production file has no Go-coverable overlap with its coverage profile: %s",
				file,
			)
		}
		for line, covered := range measured {
			key := lineKey{file: file, line: line}
			lineCoverage[key] = lineCoverage[key] || covered
		}
	}

	report := coverageReport{total: len(lineCoverage)}
	if report.total == 0 {
		return coverageReport{}, errors.New("no Go-cover-profile-owned production lines were found")
	}
	for key, covered := range lineCoverage {
		if covered {
			report.covered++
		} else {
			report.uncovered = append(report.uncovered, key)
		}
	}
	sort.Slice(report.uncovered, func(left, right int) bool {
		if report.uncovered[left].file == report.uncovered[right].file {
			return report.uncovered[left].line < report.uncovered[right].line
		}
		return report.uncovered[left].file < report.uncovered[right].file
	})
	return report, nil
}

func writeUncoveredLines(output io.Writer, uncovered []lineKey, moduleRoot string) {
	if len(uncovered) == 0 {
		fmt.Fprintln(output, "Uncovered Go-cover-profile-owned lines: none")
		return
	}
	fmt.Fprintln(output, "Uncovered Go-cover-profile-owned lines:")
	root, err := filepath.Abs(moduleRoot)
	if err != nil {
		root = moduleRoot
	}
	for _, key := range uncovered {
		display := key.file
		if relative, relativeErr := filepath.Rel(root, key.file); relativeErr == nil {
			display = relative
		}
		fmt.Fprintf(output, "- %s:%d\n", filepath.ToSlash(display), key.line)
	}
}

func readProductionFiles(path, moduleRoot string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open production file manifest: %w", err)
	}
	defer file.Close()

	unique := make(map[string]struct{})
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		value := strings.TrimSpace(scanner.Text())
		if value == "" {
			continue
		}
		absolute, err := filepath.Abs(value)
		if err != nil {
			return nil, fmt.Errorf("resolve production file %q: %w", value, err)
		}
		if err := requireWithinModule(absolute, moduleRoot); err != nil {
			return nil, err
		}
		if filepath.Ext(absolute) != ".go" || strings.HasSuffix(absolute, "_test.go") {
			return nil, fmt.Errorf("manifest contains a non-production Go file: %s", absolute)
		}
		unique[filepath.Clean(absolute)] = struct{}{}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read production file manifest: %w", err)
	}
	if len(unique) == 0 {
		return nil, errors.New("production file manifest is empty")
	}

	files := make([]string, 0, len(unique))
	for value := range unique {
		files = append(files, value)
	}
	sort.Strings(files)
	return files, nil
}

func requireWithinModule(path, moduleRoot string) error {
	relative, err := filepath.Rel(moduleRoot, path)
	if err != nil {
		return fmt.Errorf("compare production file with module root: %w", err)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("production file is outside module root: %s", path)
	}
	return nil
}

func readCoverageProfile(path, moduleRoot, modulePath string) (map[string][]coverageBlock, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open coverage profile: %w", err)
	}
	defer file.Close()

	blocks := make(map[string][]coverageBlock)
	scanner := bufio.NewScanner(file)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if lineNumber == 1 {
			if line != "mode: set" && line != "mode: count" && line != "mode: atomic" {
				return nil, fmt.Errorf("unsupported coverage profile mode %q", line)
			}
			continue
		}
		if line == "" {
			continue
		}
		matches := profileLinePattern.FindStringSubmatch(line)
		if matches == nil {
			return nil, fmt.Errorf("invalid coverage profile line %d: %s", lineNumber, line)
		}
		profileFile, err := resolveProfileFile(matches[1], moduleRoot, modulePath)
		if err != nil {
			return nil, err
		}
		values := make([]uint64, 0, 6)
		for _, match := range matches[2:] {
			value, err := strconv.ParseUint(match, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("parse coverage profile line %d: %w", lineNumber, err)
			}
			values = append(values, value)
		}
		blocks[profileFile] = append(blocks[profileFile], coverageBlock{
			start:         sourcePosition{line: int(values[0]), column: int(values[1])},
			end:           sourcePosition{line: int(values[2]), column: int(values[3])},
			numStatements: values[4],
			count:         values[5],
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read coverage profile: %w", err)
	}
	if lineNumber == 0 {
		return nil, errors.New("coverage profile is empty")
	}
	return blocks, nil
}

func positiveStatementCoverageBlocks(blocks []coverageBlock) []coverageBlock {
	instrumented := make([]coverageBlock, 0, len(blocks))
	for _, block := range blocks {
		if block.numStatements > 0 {
			instrumented = append(instrumented, block)
		}
	}
	return instrumented
}

func resolveProfileFile(value, moduleRoot, modulePath string) (string, error) {
	var resolved string
	slashValue := filepath.ToSlash(value)
	if filepath.IsAbs(value) {
		resolved = filepath.Clean(value)
	} else if strings.HasPrefix(slashValue, modulePath+"/") {
		resolved = filepath.Join(moduleRoot, filepath.FromSlash(strings.TrimPrefix(slashValue, modulePath+"/")))
	} else {
		resolved = filepath.Join(moduleRoot, filepath.FromSlash(slashValue))
	}
	resolved = filepath.Clean(resolved)
	if err := requireWithinModule(resolved, moduleRoot); err != nil {
		return "", fmt.Errorf("coverage profile path: %w", err)
	}
	return resolved, nil
}

func analyzeSource(path string) (sourceAnalysis, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return sourceAnalysis{}, fmt.Errorf("read production Go file %s: %w", path, err)
	}

	fileSet := token.NewFileSet()
	parsed, err := parser.ParseFile(fileSet, path, contents, parser.ParseComments)
	if err != nil {
		return sourceAnalysis{}, fmt.Errorf("parse production Go file %s: %w", path, err)
	}
	parsedFile := fileSet.File(parsed.Pos())
	if parsedFile == nil {
		return sourceAnalysis{}, fmt.Errorf("locate parsed production Go file: %s", path)
	}

	analysis := sourceAnalysis{
		contents:   contents,
		lineStarts: sourceLineStarts(contents),
	}
	walkExecutableStatements(parsed, parsedFile, typeSwitchClauses(parsed), &analysis)
	analysis.statements = normalizeRanges(analysis.statements)
	analysis.excluded = normalizeRanges(analysis.excluded)
	analysis.executable = append(analysis.executable, analysis.statements...)
	for _, initializer := range packageScopeInitializerRanges(parsed, parsedFile) {
		analysis.executable = append(analysis.executable, initializer)
		pieces := []offsetRange{initializer}
		for _, statement := range analysis.statements {
			if statement.start < initializer.start || statement.end > initializer.end {
				continue
			}
			pieces = subtractRange(pieces, statement)
		}
		analysis.uninstrumentedByGo = append(analysis.uninstrumentedByGo, pieces...)
	}
	ast.Inspect(parsed, func(node ast.Node) bool {
		expression, ok := node.(ast.Expr)
		if !ok {
			return true
		}
		expressionRange := offsetRange{
			start: parsedFile.Offset(expression.Pos()),
			end:   parsedFile.Offset(expression.End()),
		}
		// Expressions inside executable statement spans retain their multiline
		// token lines. Package-scope variable initializers are added separately
		// because Go 1.24 does not emit coverage counters for those expressions.
		if len(allowedIntersections(expressionRange, analysis.statements, analysis.excluded)) > 0 {
			analysis.executable = append(analysis.executable, expressionRange)
		}
		return true
	})
	analysis.executable = normalizeRanges(analysis.executable)
	analysis.uninstrumentedByGo = normalizeRanges(analysis.uninstrumentedByGo)
	analysis.semanticTokens, err = scanSemanticTokens(path, contents)
	if err != nil {
		return sourceAnalysis{}, err
	}
	return analysis, nil
}

func packageScopeInitializerRanges(fileNode *ast.File, file *token.File) []offsetRange {
	ranges := make([]offsetRange, 0)
	for _, declaration := range fileNode.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.VAR {
			continue
		}
		for _, specification := range general.Specs {
			value, ok := specification.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for _, initializer := range value.Values {
				ranges = append(ranges, offsetRange{
					start: file.Offset(initializer.Pos()),
					end:   file.Offset(initializer.End()),
				})
			}
		}
	}
	return ranges
}

func typeSwitchClauses(root ast.Node) map[*ast.CaseClause]struct{} {
	clauses := make(map[*ast.CaseClause]struct{})
	ast.Inspect(root, func(node ast.Node) bool {
		statement, ok := node.(*ast.TypeSwitchStmt)
		if !ok {
			return true
		}
		for _, bodyStatement := range statement.Body.List {
			if clause, ok := bodyStatement.(*ast.CaseClause); ok {
				clauses[clause] = struct{}{}
			}
		}
		return true
	})
	return clauses
}

func walkExecutableStatements(
	root ast.Node,
	file *token.File,
	typeClauses map[*ast.CaseClause]struct{},
	analysis *sourceAnalysis,
) {
	var walk func(ast.Node)
	walk = func(current ast.Node) {
		ast.Inspect(current, func(node ast.Node) bool {
			if node == nil {
				return true
			}
			switch value := node.(type) {
			case *ast.CaseClause:
				analysis.excluded = append(analysis.excluded, rangeThroughToken(file, value.Pos(), value.Colon))
				if _, isTypeSwitchClause := typeClauses[value]; !isTypeSwitchClause {
					anchor := file.Offset(value.Colon) + 1
					for _, expression := range value.List {
						analysis.clauseExpressions = append(analysis.clauseExpressions, clauseExpression{
							offsetRange: offsetRange{
								start: file.Offset(expression.Pos()),
								end:   file.Offset(expression.End()),
							},
							profileAnchor: anchor,
						})
					}
				}
				for _, statement := range value.Body {
					walk(statement)
				}
				return false
			case *ast.CommClause:
				analysis.excluded = append(analysis.excluded, rangeThroughToken(file, value.Pos(), value.Colon))
				if value.Comm != nil {
					analysis.clauseExpressions = append(analysis.clauseExpressions, clauseExpression{
						offsetRange: offsetRange{
							start: file.Offset(value.Comm.Pos()),
							end:   file.Offset(value.Comm.End()),
						},
						profileAnchor: file.Offset(value.Colon) + 1,
					})
				}
				for _, statement := range value.Body {
					walk(statement)
				}
				return false
			case *ast.LabeledStmt:
				analysis.excluded = append(analysis.excluded, rangeThroughToken(file, value.Pos(), value.Colon))
				walk(value.Stmt)
				return false
			case ast.Stmt:
				if !structuralStatement(value) {
					analysis.statements = append(analysis.statements, offsetRange{
						start: file.Offset(value.Pos()),
						end:   file.Offset(value.End()),
					})
				}
			}
			return true
		})
	}
	walk(root)
}

func structuralStatement(statement ast.Stmt) bool {
	switch statement.(type) {
	case *ast.BadStmt, *ast.BlockStmt, *ast.EmptyStmt, *ast.CaseClause, *ast.CommClause, *ast.LabeledStmt:
		return true
	default:
		return false
	}
}

func rangeThroughToken(file *token.File, start, punctuation token.Pos) offsetRange {
	return offsetRange{
		start: file.Offset(start),
		end:   file.Offset(punctuation) + 1,
	}
}

func scanSemanticTokens(path string, contents []byte) ([]offsetRange, error) {
	fileSet := token.NewFileSet()
	file := fileSet.AddFile(path, -1, len(contents))
	var scanError error
	var lexical scanner.Scanner
	lexical.Init(file, contents, func(position token.Position, message string) {
		if scanError == nil {
			scanError = fmt.Errorf("scan production Go file %s:%d:%d: %s", path, position.Line, position.Column, message)
		}
	}, scanner.ScanComments)

	ranges := make([]offsetRange, 0)
	for {
		position, scanned, literal := lexical.Scan()
		if scanned == token.EOF {
			break
		}
		if scanned == token.COMMENT || structuralToken(scanned) {
			continue
		}
		start := file.Offset(position)
		length := len(literal)
		if length == 0 {
			length = len(scanned.String())
		}
		end := start + length
		if end > len(contents) {
			return nil, fmt.Errorf("token extends beyond production Go file %s", path)
		}
		ranges = append(ranges, offsetRange{start: start, end: end})
	}
	if scanError != nil {
		return nil, scanError
	}
	return ranges, nil
}

func structuralToken(value token.Token) bool {
	switch value {
	case token.SEMICOLON,
		token.LPAREN,
		token.RPAREN,
		token.LBRACK,
		token.RBRACK,
		token.LBRACE,
		token.RBRACE,
		token.COMMA,
		token.PERIOD,
		token.COLON,
		token.ELLIPSIS,
		token.ELSE:
		return true
	default:
		return false
	}
}

func sourceLineStarts(contents []byte) []int {
	starts := []int{0}
	for index, value := range contents {
		if value == '\n' && index+1 < len(contents) {
			starts = append(starts, index+1)
		}
	}
	return starts
}

func normalizeRanges(ranges []offsetRange) []offsetRange {
	if len(ranges) == 0 {
		return nil
	}
	sort.Slice(ranges, func(left, right int) bool {
		if ranges[left].start == ranges[right].start {
			return ranges[left].end < ranges[right].end
		}
		return ranges[left].start < ranges[right].start
	})
	result := []offsetRange{ranges[0]}
	for _, candidate := range ranges[1:] {
		last := &result[len(result)-1]
		if candidate.start <= last.end {
			if candidate.end > last.end {
				last.end = candidate.end
			}
			continue
		}
		result = append(result, candidate)
	}
	return result
}

func hasGoCoverableStatementTokens(analysis sourceAnalysis) bool {
	for _, semantic := range analysis.semanticTokens {
		if len(allowedIntersections(semantic, analysis.statements, analysis.excluded)) > 0 {
			return true
		}
	}
	return false
}

func measureSourceLines(path string, analysis sourceAnalysis, blocks []coverageBlock) (map[int]bool, error) {
	offsetBlocks := make([]offsetCoverageBlock, 0, len(blocks))
	for _, block := range blocks {
		start, err := sourcePositionOffset(block.start, analysis.contents, analysis.lineStarts)
		if err != nil {
			return nil, fmt.Errorf("invalid coverage block start for %s: %w", path, err)
		}
		end, err := sourcePositionOffset(block.end, analysis.contents, analysis.lineStarts)
		if err != nil {
			return nil, fmt.Errorf("invalid coverage block end for %s: %w", path, err)
		}
		if start > end || (start == end && block.numStatements > 0) {
			return nil, fmt.Errorf("invalid empty or reversed coverage block for %s", path)
		}
		offsetBlocks = append(offsetBlocks, offsetCoverageBlock{
			offsetRange:   offsetRange{start: start, end: end},
			numStatements: block.numStatements,
			count:         block.count,
		})
	}

	measured := make(map[int]bool)
	for _, semantic := range analysis.semanticTokens {
		segments := allowedIntersections(semantic, analysis.executable, analysis.excluded)
		for _, segment := range segments {
			for _, piece := range nonemptyLinePieces(analysis.contents, analysis.lineStarts, segment) {
				found := false
				covered := false
				for _, block := range offsetBlocks {
					if block.numStatements == 0 {
						continue
					}
					if _, ok := intersectRanges(piece.offsetRange, block.offsetRange); !ok {
						continue
					}
					found = true
					covered = covered || block.count > 0
				}
				if !found {
					if rangeCoveredByRanges(piece.offsetRange, analysis.uninstrumentedByGo) {
						continue
					}
					column := piece.start - analysis.lineStarts[piece.line-1] + 1
					return nil, fmt.Errorf(
						"Go-coverable token has no coverage block: %s:%d:%d",
						path,
						piece.line,
						column,
					)
				}
				measured[piece.line] = measured[piece.line] || covered
			}
		}
	}

	for _, clause := range analysis.clauseExpressions {
		found := false
		covered := false
		for _, block := range offsetBlocks {
			_, overlapsExpression := intersectRanges(clause.offsetRange, block.offsetRange)
			if block.start != clause.profileAnchor && !overlapsExpression {
				continue
			}
			found = true
			covered = covered || block.count > 0
		}
		if !found {
			position := sourceOffsetPosition(clause.start, analysis.lineStarts)
			return nil, fmt.Errorf(
				"Go-coverable clause expression has no coverage block: %s:%d:%d",
				path,
				position.line,
				position.column,
			)
		}
		for _, semantic := range analysis.semanticTokens {
			segment, ok := intersectRanges(semantic, clause.offsetRange)
			if !ok {
				continue
			}
			for _, piece := range nonemptyLinePieces(analysis.contents, analysis.lineStarts, segment) {
				measured[piece.line] = measured[piece.line] || covered
			}
		}
	}
	return measured, nil
}

func rangeCoveredByRanges(subject offsetRange, ranges []offsetRange) bool {
	pieces := []offsetRange{subject}
	for _, current := range ranges {
		pieces = subtractRange(pieces, current)
		if len(pieces) == 0 {
			return true
		}
	}
	return false
}

func sourceOffsetPosition(offset int, lineStarts []int) sourcePosition {
	lineIndex := lineIndexAtOffset(lineStarts, offset)
	return sourcePosition{
		line:   lineIndex + 1,
		column: offset - lineStarts[lineIndex] + 1,
	}
}

func sourcePositionOffset(position sourcePosition, contents []byte, lineStarts []int) (int, error) {
	if position.line < 1 || position.line > len(lineStarts) || position.column < 1 {
		return 0, fmt.Errorf("position %d:%d is outside the source", position.line, position.column)
	}
	lineStart := lineStarts[position.line-1]
	lineLimit := len(contents)
	if position.line < len(lineStarts) {
		lineLimit = lineStarts[position.line] - 1
	}
	offset := lineStart + position.column - 1
	if offset > lineLimit {
		return 0, fmt.Errorf("position %d:%d is outside the source line", position.line, position.column)
	}
	return offset, nil
}

func allowedIntersections(subject offsetRange, executable, excluded []offsetRange) []offsetRange {
	allowed := make([]offsetRange, 0)
	for _, candidate := range executable {
		overlap, ok := intersectRanges(subject, candidate)
		if !ok {
			continue
		}
		pieces := []offsetRange{overlap}
		for _, exclusion := range excluded {
			pieces = subtractRange(pieces, exclusion)
			if len(pieces) == 0 {
				break
			}
		}
		allowed = append(allowed, pieces...)
	}
	return normalizeRanges(allowed)
}

func subtractRange(subjects []offsetRange, excluded offsetRange) []offsetRange {
	result := make([]offsetRange, 0, len(subjects)+1)
	for _, subject := range subjects {
		overlap, ok := intersectRanges(subject, excluded)
		if !ok {
			result = append(result, subject)
			continue
		}
		if subject.start < overlap.start {
			result = append(result, offsetRange{start: subject.start, end: overlap.start})
		}
		if overlap.end < subject.end {
			result = append(result, offsetRange{start: overlap.end, end: subject.end})
		}
	}
	return result
}

func intersectRanges(left, right offsetRange) (offsetRange, bool) {
	start := left.start
	if right.start > start {
		start = right.start
	}
	end := left.end
	if right.end < end {
		end = right.end
	}
	return offsetRange{start: start, end: end}, start < end
}

type linePiece struct {
	offsetRange
	line int
}

func nonemptyLinePieces(contents []byte, lineStarts []int, segment offsetRange) []linePiece {
	pieces := make([]linePiece, 0)
	firstLine := lineIndexAtOffset(lineStarts, segment.start)
	lastLine := lineIndexAtOffset(lineStarts, segment.end-1)
	for lineIndex := firstLine; lineIndex <= lastLine; lineIndex++ {
		lineStart := lineStarts[lineIndex]
		lineEnd := len(contents)
		if lineIndex+1 < len(lineStarts) {
			lineEnd = lineStarts[lineIndex+1]
		}
		pieceStart := segment.start
		if lineStart > pieceStart {
			pieceStart = lineStart
		}
		pieceEnd := segment.end
		if lineEnd < pieceEnd {
			pieceEnd = lineEnd
		}
		if len(bytes.TrimSpace(contents[pieceStart:pieceEnd])) == 0 {
			continue
		}
		pieces = append(pieces, linePiece{
			offsetRange: offsetRange{start: pieceStart, end: pieceEnd},
			line:        lineIndex + 1,
		})
	}
	return pieces
}

func lineIndexAtOffset(lineStarts []int, offset int) int {
	return sort.Search(len(lineStarts), func(index int) bool {
		return lineStarts[index] > offset
	}) - 1
}
