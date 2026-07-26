package option

import (
	"encoding/json"
	"testing"
)

func TestProviderRemoteDisableAutoUpdateOption(t *testing.T) {
	var options ProviderRemoteOptions
	if err := json.Unmarshal([]byte(`{"url":"https://example.com/subscription","disable_auto_update":true,"additional_prefix":"custom ","additional_suffix":" suffix"}`), &options); err != nil {
		t.Fatal(err)
	}
	if !options.DisableAutoUpdate {
		t.Fatal("disable_auto_update was not decoded")
	}
	if options.AdditionalPrefix != "custom " {
		t.Fatal("additional_prefix was not decoded")
	}
	if options.AdditionalSuffix != " suffix" {
		t.Fatal("additional_suffix was not decoded")
	}
}
