// Package pipeline provides dynamic target discovery from AWS Organizations and Identity Center.
package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/identitystore"
	"github.com/aws/aws-sdk-go-v2/service/ssoadmin"
	log "github.com/sirupsen/logrus"
)

// DiscoveryService handles dynamic target discovery from AWS services
type DiscoveryService struct {
	ctx     context.Context
	awsCtx  *AWSExecutionContext
	config  *Config
}

// NewDiscoveryService creates a new discovery service
func NewDiscoveryService(ctx context.Context, awsCtx *AWSExecutionContext, cfg *Config) *DiscoveryService {
	return &DiscoveryService{
		ctx:    ctx,
		awsCtx: awsCtx,
		config: cfg,
	}
}

// DiscoverTargets discovers and expands dynamic targets into concrete targets
func (d *DiscoveryService) DiscoverTargets() (map[string]Target, error) {
	l := log.WithFields(log.Fields{
		"action": "DiscoveryService.DiscoverTargets",
	})
	l.Info("Starting dynamic target discovery")

	discoveredTargets := make(map[string]Target)

	for dynamicName, dynamicTarget := range d.config.DynamicTargets {
		l := l.WithField("dynamicTarget", dynamicName)
		l.Debug("Processing dynamic target")

		var accounts []AccountInfo
		var err error

		// Discover from Identity Center
		if dynamicTarget.Discovery.IdentityCenter != nil {
			accounts, err = d.discoverFromIdentityCenter(dynamicTarget.Discovery.IdentityCenter)
			if err != nil {
				l.WithError(err).Warn("Failed to discover from Identity Center")
				continue
			}
		}

		// Discover from Organizations
		if dynamicTarget.Discovery.Organizations != nil {
			orgAccounts, err := d.discoverFromOrganizations(dynamicTarget.Discovery.Organizations)
			if err != nil {
				l.WithError(err).Warn("Failed to discover from Organizations")
				continue
			}
			accounts = append(accounts, orgAccounts...)
		}

		// Discover from external account list (e.g., SSM Parameter Store)
		if dynamicTarget.Discovery.AccountsList != nil {
			listAccounts, err := d.discoverFromAccountsList(dynamicTarget.Discovery.AccountsList)
			if err != nil {
				l.WithError(err).Warn("Failed to discover from accounts list")
				continue
			}
			accounts = append(accounts, listAccounts...)
		}

		// Deduplicate accounts
		accounts = deduplicateAccounts(accounts)

		// Convert discovered accounts to targets
		for _, acct := range accounts {
			// Check exclusions
			if isExcluded(acct.ID, dynamicTarget.Exclude) {
				l.WithField("accountID", acct.ID).Debug("Account excluded")
				continue
			}

			// Create target name from account name or ID
			targetName := sanitizeTargetName(acct.Name)
			if targetName == "" {
				targetName = fmt.Sprintf("account_%s", acct.ID)
			}

			// Ensure uniqueness by appending account ID suffix
			if _, exists := discoveredTargets[targetName]; exists {
				targetName = fmt.Sprintf("%s_%s", targetName, acct.ID[:6])
			}

			// Apply dynamic target options with fallbacks to config defaults
			region := dynamicTarget.Region
			if region == "" {
				region = d.config.AWS.Region
			}

			// Process role ARN template (supports {{.AccountID}})
			roleARN := dynamicTarget.RoleARN
			if roleARN != "" {
				roleARN = strings.ReplaceAll(roleARN, "{{.AccountID}}", acct.ID)
			}

			discoveredTargets[targetName] = Target{
				AccountID:    acct.ID,
				Imports:      dynamicTarget.Imports,
				Region:       region,
				SecretPrefix: dynamicTarget.SecretPrefix,
				RoleARN:      roleARN,
			}

			l.WithFields(log.Fields{
				"targetName": targetName,
				"accountID":  acct.ID,
				"region":     region,
			}).Debug("Discovered target")
		}
	}

	l.WithField("count", len(discoveredTargets)).Info("Dynamic target discovery completed")
	return discoveredTargets, nil
}

// discoverFromIdentityCenter discovers accounts from AWS Identity Center
func (d *DiscoveryService) discoverFromIdentityCenter(cfg *IdentityCenterDiscovery) ([]AccountInfo, error) {
	l := log.WithFields(log.Fields{
		"action": "discoverFromIdentityCenter",
		"group":  cfg.Group,
	})
	l.Debug("Discovering accounts from Identity Center")

	if !d.awsCtx.CanAccessIdentityCenter() {
		return nil, fmt.Errorf("no access to Identity Center from this execution context")
	}

	// Get Identity Center client
	ssoClient, err := d.awsCtx.GetIdentityCenterClient(d.ctx)
	if err != nil {
		return nil, err
	}

	// Get Identity Store client for group lookups
	idStoreClient := identitystore.NewFromConfig(d.awsCtx.BaseConfig)

	// List SSO instances to get the identity store ID
	instancesOutput, err := ssoClient.ListInstances(d.ctx, &ssoadmin.ListInstancesInput{})
	if err != nil {
		return nil, fmt.Errorf("failed to list SSO instances: %w", err)
	}

	if len(instancesOutput.Instances) == 0 {
		return nil, fmt.Errorf("no SSO instances found")
	}

	instance := instancesOutput.Instances[0]
	identityStoreID := aws.ToString(instance.IdentityStoreId)
	instanceARN := aws.ToString(instance.InstanceArn)

	var accounts []AccountInfo

	if cfg.Group != "" {
		// Find group by name
		groupID, err := d.findGroupByName(idStoreClient, identityStoreID, cfg.Group)
		if err != nil {
			return nil, fmt.Errorf("failed to find group %q: %w", cfg.Group, err)
		}

		// Get accounts assigned to this group
		accounts, err = d.getAccountsForGroup(ssoClient, instanceARN, groupID)
		if err != nil {
			return nil, err
		}
	}

	if cfg.PermissionSet != "" {
		// Get accounts with this permission set
		psAccounts, err := d.getAccountsWithPermissionSet(ssoClient, instanceARN, cfg.PermissionSet)
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, psAccounts...)
	}

	// Deduplicate accounts
	accounts = deduplicateAccounts(accounts)

	l.WithField("count", len(accounts)).Debug("Discovered accounts from Identity Center")
	return accounts, nil
}

// discoverFromOrganizations discovers accounts from AWS Organizations
func (d *DiscoveryService) discoverFromOrganizations(cfg *OrganizationsDiscovery) ([]AccountInfo, error) {
	l := log.WithFields(log.Fields{
		"action":    "discoverFromOrganizations",
		"ou":        cfg.OU,
		"recursive": cfg.Recursive,
	})
	l.Debug("Discovering accounts from Organizations")

	if !d.awsCtx.CanAccessOrganizations() {
		return nil, fmt.Errorf("no access to Organizations API from this execution context")
	}

	var accounts []AccountInfo

	// Discover by OU
	if cfg.OU != "" {
		if cfg.Recursive {
			// Recursive traversal of OU and all child OUs
			ouAccounts, err := d.listAccountsInOURecursive(cfg.OU)
			if err != nil {
				return nil, err
			}
			accounts = append(accounts, ouAccounts...)
		} else {
			// Direct children only
			ouAccounts, err := d.awsCtx.ListAccountsInOU(d.ctx, cfg.OU)
			if err != nil {
				return nil, err
			}
			accounts = append(accounts, ouAccounts...)
		}
	}

	// If no OU specified but tags are specified, list all accounts and filter
	if cfg.OU == "" && len(cfg.Tags) > 0 {
		allAccounts, err := d.awsCtx.ListOrganizationAccounts(d.ctx)
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, allAccounts...)
	}

	// Filter by tags if specified
	if len(cfg.Tags) > 0 {
		accounts = filterAccountsByTags(accounts, cfg.Tags)
	}

	l.WithField("count", len(accounts)).Debug("Discovered accounts from Organizations")
	return accounts, nil
}

// listAccountsInOURecursive recursively lists accounts in an OU and all child OUs
func (d *DiscoveryService) listAccountsInOURecursive(ouID string) ([]AccountInfo, error) {
	var accounts []AccountInfo

	// Get accounts directly in this OU
	ouAccounts, err := d.awsCtx.ListAccountsInOU(d.ctx, ouID)
	if err != nil {
		return nil, fmt.Errorf("failed to list accounts in OU %s: %w", ouID, err)
	}
	accounts = append(accounts, ouAccounts...)

	// Get child OUs and recurse
	childOUs, err := d.awsCtx.ListChildOUs(d.ctx, ouID)
	if err != nil {
		// Log but continue - we might not have permission to list child OUs
		log.WithError(err).WithField("ou", ouID).Debug("Could not list child OUs")
		return accounts, nil
	}

	for _, childOU := range childOUs {
		childAccounts, err := d.listAccountsInOURecursive(childOU)
		if err != nil {
			log.WithError(err).WithField("childOU", childOU).Debug("Error recursing into child OU")
			continue
		}
		accounts = append(accounts, childAccounts...)
	}

	return accounts, nil
}

// discoverFromAccountsList discovers accounts from an external source (e.g., SSM Parameter Store)
func (d *DiscoveryService) discoverFromAccountsList(cfg *AccountsListDiscovery) ([]AccountInfo, error) {
	l := log.WithFields(log.Fields{
		"action": "discoverFromAccountsList",
		"source": cfg.Source,
	})
	l.Debug("Discovering accounts from external list")

	// Parse the source - currently supports SSM Parameter Store
	if strings.HasPrefix(cfg.Source, "ssm:") {
		paramName := strings.TrimPrefix(cfg.Source, "ssm:")
		return d.getAccountsFromSSM(paramName)
	}

	return nil, fmt.Errorf("unsupported accounts list source: %s (supported: ssm:)", cfg.Source)
}

// getAccountsFromSSM retrieves account IDs from an SSM Parameter Store parameter.
// The parameter value can be:
//   - A comma-separated list of account IDs: "111111111111,222222222222,333333333333"
//   - A JSON array: ["111111111111","222222222222","333333333333"]
//   - A JSON array of objects: [{"id": "111111111111", "name": "Account1"}, ...]
func (d *DiscoveryService) getAccountsFromSSM(paramName string) ([]AccountInfo, error) {
	l := log.WithFields(log.Fields{
		"action": "getAccountsFromSSM",
		"param":  paramName,
	})
	l.Debug("Fetching accounts from SSM Parameter Store")

	// Get parameter value
	value, err := d.awsCtx.GetSSMParameter(d.ctx, paramName)
	if err != nil {
		return nil, err
	}

	value = strings.TrimSpace(value)
	if value == "" {
		return nil, fmt.Errorf("SSM parameter %s is empty", paramName)
	}

	var accounts []AccountInfo

	// Try to parse as JSON array first
	if strings.HasPrefix(value, "[") {
		// Try as array of objects with id/name fields
		var objArray []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		if err := json.Unmarshal([]byte(value), &objArray); err == nil && len(objArray) > 0 && objArray[0].ID != "" {
			for _, obj := range objArray {
				accounts = append(accounts, AccountInfo{
					ID:   obj.ID,
					Name: obj.Name,
				})
			}
			l.WithField("count", len(accounts)).Debug("Parsed SSM parameter as JSON object array")
			return accounts, nil
		}

		// Try as simple string array
		var strArray []string
		if err := json.Unmarshal([]byte(value), &strArray); err == nil {
			for _, id := range strArray {
				id = strings.TrimSpace(id)
				if id != "" {
					accounts = append(accounts, AccountInfo{ID: id})
				}
			}
			l.WithField("count", len(accounts)).Debug("Parsed SSM parameter as JSON string array")
			return accounts, nil
		}
	}

	// Fall back to comma-separated list
	parts := strings.Split(value, ",")
	for _, part := range parts {
		id := strings.TrimSpace(part)
		if id != "" {
			accounts = append(accounts, AccountInfo{ID: id})
		}
	}

	l.WithField("count", len(accounts)).Debug("Parsed SSM parameter as comma-separated list")
	return accounts, nil
}

// findGroupByName finds an Identity Store group by display name
func (d *DiscoveryService) findGroupByName(client *identitystore.Client, storeID, groupName string) (string, error) {
	paginator := identitystore.NewListGroupsPaginator(client, &identitystore.ListGroupsInput{
		IdentityStoreId: aws.String(storeID),
	})

	for paginator.HasMorePages() {
		output, err := paginator.NextPage(d.ctx)
		if err != nil {
			return "", err
		}

		for _, group := range output.Groups {
			if aws.ToString(group.DisplayName) == groupName {
				return aws.ToString(group.GroupId), nil
			}
		}
	}

	return "", fmt.Errorf("group not found: %s", groupName)
}

// getAccountsForGroup gets AWS accounts assigned to an Identity Center group
func (d *DiscoveryService) getAccountsForGroup(client *ssoadmin.Client, instanceARN, groupID string) ([]AccountInfo, error) {
	var accounts []AccountInfo
	seen := make(map[string]bool)

	// List permission sets for this group
	paginator := ssoadmin.NewListPermissionSetsPaginator(client, &ssoadmin.ListPermissionSetsInput{
		InstanceArn: aws.String(instanceARN),
	})

	for paginator.HasMorePages() {
		output, err := paginator.NextPage(d.ctx)
		if err != nil {
			return nil, err
		}

		for _, psARN := range output.PermissionSets {
			// List account assignments for this permission set
			assignmentsPaginator := ssoadmin.NewListAccountAssignmentsPaginator(client, &ssoadmin.ListAccountAssignmentsInput{
				InstanceArn:      aws.String(instanceARN),
				PermissionSetArn: aws.String(psARN),
				AccountId:        nil, // List all accounts
			})

			for assignmentsPaginator.HasMorePages() {
				assignOutput, err := assignmentsPaginator.NextPage(d.ctx)
				if err != nil {
					continue // Skip errors for individual permission sets
				}

				for _, assignment := range assignOutput.AccountAssignments {
					if aws.ToString(assignment.PrincipalId) == groupID {
						accountID := aws.ToString(assignment.AccountId)
						if !seen[accountID] {
							seen[accountID] = true
							accounts = append(accounts, AccountInfo{
								ID: accountID,
							})
						}
					}
				}
			}
		}
	}

	// Enrich with account names from Organizations
	if d.awsCtx.CanAccessOrganizations() {
		allAccounts, _ := d.awsCtx.ListOrganizationAccounts(d.ctx)
		accountMap := make(map[string]AccountInfo)
		for _, a := range allAccounts {
			accountMap[a.ID] = a
		}
		for i, a := range accounts {
			if enriched, ok := accountMap[a.ID]; ok {
				accounts[i] = enriched
			}
		}
	}

	return accounts, nil
}

// getAccountsWithPermissionSet gets accounts with a specific permission set
func (d *DiscoveryService) getAccountsWithPermissionSet(client *ssoadmin.Client, instanceARN, permissionSetName string) ([]AccountInfo, error) {
	// First, find the permission set ARN by name
	var permissionSetARN string
	paginator := ssoadmin.NewListPermissionSetsPaginator(client, &ssoadmin.ListPermissionSetsInput{
		InstanceArn: aws.String(instanceARN),
	})

	for paginator.HasMorePages() {
		output, err := paginator.NextPage(d.ctx)
		if err != nil {
			return nil, err
		}

		for _, psARN := range output.PermissionSets {
			// Get permission set details
			details, err := client.DescribePermissionSet(d.ctx, &ssoadmin.DescribePermissionSetInput{
				InstanceArn:      aws.String(instanceARN),
				PermissionSetArn: aws.String(psARN),
			})
			if err != nil {
				continue
			}

			if aws.ToString(details.PermissionSet.Name) == permissionSetName {
				permissionSetARN = psARN
				break
			}
		}

		if permissionSetARN != "" {
			break
		}
	}

	if permissionSetARN == "" {
		return nil, fmt.Errorf("permission set not found: %s", permissionSetName)
	}

	// List accounts provisioned with this permission set
	var accounts []AccountInfo
	accountsPaginator := ssoadmin.NewListAccountsForProvisionedPermissionSetPaginator(client, &ssoadmin.ListAccountsForProvisionedPermissionSetInput{
		InstanceArn:      aws.String(instanceARN),
		PermissionSetArn: aws.String(permissionSetARN),
	})

	for accountsPaginator.HasMorePages() {
		output, err := accountsPaginator.NextPage(d.ctx)
		if err != nil {
			return nil, err
		}

		for _, accountID := range output.AccountIds {
			accounts = append(accounts, AccountInfo{
				ID: accountID,
			})
		}
	}

	return accounts, nil
}

// Helper functions

func isExcluded(accountID string, excludeList []string) bool {
	for _, excluded := range excludeList {
		if excluded == accountID {
			return true
		}
	}
	return false
}

func sanitizeTargetName(name string) string {
	// Replace spaces and special characters with underscores
	name = strings.ReplaceAll(name, " ", "_")
	name = strings.ReplaceAll(name, "-", "_")
	// Remove any characters that aren't alphanumeric or underscore
	var result strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			result.WriteRune(r)
		}
	}
	return result.String()
}

func deduplicateAccounts(accounts []AccountInfo) []AccountInfo {
	seen := make(map[string]bool)
	var result []AccountInfo
	for _, a := range accounts {
		if !seen[a.ID] {
			seen[a.ID] = true
			result = append(result, a)
		}
	}
	return result
}

func filterAccountsByTags(accounts []AccountInfo, requiredTags map[string]string) []AccountInfo {
	var result []AccountInfo
	for _, a := range accounts {
		if a.Tags == nil {
			continue
		}
		matches := true
		for k, v := range requiredTags {
			if a.Tags[k] != v {
				matches = false
				break
			}
		}
		if matches {
			result = append(result, a)
		}
	}
	return result
}

// ExpandDynamicTargets expands dynamic targets in the config and merges them with static targets
func ExpandDynamicTargets(ctx context.Context, cfg *Config, awsCtx *AWSExecutionContext) error {
	if len(cfg.DynamicTargets) == 0 {
		return nil
	}

	l := log.WithFields(log.Fields{
		"action": "ExpandDynamicTargets",
	})
	l.Info("Expanding dynamic targets")

	discovery := NewDiscoveryService(ctx, awsCtx, cfg)
	discovered, err := discovery.DiscoverTargets()
	if err != nil {
		return fmt.Errorf("failed to discover dynamic targets: %w", err)
	}

	// Merge discovered targets with static targets
	if cfg.Targets == nil {
		cfg.Targets = make(map[string]Target)
	}

	for name, target := range discovered {
		// Don't overwrite static targets
		if _, exists := cfg.Targets[name]; !exists {
			cfg.Targets[name] = target
		} else {
			l.WithField("target", name).Warn("Dynamic target name conflicts with static target, skipping")
		}
	}

	l.WithField("totalTargets", len(cfg.Targets)).Info("Dynamic targets expanded")
	return nil
}
