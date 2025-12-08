// Package utils provides utility functions for vault-secret-sync.
package utils

import (
	"encoding/json"
	"reflect"
)

// DeepMerge merges src into dst using the following strategy:
//   - Lists: APPEND (src items are appended to dst)
//   - Dicts/Maps: MERGE (recursive deep merge)
//   - Scalars: OVERRIDE (src replaces dst)
//   - Type conflicts: OVERRIDE (src replaces dst)
//
// This matches the behavior of Python's deepmerge.Merger with:
//
//	[(list, ["append"]), (dict, ["merge"]), (set, ["union"])],
//	["override"], ["override"]
//
// The function modifies dst in place and returns the merged result.
func DeepMerge(dst, src map[string]interface{}) map[string]interface{} {
	if dst == nil {
		dst = make(map[string]interface{})
	}
	if src == nil {
		return dst
	}

	for key, srcVal := range src {
		dstVal, exists := dst[key]

		if !exists {
			// Key doesn't exist in dst, just set it
			dst[key] = deepCopyValue(srcVal)
			continue
		}

		// Both exist, merge based on types
		dst[key] = mergeValues(dstVal, srcVal)
	}

	return dst
}

// mergeValues merges two values according to deepmerge semantics
func mergeValues(dst, src interface{}) interface{} {
	// Handle nil cases
	if src == nil {
		return dst
	}
	if dst == nil {
		return deepCopyValue(src)
	}

	// Type-based merging
	switch srcTyped := src.(type) {
	case map[string]interface{}:
		// Dict merge (recursive)
		if dstMap, ok := dst.(map[string]interface{}); ok {
			return DeepMerge(dstMap, srcTyped)
		}
		// Type conflict: override
		return deepCopyValue(src)

	case []interface{}:
		// List append
		if dstSlice, ok := dst.([]interface{}); ok {
			return appendSlices(dstSlice, srcTyped)
		}
		// Type conflict: override
		return deepCopyValue(src)

	default:
		// Scalar or unknown type: override
		return deepCopyValue(src)
	}
}

// appendSlices appends src slice to dst slice (list append strategy)
func appendSlices(dst, src []interface{}) []interface{} {
	result := make([]interface{}, 0, len(dst)+len(src))

	// Copy dst items
	for _, v := range dst {
		result = append(result, deepCopyValue(v))
	}

	// Append src items
	for _, v := range src {
		result = append(result, deepCopyValue(v))
	}

	return result
}

// deepCopyValue creates a deep copy of a value
func deepCopyValue(v interface{}) interface{} {
	if v == nil {
		return nil
	}

	switch typed := v.(type) {
	case map[string]interface{}:
		result := make(map[string]interface{}, len(typed))
		for k, val := range typed {
			result[k] = deepCopyValue(val)
		}
		return result

	case []interface{}:
		result := make([]interface{}, len(typed))
		for i, val := range typed {
			result[i] = deepCopyValue(val)
		}
		return result

	default:
		// Scalars are immutable, just return
		return v
	}
}

// DeepMergeJSON merges two JSON byte slices using deepmerge semantics.
// Returns the merged result as JSON bytes.
func DeepMergeJSON(dst, src []byte) ([]byte, error) {
	var dstMap map[string]interface{}
	var srcMap map[string]interface{}

	// Parse dst (may be empty/nil)
	if len(dst) > 0 {
		if err := json.Unmarshal(dst, &dstMap); err != nil {
			return nil, err
		}
	}
	if dstMap == nil {
		dstMap = make(map[string]interface{})
	}

	// Parse src
	if len(src) > 0 {
		if err := json.Unmarshal(src, &srcMap); err != nil {
			return nil, err
		}
	}

	// Merge
	result := DeepMerge(dstMap, srcMap)

	// Serialize
	return json.Marshal(result)
}

// DeepEqual compares two values for deep equality.
// Handles JSON number comparison properly.
func DeepEqual(a, b interface{}) bool {
	// Handle JSON numbers by normalizing to float64
	a = normalizeValue(a)
	b = normalizeValue(b)
	return reflect.DeepEqual(a, b)
}

// normalizeValue normalizes JSON values for comparison
func normalizeValue(v interface{}) interface{} {
	switch typed := v.(type) {
	case map[string]interface{}:
		result := make(map[string]interface{}, len(typed))
		for k, val := range typed {
			result[k] = normalizeValue(val)
		}
		return result

	case []interface{}:
		result := make([]interface{}, len(typed))
		for i, val := range typed {
			result[i] = normalizeValue(val)
		}
		return result

	case json.Number:
		// Convert JSON numbers to float64 for consistent comparison
		if f, err := typed.Float64(); err == nil {
			return f
		}
		return typed.String()

	case int:
		return float64(typed)

	case int64:
		return float64(typed)

	case int32:
		return float64(typed)

	default:
		return v
	}
}

// CompareSecretsJSON compares two JSON secret values for equality.
// Returns true if they are equivalent (handling JSON number differences).
func CompareSecretsJSON(existing, new []byte) (bool, error) {
	var existingMap, newMap interface{}

	// Try to parse as JSON
	if err := json.Unmarshal(existing, &existingMap); err != nil {
		// Not valid JSON, compare as strings
		return string(existing) == string(new), nil
	}

	if err := json.Unmarshal(new, &newMap); err != nil {
		// New is not valid JSON but existing is, they're different
		return false, nil
	}

	return DeepEqual(existingMap, newMap), nil
}
