package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"agentflow-platform/apps/api/internal/skill"
)

func checkCommand(args []string, out, stderr io.Writer) int {
	flags := flag.NewFlagSet("skill check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var opts skill.CheckOptions
	flags.Func("dir", "package directory to check (repeatable; no trust granted)", func(value string) error { opts.Directories = append(opts.Directories, value); return nil })
	flags.Func("root", "installation root for immediate discovery (repeatable)", func(value string) error { opts.Roots = append(opts.Roots, value); return nil })
	flags.Func("tools", "effective, ready Tool names, comma-separated; empty means no Tools", func(value string) error {
		opts.EffectiveTools = []string{}
		for _, name := range strings.Split(value, ",") {
			if name = strings.TrimSpace(name); name != "" {
				opts.EffectiveTools = append(opts.EffectiveTools, name)
			}
		}
		return nil
	})
	if err := flags.Parse(args); errors.Is(err, flag.ErrHelp) {
		return 0
	} else if err != nil || flags.NArg() != 0 {
		return 2
	}
	if len(opts.Directories)+len(opts.Roots) == 0 {
		fmt.Fprintln(stderr, "check requires --dir or --root; no implicit trusted directory is inspected")
		return 2
	}
	report := skill.Check(opts)
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		fmt.Fprintln(stderr, "could not write Skill check report")
		return 1
	}
	if !report.Compatible {
		return 1
	}
	return 0
}
