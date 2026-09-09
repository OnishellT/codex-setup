package installer

import "testing"

func TestNativeCatalogFiltersHiddenAndRetainsValidatedSpark(t *testing.T) {
	options := mergeNativeCatalog([]ModelOption{{"gpt-5.3-codex-spark", []string{"low", "medium", "high", "xhigh"}}}, []byte(`{"models":[
	{"slug":"gpt-hidden","visibility":"hide","supported_reasoning_levels":[{"effort":"high"}]},
	{"slug":"gpt-visible","visibility":"list","supported_reasoning_levels":[{"effort":"medium"}]},
	{"slug":"foreign-model","visibility":"list","supported_reasoning_levels":[{"effort":"high"}]}
	]}`))
	if len(options) != 2 || options[0].Model != "gpt-5.3-codex-spark" || options[1].Model != "gpt-visible" {
		t.Fatalf("unexpected options: %#v", options)
	}
	for _, effort := range []string{"low", "medium", "high", "xhigh"} {
		if !hasString(options[0].Efforts, effort) {
			t.Fatalf("validated Spark effort lost: %s", effort)
		}
	}
}
