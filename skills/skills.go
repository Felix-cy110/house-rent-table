// Package skills bundles the application's review instructions with the binary.
package skills

import (
	_ "embed"
)

const Name = "rental-review"

//go:embed rental-review/SKILL.md
var rentalReview []byte

// Instructions is sent separately from the unmodified spreadsheet JSON.
func Instructions() string { return string(rentalReview) }
