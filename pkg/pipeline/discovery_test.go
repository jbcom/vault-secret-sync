package pipeline

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsExcluded(t *testing.T) {
	tests := []struct {
		name        string
		accountID   string
		excludeList []string
		expected    bool
	}{
		{
			name:        "not excluded - empty list",
			accountID:   "123456789012",
			excludeList: []string{},
			expected:    false,
		},
		{
			name:        "not excluded",
			accountID:   "123456789012",
			excludeList: []string{"111111111111", "222222222222"},
			expected:    false,
		},
		{
			name:        "excluded",
			accountID:   "123456789012",
			excludeList: []string{"111111111111", "123456789012"},
			expected:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isExcluded(tt.accountID, tt.excludeList)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSanitizeTargetName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple name",
			input:    "Production",
			expected: "Production",
		},
		{
			name:     "with spaces",
			input:    "Serverless Stg",
			expected: "Serverless_Stg",
		},
		{
			name:     "with hyphens",
			input:    "analytics-engineers",
			expected: "analytics_engineers",
		},
		{
			name:     "with special chars",
			input:    "Account (Test) #1",
			expected: "Account_Test_1",
		},
		{
			name:     "numbers only",
			input:    "123456",
			expected: "123456",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeTargetName(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDeduplicateAccounts(t *testing.T) {
	accounts := []AccountInfo{
		{ID: "111111111111", Name: "Account1"},
		{ID: "222222222222", Name: "Account2"},
		{ID: "111111111111", Name: "Account1 Duplicate"},
		{ID: "333333333333", Name: "Account3"},
		{ID: "222222222222", Name: "Account2 Duplicate"},
	}

	result := deduplicateAccounts(accounts)

	assert.Len(t, result, 3)
	ids := make(map[string]bool)
	for _, a := range result {
		ids[a.ID] = true
	}
	assert.True(t, ids["111111111111"])
	assert.True(t, ids["222222222222"])
	assert.True(t, ids["333333333333"])
}

func TestFilterAccountsByTags(t *testing.T) {
	accounts := []AccountInfo{
		{
			ID:   "111111111111",
			Name: "ProdAccount",
			Tags: map[string]string{"Environment": "production", "Team": "platform"},
		},
		{
			ID:   "222222222222",
			Name: "DevAccount",
			Tags: map[string]string{"Environment": "development", "Team": "platform"},
		},
		{
			ID:   "333333333333",
			Name: "StagingAccount",
			Tags: map[string]string{"Environment": "staging", "Team": "analytics"},
		},
		{
			ID:   "444444444444",
			Name: "NoTagsAccount",
			Tags: nil,
		},
	}

	t.Run("filter by single tag", func(t *testing.T) {
		result := filterAccountsByTags(accounts, map[string]string{"Environment": "production"})
		assert.Len(t, result, 1)
		assert.Equal(t, "111111111111", result[0].ID)
	})

	t.Run("filter by multiple tags", func(t *testing.T) {
		result := filterAccountsByTags(accounts, map[string]string{
			"Environment": "development",
			"Team":        "platform",
		})
		assert.Len(t, result, 1)
		assert.Equal(t, "222222222222", result[0].ID)
	})

	t.Run("no matches", func(t *testing.T) {
		result := filterAccountsByTags(accounts, map[string]string{"Environment": "sandbox"})
		assert.Len(t, result, 0)
	})
}
