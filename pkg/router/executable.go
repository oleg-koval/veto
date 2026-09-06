package router

import "strings"

// RequiresExecutableRuntime identifies objectives that need a runtime capable
// of reading or mutating the caller's workspace. Text-only generation remains
// eligible for HTTP transports.
func RequiresExecutableRuntime(objective string) bool {
	s := strings.ToLower(objective)
	if containsAny(s,
		"git push", "commit and push", "push when", "push once",
		"modify the repository", "edit the repository", "update the repository",
		"modify the repo", "edit the repo", "commit the changes",
	) {
		return true
	}
	prTarget := containsAny(s, "pull request", "/pull/", "this pr", "the pr") || containsWord(s, "pr")
	mutation := containsAny(s, "fix", "resolve", "address", "implement", "modify", "edit", "update", "change", "refactor", "push")
	return prTarget && mutation
}

func containsWord(s, word string) bool {
	for _, field := range strings.FieldsFunc(s, func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_')
	}) {
		if field == word {
			return true
		}
	}
	return false
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
