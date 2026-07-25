package option

import (
	"encoding/json"
	"testing"
)

func TestProviderRemoteDisableAutoUpdateOption(t *testing.T) {
	var options ProviderRemoteOptions
	if err := json.Unmarshal([]byte(`{"url":"https://example.com/subscription","disable_auto_update":true}`), &options); err != nil {
		t.Fatal(err)
	}
	if !options.DisableAutoUpdate {
		t.Fatal("disable_auto_update was not decoded")
	}
}
