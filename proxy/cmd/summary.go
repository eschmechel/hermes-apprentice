package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"time"

	"github.com/hermes-apprentice/proxy/internal/summary"
	"github.com/spf13/cobra"
)

func summaryCmd() *cobra.Command {
	var (
		sinceStr string
		logsPath string
		outPath  string
	)

	cmd := &cobra.Command{
		Use:   "summary",
		Short: "Generate a per-pattern summary from proxy JSON log lines.",
		Long: `Read proxy JSON log lines (default stdin), filter by time window,
and produce a per-pattern aggregation report.

Log lines are expected to be the structured JSON output from the proxy
server (one JSON object per line as emitted by slog.JSONHandler).`,
		RunE: func(c *cobra.Command, _ []string) error {
			since, err := parseDurationFlag(sinceStr)
			if err != nil {
				return fmt.Errorf("invalid --since: %w", err)
			}
			until := time.Now().UTC()

			in, err := summary.OpenLogs(logsPath)
			if err != nil {
				return err
			}
			defer in.Close()

			report, err := summary.Generate(in, since, until)
			if err != nil {
				return fmt.Errorf("generate: %w", err)
			}

			var out *os.File
			if outPath == "" || outPath == "-" {
				out = os.Stdout
			} else {
				out, err = os.Create(outPath)
				if err != nil {
					return err
				}
				defer out.Close()
			}

			enc := json.NewEncoder(out)
			enc.SetIndent("", "  ")
			return enc.Encode(report)
		},
	}

	defaultLogs := os.ExpandEnv("$HOME/.apprentice/proxy/log/*.jsonl")
	cmd.Flags().StringVar(&sinceStr, "since", "168h", "Window start offset from now (e.g. 7d, 24h, 1w)")
	cmd.Flags().StringVar(&logsPath, "logs", defaultLogs, "Log file path, glob pattern, or '-' for stdin")
	cmd.Flags().StringVar(&outPath, "out", "-", "Output path or '-' for stdout")
	return cmd
}

var durationRe = regexp.MustCompile(`^(\d+)([dw])$`)

func parseDurationFlag(s string) (time.Time, error) {
	d, err := parseDuration(s)
	if err != nil {
		return time.Time{}, err
	}
	return time.Now().UTC().Add(-d), nil
}

func parseDuration(s string) (time.Duration, error) {
	s = cleanStr(s)
	if d, err := time.ParseDuration(s); err == nil {
		return d, nil
	}
	matches := durationRe.FindStringSubmatch(s)
	if matches == nil {
		return 0, fmt.Errorf("invalid duration %q (use 7d, 24h, 1w, etc.)", s)
	}
	val, err := strconv.Atoi(matches[1])
	if err != nil {
		return 0, err
	}
	switch matches[2] {
	case "d":
		return time.Duration(val) * 24 * time.Hour, nil
	case "w":
		return time.Duration(val) * 7 * 24 * time.Hour, nil
	}
	return 0, fmt.Errorf("invalid duration unit %q", matches[2])
}

func cleanStr(s string) string {
	if len(s) > 0 && s[0] == '"' {
		s = s[1:]
	}
	if len(s) > 0 && s[len(s)-1] == '"' {
		s = s[:len(s)-1]
	}
	return s
}
