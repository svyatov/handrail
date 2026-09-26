package rule

import (
	"fmt"
	"slices"
)

// Trust end to end: the registry file that records it, and what the user is
// told when a tier goes untrusted. The file is one Project root per line, and
// it gates the Project-shared tier only: cloning a repo cannot put its
// committed rules in front of the agent until the user grants that path once.

const trustFile = "trusted"

// isTrusted reports whether root's Project-shared tier has been granted. An
// unreadable registry means untrusted: the gate fails closed.
func isTrusted(root string) bool {
	return slices.Contains(registry(trustFile), root)
}

// TrustNotice is what a skipped Project-shared tier owes the user, and "" when
// nothing was skipped. The tiers and the root are the ruleset's own state, so
// the sentence is written here rather than assembled by every caller that has
// to print it.
func (rs *Ruleset) TrustNotice() string {
	for _, t := range rs.Tiers {
		if t.Skipped {
			return fmt.Sprintf(
				"handrail: skipping the untrusted Project-shared rules in %s; run handrail trust to enable them",
				rs.Root)
		}
	}

	return ""
}

// Trust records a project root as trusted, reporting whether that was new. The
// grant is keyed by the root alone, so granting one needs no ruleset: reading
// the rules is what the grant gates, not a prerequisite for making it.
func Trust(root string) (bool, error) {
	granted := registry(trustFile)
	if slices.Contains(granted, root) {
		return false, nil
	}

	err := writeRegistry(trustFile, append(granted, root))

	return err == nil, err
}
