package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"agentflow-platform/apps/api/internal/evaluation/failurecorpus"
)

func runFailures(ctx context.Context, args []string, out, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: eval failures <candidate|promote|check> [options]")
		return 2
	}
	flags := flag.NewFlagSet("eval failures "+args[0], flag.ContinueOnError)
	flags.SetOutput(stderr)
	corpusPath := flags.String("corpus", "../../examples/failures/corpus.v1.json", "reviewed failure corpus")
	var artifact, id, owner, sourceRef, sourceRevision, evidenceRef, candidatePath, resultsPath, resultsRevision string
	var enforce bool
	switch args[0] {
	case "candidate":
		flags.StringVar(&artifact, "artifact", "", "operator-retained Replay or evaluation JSON (never copied)")
		flags.StringVar(&id, "id", "", "stable failure case ID")
		flags.StringVar(&owner, "owner", "", "provider, tool, authorization, rag, context, routing or recovery")
		flags.StringVar(&sourceRef, "source-ref", "", "credential-free HTTPS source reference")
		flags.StringVar(&sourceRevision, "source-revision", "", "full source Git revision")
		flags.StringVar(&evidenceRef, "evidence-ref", "", "credential-free HTTPS location of retained evidence")
	case "promote":
		flags.StringVar(&candidatePath, "candidate", "", "manually reviewed failure-case-v1 JSON")
	case "check":
		flags.StringVar(&resultsPath, "results", "", "retained go test -json output")
		flags.StringVar(&resultsRevision, "results-revision", "", "full Git revision used to execute the tests")
		flags.BoolVar(&enforce, "enforce", false, "exit 1 for any failed, skipped or missing regression")
	default:
		fmt.Fprintln(stderr, "use failures candidate, promote or check")
		return 2
	}
	if flags.Parse(args[1:]) != nil || flags.NArg() != 0 {
		return 2
	}
	inputPath := artifact
	if args[0] != "candidate" {
		inputPath = *corpusPath
	}
	input, err := os.Open(inputPath)
	if err != nil {
		fmt.Fprintln(stderr, "cannot open failure input")
		return 2
	}
	defer input.Close()
	var value any
	if args[0] == "candidate" {
		value, err = failurecorpus.Candidate(input, failurecorpus.Case{ID: id, Owner: owner, SourceRef: sourceRef, SourceRevision: sourceRevision}, evidenceRef)
	} else {
		var corpus failurecorpus.Corpus
		corpus, err = failurecorpus.DecodeCorpus(input)
		if err == nil {
			path := candidatePath
			if args[0] == "check" {
				path = resultsPath
			}
			var evidence *os.File
			evidence, err = os.Open(path)
			if err != nil {
				fmt.Fprintln(stderr, "cannot open failure candidate or test results")
				return 2
			}
			defer evidence.Close()
			if args[0] == "promote" {
				var item failurecorpus.Case
				item, err = failurecorpus.DecodeCase(evidence)
				if err == nil {
					value, err = failurecorpus.Promote(corpus, item)
				}
			} else {
				value, err = failurecorpus.Check(corpus, evidence, resultsRevision, gitRevision(ctx))
			}
		}
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if !writeJSON(out, stderr, value) {
		return 2
	}
	if report, ok := value.(failurecorpus.Report); ok {
		fmt.Fprintf(stderr, "failure regressions: passed=%t cases=%d failures=%d\n", report.Gate.Passed, report.Gate.BlockingSamples, report.Gate.BlockingFailures)
		if enforce && !report.Gate.Passed {
			return 1
		}
	}
	return 0
}
