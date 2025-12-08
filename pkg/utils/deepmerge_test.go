package utils

import (
	"encoding/json"
	"testing"
)

func TestDeepMerge_ListAppend(t *testing.T) {
	// Original requirement: Lists should APPEND, not replace
	// Source 1: {"tags": ["prod"]}
	// Source 2: {"tags": ["v2"]}
	// Expected: {"tags": ["prod", "v2"]}

	dst := map[string]interface{}{
		"tags": []interface{}{"prod"},
	}
	src := map[string]interface{}{
		"tags": []interface{}{"v2"},
	}

	result := DeepMerge(dst, src)

	tags, ok := result["tags"].([]interface{})
	if !ok {
		t.Fatalf("expected tags to be []interface{}, got %T", result["tags"])
	}

	if len(tags) != 2 {
		t.Errorf("expected 2 tags, got %d: %v", len(tags), tags)
	}

	if tags[0] != "prod" || tags[1] != "v2" {
		t.Errorf("expected [prod, v2], got %v", tags)
	}
}

func TestDeepMerge_DictMerge(t *testing.T) {
	// Original requirement: Dicts should MERGE recursively
	// Source 1: {"config": {"a": 1}}
	// Source 2: {"config": {"b": 2}}
	// Expected: {"config": {"a": 1, "b": 2}}

	dst := map[string]interface{}{
		"config": map[string]interface{}{
			"a": float64(1),
		},
	}
	src := map[string]interface{}{
		"config": map[string]interface{}{
			"b": float64(2),
		},
	}

	result := DeepMerge(dst, src)

	config, ok := result["config"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected config to be map[string]interface{}, got %T", result["config"])
	}

	if config["a"] != float64(1) {
		t.Errorf("expected config.a = 1, got %v", config["a"])
	}
	if config["b"] != float64(2) {
		t.Errorf("expected config.b = 2, got %v", config["b"])
	}
}

func TestDeepMerge_NestedDictAndList(t *testing.T) {
	// Combined test matching original example:
	// Source 1: {"api_keys": {"stripe": "sk_old"}, "tags": ["prod"]}
	// Source 2: {"api_keys": {"datadog": "dd_key"}, "tags": ["v2"]}
	// Expected: {"api_keys": {"stripe": "sk_old", "datadog": "dd_key"}, "tags": ["prod", "v2"]}

	dst := map[string]interface{}{
		"api_keys": map[string]interface{}{
			"stripe": "sk_old",
		},
		"tags": []interface{}{"prod"},
	}
	src := map[string]interface{}{
		"api_keys": map[string]interface{}{
			"datadog": "dd_key",
		},
		"tags": []interface{}{"v2"},
	}

	result := DeepMerge(dst, src)

	// Check api_keys
	apiKeys, ok := result["api_keys"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected api_keys to be map, got %T", result["api_keys"])
	}
	if apiKeys["stripe"] != "sk_old" {
		t.Errorf("expected stripe = sk_old, got %v", apiKeys["stripe"])
	}
	if apiKeys["datadog"] != "dd_key" {
		t.Errorf("expected datadog = dd_key, got %v", apiKeys["datadog"])
	}

	// Check tags
	tags, ok := result["tags"].([]interface{})
	if !ok {
		t.Fatalf("expected tags to be []interface{}, got %T", result["tags"])
	}
	if len(tags) != 2 || tags[0] != "prod" || tags[1] != "v2" {
		t.Errorf("expected [prod, v2], got %v", tags)
	}
}

func TestDeepMerge_ScalarOverride(t *testing.T) {
	// Scalars should override (later value wins)
	dst := map[string]interface{}{
		"version": "1.0",
		"count":   float64(5),
	}
	src := map[string]interface{}{
		"version": "2.0",
		"count":   float64(10),
	}

	result := DeepMerge(dst, src)

	if result["version"] != "2.0" {
		t.Errorf("expected version = 2.0, got %v", result["version"])
	}
	if result["count"] != float64(10) {
		t.Errorf("expected count = 10, got %v", result["count"])
	}
}

func TestDeepMerge_TypeConflict(t *testing.T) {
	// Type conflicts: override (src wins)
	dst := map[string]interface{}{
		"data": []interface{}{"a", "b"},
	}
	src := map[string]interface{}{
		"data": "scalar_value",
	}

	result := DeepMerge(dst, src)

	if result["data"] != "scalar_value" {
		t.Errorf("expected data = scalar_value, got %v", result["data"])
	}
}

func TestDeepMerge_NewKeys(t *testing.T) {
	// New keys in src should be added to dst
	dst := map[string]interface{}{
		"existing": "value",
	}
	src := map[string]interface{}{
		"new_key": "new_value",
	}

	result := DeepMerge(dst, src)

	if result["existing"] != "value" {
		t.Errorf("expected existing = value, got %v", result["existing"])
	}
	if result["new_key"] != "new_value" {
		t.Errorf("expected new_key = new_value, got %v", result["new_key"])
	}
}

func TestDeepMerge_DeepNesting(t *testing.T) {
	// Test deeply nested merge
	dst := map[string]interface{}{
		"level1": map[string]interface{}{
			"level2": map[string]interface{}{
				"level3": map[string]interface{}{
					"a": "value_a",
				},
			},
		},
	}
	src := map[string]interface{}{
		"level1": map[string]interface{}{
			"level2": map[string]interface{}{
				"level3": map[string]interface{}{
					"b": "value_b",
				},
			},
		},
	}

	result := DeepMerge(dst, src)

	level3 := result["level1"].(map[string]interface{})["level2"].(map[string]interface{})["level3"].(map[string]interface{})
	if level3["a"] != "value_a" || level3["b"] != "value_b" {
		t.Errorf("expected {a: value_a, b: value_b}, got %v", level3)
	}
}

func TestDeepMerge_NilValues(t *testing.T) {
	// Test nil handling
	dst := map[string]interface{}{
		"keep":   "value",
		"remove": "old",
	}
	src := map[string]interface{}{
		"remove": nil,
		"add":    "new",
	}

	result := DeepMerge(dst, src)

	if result["keep"] != "value" {
		t.Errorf("expected keep = value, got %v", result["keep"])
	}
	// nil in src means "keep dst value" in our implementation
	if result["remove"] != "old" {
		t.Errorf("expected remove = old (nil preserves), got %v", result["remove"])
	}
	if result["add"] != "new" {
		t.Errorf("expected add = new, got %v", result["add"])
	}
}

func TestDeepMerge_EmptyDst(t *testing.T) {
	var dst map[string]interface{}
	src := map[string]interface{}{
		"key": "value",
	}

	result := DeepMerge(dst, src)

	if result["key"] != "value" {
		t.Errorf("expected key = value, got %v", result["key"])
	}
}

func TestDeepMerge_EmptySrc(t *testing.T) {
	dst := map[string]interface{}{
		"key": "value",
	}
	var src map[string]interface{}

	result := DeepMerge(dst, src)

	if result["key"] != "value" {
		t.Errorf("expected key = value, got %v", result["key"])
	}
}

func TestDeepMergeJSON(t *testing.T) {
	dst := []byte(`{"api_keys": {"stripe": "sk_old"}, "tags": ["prod"]}`)
	src := []byte(`{"api_keys": {"datadog": "dd_key"}, "tags": ["v2"]}`)

	result, err := DeepMergeJSON(dst, src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var resultMap map[string]interface{}
	if err := json.Unmarshal(result, &resultMap); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}

	apiKeys := resultMap["api_keys"].(map[string]interface{})
	if apiKeys["stripe"] != "sk_old" || apiKeys["datadog"] != "dd_key" {
		t.Errorf("expected both stripe and datadog keys, got %v", apiKeys)
	}

	tags := resultMap["tags"].([]interface{})
	if len(tags) != 2 {
		t.Errorf("expected 2 tags, got %v", tags)
	}
}

func TestCompareSecretsJSON_Equal(t *testing.T) {
	existing := []byte(`{"key": "value", "count": 5}`)
	new := []byte(`{"key": "value", "count": 5}`)

	equal, err := CompareSecretsJSON(existing, new)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !equal {
		t.Error("expected secrets to be equal")
	}
}

func TestCompareSecretsJSON_DifferentOrder(t *testing.T) {
	// JSON objects with different key order should be equal
	existing := []byte(`{"a": 1, "b": 2}`)
	new := []byte(`{"b": 2, "a": 1}`)

	equal, err := CompareSecretsJSON(existing, new)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !equal {
		t.Error("expected secrets with different key order to be equal")
	}
}

func TestCompareSecretsJSON_NotEqual(t *testing.T) {
	existing := []byte(`{"key": "value"}`)
	new := []byte(`{"key": "different"}`)

	equal, err := CompareSecretsJSON(existing, new)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if equal {
		t.Error("expected secrets to be not equal")
	}
}

func TestCompareSecretsJSON_StringValues(t *testing.T) {
	// Non-JSON strings should be compared as strings
	existing := []byte(`plain text value`)
	new := []byte(`plain text value`)

	equal, err := CompareSecretsJSON(existing, new)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !equal {
		t.Error("expected plain text secrets to be equal")
	}
}

func TestDeepMerge_MultipleImports(t *testing.T) {
	// Simulate merging multiple imports in order:
	// analytics → analytics-engineers → result

	analytics := map[string]interface{}{
		"DATADOG_API_KEY": "dd_xxx",
		"common": map[string]interface{}{
			"region": "us-east-1",
		},
		"tags": []interface{}{"analytics"},
	}

	analyticsEngineers := map[string]interface{}{
		"STRIPE_KEY": "sk_xxx",
		"common": map[string]interface{}{
			"env": "prod",
		},
		"tags": []interface{}{"engineers"},
	}

	// First merge: empty → analytics
	result := DeepMerge(nil, analytics)

	// Second merge: result → analytics-engineers
	result = DeepMerge(result, analyticsEngineers)

	// Verify all keys present
	if result["DATADOG_API_KEY"] != "dd_xxx" {
		t.Error("missing DATADOG_API_KEY")
	}
	if result["STRIPE_KEY"] != "sk_xxx" {
		t.Error("missing STRIPE_KEY")
	}

	// Verify nested dict merged
	common := result["common"].(map[string]interface{})
	if common["region"] != "us-east-1" {
		t.Error("missing common.region")
	}
	if common["env"] != "prod" {
		t.Error("missing common.env")
	}

	// Verify lists appended
	tags := result["tags"].([]interface{})
	if len(tags) != 2 || tags[0] != "analytics" || tags[1] != "engineers" {
		t.Errorf("expected [analytics, engineers], got %v", tags)
	}
}
