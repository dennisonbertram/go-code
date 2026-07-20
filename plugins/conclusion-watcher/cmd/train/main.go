// Command train reads harness SSE event transcripts in JSONL format and calls
// the OpenAI evaluator to annotate each assistant turn with conclusion-jump
// detection results.
//
// Usage:
//
//	train [flags] <transcript.jsonl> [transcript2.jsonl ...]
//
// Flags:
//
//	-api-key string    OpenAI API key (defaults to OPENAI_API_KEY env var)
//	-model string      Evaluator model (default: gpt-4o-mini)
//	-out string        Output annotated JSONL path (default: stdout)
//	-concurrency int   Parallel evaluations (default: 4)
//	-summary           Print summary report to stderr after processing
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"

	cw "go-agent-harness/plugins/conclusion-watcher"
)

// inputEvent represents a single line from the harness SSE event JSONL transcript.
type inputEvent struct {
	Type    string       `json:"type"`
	RunID   string       `json:"run_id"`
	Step    int          `json:"step"`
	Payload eventPayload `json:"payload"`
}

// eventPayload holds the fields relevant to both message.created and tool_call.completed events.
type eventPayload struct {
	// For message.created
	Role      string        `json:"role"`
	Content   string        `json:"content"`
	ToolCalls []payloadTool `json:"tool_calls"`
	// For tool_call.completed
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Result    string `json:"result"`
}

// payloadTool represents a tool call in a message.created event.
type payloadTool struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// outputRecord is one line written to the output JSONL.
type outputRecord struct {
	RunID                    string           `json:"run_id"`
	Step                     int              `json:"step"`
	HasUnjustifiedConclusion bool             `json:"has_unjustified_conclusion"`
	Patterns                 []cw.PatternType `json:"patterns"`
	Evidence                 string           `json:"evidence"`
	Explanation              string           `json:"explanation"`
	OriginalText             string           `json:"original_text"`
}

// summaryReport holds aggregate stats built from output records.
type summaryReport struct {
	TotalSteps    int
	JumpsDetected int
	TotalRuns     int
	ByPattern     map[cw.PatternType]int
}

// parseEvent decodes a single JSON line into an inputEvent.
func parseEvent(line []byte) (inputEvent, error) {
	var ev inputEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		return inputEvent{}, fmt.Errorf("parse event: %w", err)
	}
	return ev, nil
}

// groupByRunID groups events by run_id.
func groupByRunID(events []inputEvent) map[string][]inputEvent {
	out := make(map[string][]inputEvent)
	for _, ev := range events {
		out[ev.RunID] = append(out[ev.RunID], ev)
	}
	return out
}

// buildToolHistory returns the last 10 tool_call.completed events from the run's
// events that have step <= currentStep, formatted as:
// "step N: <tool_name>(<args_truncated_to_50_chars>)"
func buildToolHistory(events []inputEvent, currentStep int) []string {
	var entries []inputEvent
	for _, ev := range events {
		if ev.Type == "tool_call.completed" && ev.Step <= currentStep {
			entries = append(entries, ev)
		}
	}
	// Sort by step ascending.
	sort.Slice(entries, func(i, j int) bool { return entries[i].Step < entries[j].Step })

	// Keep last 10.
	if len(entries) > 10 {
		entries = entries[len(entries)-10:]
	}

	out := make([]string, 0, len(entries))
	for _, ev := range entries {
		args := ev.Payload.Arguments
		if len(args) > 50 {
			args = args[:50]
		}
		out = append(out, fmt.Sprintf("step %d: %s(%s)", ev.Step, ev.Payload.Name, args))
	}
	return out
}

// writeOutputRecord encodes rec as a JSON line and writes it to w.
func writeOutputRecord(w io.Writer, rec outputRecord) error {
	b, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("marshal output record: %w", err)
	}
	_, err = fmt.Fprintf(w, "%s\n", b)
	return err
}

// buildSummary computes aggregate statistics from a slice of output records.
func buildSummary(records []outputRecord) summaryReport {
	report := summaryReport{
		TotalSteps: len(records),
		ByPattern:  make(map[cw.PatternType]int),
	}
	runs := make(map[string]struct{})
	for _, r := range records {
		runs[r.RunID] = struct{}{}
		if r.HasUnjustifiedConclusion {
			report.JumpsDetected++
			for _, p := range r.Patterns {
				report.ByPattern[p]++
			}
		}
	}
	report.TotalRuns = len(runs)
	return report
}

// printSummary writes a human-readable summary to w.
func printSummary(w io.Writer, s summaryReport) {
	pct := 0.0
	if s.TotalSteps > 0 {
		pct = float64(s.JumpsDetected) / float64(s.TotalSteps) * 100
	}
	fmt.Fprintf(w, "Analyzed: %d steps across %d runs\n", s.TotalSteps, s.TotalRuns)
	fmt.Fprintf(w, "Jumps detected: %d (%.1f%%)\n", s.JumpsDetected, pct)
	if len(s.ByPattern) > 0 {
		fmt.Fprintln(w, "By pattern:")
		// Sort patterns for deterministic output.
		patterns := make([]string, 0, len(s.ByPattern))
		for p := range s.ByPattern {
			patterns = append(patterns, string(p))
		}
		sort.Strings(patterns)
		for _, p := range patterns {
			fmt.Fprintf(w, "  %s: %d\n", p, s.ByPattern[cw.PatternType(p)])
		}
	}
}

// evaluationJob is a unit of work for the parallel evaluator pool.
type evaluationJob struct {
	ev          inputEvent
	toolHistory []string
}

// processTranscript reads all JSONL events from scanner, groups them by run_id,
// evaluates each assistant message.created turn using eval, writes output records
// to out, and returns the complete list of records.
//
// concurrency controls how many Evaluate calls run in parallel.
func processTranscript(ctx context.Context, scanner *bufio.Scanner, eval cw.Evaluator, concurrency int, out io.Writer) ([]outputRecord, error) {
	// Read all lines.
	var events []inputEvent
	for scanner.Scan() {
		line := scanner.Bytes()
		trimmed := strings.TrimSpace(string(line))
		if trimmed == "" {
			continue
		}
		ev, err := parseEvent([]byte(trimmed))
		if err != nil {
			// Skip malformed lines.
			continue
		}
		events = append(events, ev)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan error: %w", err)
	}

	// Group by run_id.
	grouped := groupByRunID(events)

	// Collect assistant message.created turns to evaluate.
	type turnEntry struct {
		runID       string
		step        int
		content     string
		toolCalls   []string
		toolHistory []string
	}
	var turns []turnEntry
	for runID, runEvents := range grouped {
		// Sort by step.
		sort.Slice(runEvents, func(i, j int) bool { return runEvents[i].Step < runEvents[j].Step })
		for _, ev := range runEvents {
			if ev.Type != "message.created" || ev.Payload.Role != "assistant" {
				continue
			}
			history := buildToolHistory(runEvents, ev.Step)
			var proposedTools []string
			for _, tc := range ev.Payload.ToolCalls {
				proposedTools = append(proposedTools, tc.Name)
			}
			turns = append(turns, turnEntry{
				runID:       runID,
				step:        ev.Step,
				content:     ev.Payload.Content,
				toolCalls:   proposedTools,
				toolHistory: history,
			})
		}
	}

	if len(turns) == 0 {
		return nil, nil
	}

	// Evaluate turns in parallel using a worker pool.
	type result struct {
		idx    int
		record outputRecord
		err    error
	}

	jobs := make(chan int, len(turns))
	results := make(chan result, len(turns))

	for i := 0; i < len(turns); i++ {
		jobs <- i
	}
	close(jobs)

	if concurrency < 1 {
		concurrency = 1
	}

	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				turn := turns[idx]
				evalResult, err := eval.Evaluate(ctx, turn.content, turn.toolHistory, turn.toolCalls)
				if err != nil {
					results <- result{idx: idx, err: err}
					continue
				}
				rec := outputRecord{
					RunID:                    turn.runID,
					Step:                     turn.step,
					HasUnjustifiedConclusion: evalResult.HasUnjustifiedConclusion,
					Patterns:                 evalResult.Patterns,
					Evidence:                 evalResult.Evidence,
					Explanation:              evalResult.Explanation,
					OriginalText:             turn.content,
				}
				if rec.Patterns == nil {
					rec.Patterns = []cw.PatternType{}
				}
				results <- result{idx: idx, record: rec}
			}
		}()
	}

	// Close results after all workers finish.
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect results preserving order.
	recordsByIdx := make([]outputRecord, len(turns))
	var evalErr error
	for r := range results {
		if r.err != nil {
			evalErr = r.err
			// On error: write a zero record with the original text.
			recordsByIdx[r.idx] = outputRecord{
				RunID:        turns[r.idx].runID,
				Step:         turns[r.idx].step,
				OriginalText: turns[r.idx].content,
				Patterns:     []cw.PatternType{},
			}
			continue
		}
		recordsByIdx[r.idx] = r.record
	}
	_ = evalErr // best-effort: don't fail the whole run on a single eval error

	// Write output JSONL in order.
	for _, rec := range recordsByIdx {
		if err := writeOutputRecord(out, rec); err != nil {
			return nil, fmt.Errorf("write output: %w", err)
		}
	}

	return recordsByIdx, nil
}

func main() {
	apiKey := flag.String("api-key", "", "OpenAI API key (defaults to OPENAI_API_KEY env var)")
	model := flag.String("model", "gpt-4o-mini", "Evaluator model")
	outPath := flag.String("out", "", "Output JSONL path (default: stdout)")
	concurrency := flag.Int("concurrency", 4, "Parallel evaluations")
	summary := flag.Bool("summary", false, "Print summary report to stderr")
	flag.Parse()

	key := *apiKey
	if key == "" {
		key = os.Getenv("OPENAI_API_KEY")
	}
	if key == "" {
		fmt.Fprintln(os.Stderr, "error: -api-key or OPENAI_API_KEY required")
		os.Exit(1)
	}

	eval := cw.NewOpenAIEvaluator(key)
	eval.Model = *model

	var outWriter io.Writer = os.Stdout
	if *outPath != "" {
		f, err := os.Create(*outPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error opening output file: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()
		outWriter = f
	}

	files := flag.Args()
	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "error: at least one transcript.jsonl file required")
		os.Exit(1)
	}

	var allRecords []outputRecord
	ctx := context.Background()

	for _, filePath := range files {
		f, err := os.Open(filePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error opening %s: %v\n", filePath, err)
			os.Exit(1)
		}
		scanner := bufio.NewScanner(f)
		records, err := processTranscript(ctx, scanner, eval, *concurrency, outWriter)
		f.Close()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error processing %s: %v\n", filePath, err)
			os.Exit(1)
		}
		allRecords = append(allRecords, records...)
	}

	if *summary {
		s := buildSummary(allRecords)
		printSummary(os.Stderr, s)
	}
}
