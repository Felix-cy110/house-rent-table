package aiclient

import (
	"errors"
	"strconv"
	"strings"

	"github.com/Felix-cy110/house-rent-table/internal/analysis"
)

const defaultSearchLimit = 24

// ParseSearchLimit reads a server setting, never a user-supplied model field.
func ParseSearchLimit(value string) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return defaultSearchLimit, nil
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit < 1 || limit > 100 {
		return 0, errors.New("RENT_SEARCH_MAX_CALLS 必须是 1 至 100 的整数")
	}
	return limit, nil
}

// A budget and its successful-result cache belong to one analysis. Never reuse
// results across users, keys or reports. Failed outbound calls still consume budget.
type searchBudget struct {
	limit int
	calls int
	cache map[string]searchResult
}

type searchResult struct {
	output  string
	sources []analysis.Source
}

func (s *Service) toolRoundLimit() int {
	// Leave room for sequential queries, calculation, fallback and a final answer.
	return max(16, s.opts.SearchLimit+8)
}
