package analysis

import "testing"

func TestDirectConnectionRequiresKeyModelAndURL(t *testing.T) {
	for _, input := range []LoginRequest{
		{}, {APIKey: "key", Model: "m"}, {APIKey: "key", BaseURL: "https://example.com/v1"},
		{APIKey: "key\nheader", BaseURL: "https://example.com/v1", Model: "m"},
		{APIKey: "key", BaseURL: "https://user:password@example.com/v1", Model: "m"},
		{APIKey: "key", BaseURL: "https://example.com/v1?key=secret", Model: "m"},
		{APIKey: "key", BaseURL: "https://example.com/v1#secret", Model: "m"},
		{APIKey: "key", BaseURL: "file:///private", Model: "m"},
	} {
		if input.Validate() == nil {
			t.Errorf("invalid input accepted: model=%q", input.Model)
		}
	}
	input := LoginRequest{APIKey: " key ", BaseURL: " https://example.com/v1/ ", Model: " m "}
	if err := input.Validate(); err != nil || input.APIKey != "key" || input.BaseURL != "https://example.com/v1" || input.Model != "m" {
		t.Fatal("valid connection not normalized")
	}
}
