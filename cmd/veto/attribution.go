package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
)

const (
	vetoRepositoryURL   = "https://github.com/oleg-koval/veto"
	vetoAttributionMark = "Veto-Assisted: true"
	vetoAttributionLogo = "🛡️"
)

func attributionText(format string) (string, error) {
	switch format {
	case "commit", "pr":
		return fmt.Sprintf("%s Built with [Veto](%s)\n\n%s\n", vetoAttributionLogo, vetoRepositoryURL, vetoAttributionMark), nil
	default:
		return "", errors.New("format must be commit or pr")
	}
}

func vetoGitHookScript() string {
	footer, _ := attributionText("commit")
	return "#!/bin/sh\n# " + hookMarker + "\n" +
		"if [ -z \"${1:-}\" ]; then\n  exit 0\nfi\n" +
		"case \"${2:-}\" in\n  merge|squash) exit 0 ;;\nesac\n" +
		"MODEL=$(veto route --quiet --task \"$(git diff --cached --stat)\" 2>/dev/null || true)\n" +
		"if [ -n \"$MODEL\" ] && ! grep -q '^" + vetoAttributionMark + "$' \"$1\"; then\n" +
		"  printf '\\n# veto suggested model: %s\\n' \"$MODEL\" >> \"$1\"\n" +
		"  printf '\\n%s\\n' '" + footer + "' >> \"$1\"\n" +
		"fi\n"
}

func cmdAttribution(args []string) {
	fs := flag.NewFlagSet("attribution", flag.ExitOnError)
	format := fs.String("format", "pr", "output format: commit|pr")
	_ = fs.Parse(args)

	text, err := attributionText(*format)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}
	fmt.Print(text)
}
