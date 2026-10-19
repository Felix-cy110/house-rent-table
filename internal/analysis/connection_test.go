package analysis

import "testing"

func TestConnectionRequiresSupportedProviderAndKey(t *testing.T) {
	for _, input := range []LoginRequest{{}, {APIKey: "key"}, {Provider: "mimo"}, {Provider: "other", APIKey: "key"}, {Provider: "glm", APIKey: "key\nheader"}} {
		if input.Validate() == nil {
			t.Error("invalid configuration accepted")
		}
	}
	for _, provider := range []string{"mimo", "glm", "deepseek"} {
		input := LoginRequest{Provider: " " + provider + " ", APIKey: " key "}
		if input.Validate() != nil || input.Provider != provider || input.APIKey != "key" {
			t.Error("valid configuration not normalized")
		}
	}
}
