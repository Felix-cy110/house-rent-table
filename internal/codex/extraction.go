package codex

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/Felix-cy110/house-rent-table/internal/analysis"
	"github.com/Felix-cy110/house-rent-table/internal/extraction"
)

// Extract uses a separate app-server, gate, event stream and working directory.
// Only authentication is shared with the app's login; prompts and tools are not.
// Each attempt snapshots the currently selected provider. No credential enters
// the durable task, and database-only retries never invoke this method.
func (s *Service) Extract(ctx context.Context, input extraction.Input) (extraction.Output, error) {
	s.startMu.Lock()
	home, err := s.authHome()
	var provider *providerConfig
	if err == nil {
		provider, err = loadProvider(home)
	}
	opts := s.opts
	s.startMu.Unlock()
	if err != nil {
		return extraction.Output{}, analysis.ErrUnavailable
	}
	worker := New(opts)
	if provider != nil && provider.Model != "" {
		worker.opts.Model = provider.Model
	}
	worker.extractionOnly = true
	worker.workDir = filepath.Join(home, "extraction-workspace")
	if err = os.MkdirAll(worker.workDir, 0700); err != nil {
		return extraction.Output{}, analysis.ErrUnavailable
	}
	worker.client, err = worker.openClient(ctx, home, provider)
	if err != nil {
		return extraction.Output{}, err
	}
	defer worker.Close()
	raw, err := json.Marshal(input)
	if err != nil {
		return extraction.Output{}, err
	}
	result, err := worker.analyze(ctx, raw, extraction.Schema(), extraction.Instructions())
	output := extraction.Output{JSON: json.RawMessage(result.Text), Provider: "openai", Model: worker.opts.Model}
	if provider != nil {
		output.Provider = provider.Name
		if output.Model == "" {
			output.Model = provider.Model
		}
	}
	return output, err
}
