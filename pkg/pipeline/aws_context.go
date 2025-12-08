// Package pipeline provides AWS execution context management for multi-account operations.
//
// AWS Organizations supports two primary patterns for cross-account operations:
//
// 1. MANAGEMENT ACCOUNT: The root account that owns the AWS Organization
//    - Has implicit trust from OrganizationAccountAccessRole in all member accounts
//    - Can assume AWSControlTowerExecution in Control Tower enrolled accounts
//    - Full Organizations API access
//    - Full Identity Center admin access
//    - NOT recommended for production workloads (security best practice)
//
// 2. DELEGATED ADMINISTRATOR: A member account with delegated permissions
//    - Requires explicit delegation via Organizations RegisterDelegatedAdministrator
//    - Can be delegated for specific services (SSO, CloudFormation StackSets, etc.)
//    - More secure - separates admin workloads from org management
//    - Requires custom cross-account role deployment (StackSets, AFT, etc.)
//
// This package handles both patterns and provides a unified interface for
// cross-account secrets synchronization.
package pipeline

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/organizations"
	"github.com/aws/aws-sdk-go-v2/service/ssoadmin"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	log "github.com/sirupsen/logrus"
)

// AWSExecutionContext manages AWS credentials and cross-account access
type AWSExecutionContext struct {
	Config           *AWSConfig
	BaseConfig       aws.Config
	CallerIdentity   *CallerIdentity
	OrganizationInfo *OrganizationInfo
	
	// Cached clients
	stsClient  *sts.Client
	orgClient  *organizations.Client
	ssoClient  *ssoadmin.Client
}

// CallerIdentity contains AWS STS GetCallerIdentity information
type CallerIdentity struct {
	AccountID string
	ARN       string
	UserID    string
}

// OrganizationInfo contains AWS Organizations information
type OrganizationInfo struct {
	ID                string
	MasterAccountID   string
	MasterAccountARN  string
	IsManagementAccount bool
	IsDelegatedAdmin    bool
	DelegatedServices   []string
}

// redactARN extracts the type of identity from an ARN for safe logging
// without exposing sensitive role/user names
func redactARN(arn string) string {
	parts := strings.Split(arn, ":")
	if len(parts) < 6 {
		return "unknown"
	}
	// Extract resource type (role, user, assumed-role, etc.)
	resource := parts[5]
	if idx := strings.Index(resource, "/"); idx > 0 {
		return resource[:idx] // Return just "role", "user", "assumed-role"
	}
	return resource
}

// NewAWSExecutionContext creates and initializes an AWS execution context
func NewAWSExecutionContext(ctx context.Context, cfg *AWSConfig) (*AWSExecutionContext, error) {
	l := log.WithFields(log.Fields{
		"action": "NewAWSExecutionContext",
	})

	// Load base AWS config from environment (supports OIDC, instance profile, etc.)
	awsCfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(cfg.Region))
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	ec := &AWSExecutionContext{
		Config:     cfg,
		BaseConfig: awsCfg,
		stsClient:  sts.NewFromConfig(awsCfg),
	}

	// Get caller identity
	if err := ec.discoverCallerIdentity(ctx); err != nil {
		return nil, fmt.Errorf("failed to get caller identity: %w", err)
	}

	l.WithFields(log.Fields{
		"accountID":    ec.CallerIdentity.AccountID,
		"identityType": redactARN(ec.CallerIdentity.ARN),
	}).Info("AWS caller identity discovered")

	// Discover organization context
	if err := ec.discoverOrganizationContext(ctx); err != nil {
		// Non-fatal - might not have org access
		l.WithError(err).Warn("Could not discover organization context")
	}

	// Validate execution context
	if err := ec.validateExecutionContext(); err != nil {
		return nil, err
	}

	return ec, nil
}

// discoverCallerIdentity gets the current AWS identity
func (ec *AWSExecutionContext) discoverCallerIdentity(ctx context.Context) error {
	output, err := ec.stsClient.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return err
	}

	ec.CallerIdentity = &CallerIdentity{
		AccountID: aws.ToString(output.Account),
		ARN:       aws.ToString(output.Arn),
		UserID:    aws.ToString(output.UserId),
	}

	return nil
}

// discoverOrganizationContext discovers organization membership and delegation status
func (ec *AWSExecutionContext) discoverOrganizationContext(ctx context.Context) error {
	l := log.WithFields(log.Fields{
		"action": "discoverOrganizationContext",
	})

	ec.orgClient = organizations.NewFromConfig(ec.BaseConfig)

	// Get organization info
	orgOutput, err := ec.orgClient.DescribeOrganization(ctx, &organizations.DescribeOrganizationInput{})
	if err != nil {
		return fmt.Errorf("failed to describe organization: %w", err)
	}

	org := orgOutput.Organization
	ec.OrganizationInfo = &OrganizationInfo{
		ID:               aws.ToString(org.Id),
		MasterAccountID:  aws.ToString(org.MasterAccountId),
		MasterAccountARN: aws.ToString(org.MasterAccountArn),
	}

	// Check if we're the management account
	ec.OrganizationInfo.IsManagementAccount = ec.CallerIdentity.AccountID == ec.OrganizationInfo.MasterAccountID

	l.WithFields(log.Fields{
		"orgID":               ec.OrganizationInfo.ID,
		"masterAccountID":     ec.OrganizationInfo.MasterAccountID,
		"isManagementAccount": ec.OrganizationInfo.IsManagementAccount,
	}).Debug("Organization info discovered")

	// If not management account, check delegated admin status
	if !ec.OrganizationInfo.IsManagementAccount {
		if err := ec.discoverDelegatedServices(ctx); err != nil {
			l.WithError(err).Debug("Could not discover delegated services")
		}
	}

	return nil
}

// discoverDelegatedServices checks what services this account is delegated admin for
func (ec *AWSExecutionContext) discoverDelegatedServices(ctx context.Context) error {
	// This requires calling from an account that can list delegated admins
	// In practice, this might fail if we're not management account
	
	paginator := organizations.NewListDelegatedAdministratorsPaginator(ec.orgClient, &organizations.ListDelegatedAdministratorsInput{})
	
	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return err
		}

		for _, admin := range output.DelegatedAdministrators {
			if aws.ToString(admin.Id) == ec.CallerIdentity.AccountID {
				ec.OrganizationInfo.IsDelegatedAdmin = true
				// Get services for this delegated admin
				servicesOutput, err := ec.orgClient.ListDelegatedServicesForAccount(ctx, &organizations.ListDelegatedServicesForAccountInput{
					AccountId: admin.Id,
				})
				if err == nil {
					for _, svc := range servicesOutput.DelegatedServices {
						ec.OrganizationInfo.DelegatedServices = append(
							ec.OrganizationInfo.DelegatedServices,
							aws.ToString(svc.ServicePrincipal),
						)
					}
				}
				break
			}
		}
	}

	return nil
}

// validateExecutionContext validates the execution context matches configuration
func (ec *AWSExecutionContext) validateExecutionContext() error {
	l := log.WithFields(log.Fields{
		"action": "validateExecutionContext",
	})

	configuredType := ec.Config.ExecutionContext.Type
	configuredAccountID := ec.Config.ExecutionContext.AccountID

	// Validate account ID if specified
	if configuredAccountID != "" && configuredAccountID != ec.CallerIdentity.AccountID {
		return fmt.Errorf(
			"execution context mismatch: config specifies account %s but running as %s",
			configuredAccountID,
			ec.CallerIdentity.AccountID,
		)
	}

	// Validate execution context type
	switch configuredType {
	case ExecutionContextManagement:
		if ec.OrganizationInfo != nil && !ec.OrganizationInfo.IsManagementAccount {
			return fmt.Errorf(
				"execution context configured as management_account but running in member account %s (management is %s)",
				ec.CallerIdentity.AccountID,
				ec.OrganizationInfo.MasterAccountID,
			)
		}
		l.Info("Validated: running from management account")

	case ExecutionContextDelegated:
		if ec.OrganizationInfo != nil && ec.OrganizationInfo.IsManagementAccount {
			l.Warn("Configured as delegated_admin but running from management account")
		}
		if ec.OrganizationInfo != nil && !ec.OrganizationInfo.IsDelegatedAdmin {
			l.Warn("Account may not be a delegated administrator - cross-account access may fail")
		}
		if ec.OrganizationInfo != nil {
			l.WithField("services", ec.OrganizationInfo.DelegatedServices).Info("Validated: running from delegated admin account")
		} else {
			l.Info("Validated: running as delegated admin (organization info unavailable)")
		}

	case ExecutionContextHub:
		l.Info("Validated: running from hub account with custom role pattern")

	default:
		// Auto-detect
		if ec.OrganizationInfo != nil {
			if ec.OrganizationInfo.IsManagementAccount {
				l.Info("Auto-detected: running from management account")
			} else if ec.OrganizationInfo.IsDelegatedAdmin {
				l.WithField("services", ec.OrganizationInfo.DelegatedServices).Info("Auto-detected: running from delegated admin account")
			} else {
				l.Info("Auto-detected: running from member account (requires custom role)")
			}
		}
	}

	return nil
}

// GetRoleARN returns the appropriate role ARN for a target account
func (ec *AWSExecutionContext) GetRoleARN(accountID string) string {
	// Same account - no role assumption needed
	if accountID == ec.CallerIdentity.AccountID {
		return ""
	}

	// Check for custom role pattern
	if ec.Config.ExecutionContext.CustomRolePattern != "" {
		return strings.ReplaceAll(ec.Config.ExecutionContext.CustomRolePattern, "{{.AccountID}}", accountID)
	}

	// Use Control Tower execution role
	if ec.Config.ControlTower.Enabled {
		roleName := ec.Config.ControlTower.ExecutionRole.Name
		if roleName == "" {
			roleName = "AWSControlTowerExecution"
		}
		path := ec.Config.ControlTower.ExecutionRole.Path
		if path != "" && !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		if path != "" && !strings.HasSuffix(path, "/") {
			path += "/"
		}
		if path == "" {
			path = "/"
		}
		return fmt.Sprintf("arn:aws:iam::%s:role%s%s", accountID, path, roleName)
	}

	// Default: OrganizationAccountAccessRole (created by Organizations)
	return fmt.Sprintf("arn:aws:iam::%s:role/OrganizationAccountAccessRole", accountID)
}

// AssumeRoleConfig returns AWS config with assumed role credentials
func (ec *AWSExecutionContext) AssumeRoleConfig(ctx context.Context, accountID string) (aws.Config, error) {
	roleARN := ec.GetRoleARN(accountID)
	
	// No role assumption needed for same account
	if roleARN == "" {
		return ec.BaseConfig, nil
	}

	l := log.WithFields(log.Fields{
		"action":    "AssumeRoleConfig",
		"accountID": accountID,
		"roleARN":   roleARN,
	})

	l.Debug("Assuming role for cross-account access")

	// Create STS assume role provider
	provider := stscreds.NewAssumeRoleProvider(ec.stsClient, roleARN, func(o *stscreds.AssumeRoleOptions) {
		o.RoleSessionName = "vault-secret-sync"
	})

	// Create new config with assumed role credentials
	assumedConfig := ec.BaseConfig.Copy()
	assumedConfig.Credentials = aws.NewCredentialsCache(provider)

	return assumedConfig, nil
}

// CanAccessIdentityCenter checks if we can access Identity Center
func (ec *AWSExecutionContext) CanAccessIdentityCenter() bool {
	// Management account always has access
	if ec.OrganizationInfo != nil && ec.OrganizationInfo.IsManagementAccount {
		return true
	}

	// Delegated admin for SSO
	if ec.OrganizationInfo != nil && ec.OrganizationInfo.IsDelegatedAdmin {
		for _, svc := range ec.OrganizationInfo.DelegatedServices {
			if strings.Contains(svc, "sso") {
				return true
			}
		}
	}

	return false
}

// CanAccessOrganizations checks if we can access Organizations API
func (ec *AWSExecutionContext) CanAccessOrganizations() bool {
	// Management account always has access
	if ec.OrganizationInfo != nil && ec.OrganizationInfo.IsManagementAccount {
		return true
	}

	// Check for organizations delegation
	if ec.OrganizationInfo != nil && ec.OrganizationInfo.IsDelegatedAdmin {
		for _, svc := range ec.OrganizationInfo.DelegatedServices {
			if strings.Contains(svc, "organizations") {
				return true
			}
		}
	}

	return false
}

// GetIdentityCenterClient returns an Identity Center client if accessible
func (ec *AWSExecutionContext) GetIdentityCenterClient(ctx context.Context) (*ssoadmin.Client, error) {
	if !ec.Config.IdentityCenter.Enabled {
		return nil, fmt.Errorf("identity center not enabled in config")
	}

	if !ec.CanAccessIdentityCenter() {
		return nil, fmt.Errorf("no access to Identity Center from this execution context")
	}

	if ec.ssoClient == nil {
		ec.ssoClient = ssoadmin.NewFromConfig(ec.BaseConfig)
	}

	return ec.ssoClient, nil
}

// ListOrganizationAccounts returns all accounts in the organization
func (ec *AWSExecutionContext) ListOrganizationAccounts(ctx context.Context) ([]AccountInfo, error) {
	if !ec.CanAccessOrganizations() {
		return nil, fmt.Errorf("no access to Organizations API from this execution context")
	}

	var accounts []AccountInfo
	paginator := organizations.NewListAccountsPaginator(ec.orgClient, &organizations.ListAccountsInput{})

	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list accounts: %w", err)
		}

		for _, acct := range output.Accounts {
			accounts = append(accounts, AccountInfo{
				ID:     aws.ToString(acct.Id),
				Name:   aws.ToString(acct.Name),
				Email:  aws.ToString(acct.Email),
				Status: string(acct.Status),
			})
		}
	}

	return accounts, nil
}

// ListAccountsInOU returns accounts in a specific Organizational Unit
func (ec *AWSExecutionContext) ListAccountsInOU(ctx context.Context, ouID string) ([]AccountInfo, error) {
	if !ec.CanAccessOrganizations() {
		return nil, fmt.Errorf("no access to Organizations API from this execution context")
	}

	var accounts []AccountInfo
	paginator := organizations.NewListAccountsForParentPaginator(ec.orgClient, &organizations.ListAccountsForParentInput{
		ParentId: aws.String(ouID),
	})

	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list accounts in OU %s: %w", ouID, err)
		}

		for _, acct := range output.Accounts {
			accounts = append(accounts, AccountInfo{
				ID:     aws.ToString(acct.Id),
				Name:   aws.ToString(acct.Name),
				Email:  aws.ToString(acct.Email),
				Status: string(acct.Status),
			})
		}
	}

	return accounts, nil
}

// ListChildOUs returns child Organizational Units for a given parent OU
func (ec *AWSExecutionContext) ListChildOUs(ctx context.Context, parentID string) ([]string, error) {
	if !ec.CanAccessOrganizations() {
		return nil, fmt.Errorf("no access to Organizations API from this execution context")
	}

	var childOUs []string
	paginator := organizations.NewListOrganizationalUnitsForParentPaginator(ec.orgClient, &organizations.ListOrganizationalUnitsForParentInput{
		ParentId: aws.String(parentID),
	})

	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list child OUs for %s: %w", parentID, err)
		}

		for _, ou := range output.OrganizationalUnits {
			childOUs = append(childOUs, aws.ToString(ou.Id))
		}
	}

	return childOUs, nil
}

// AccountInfo contains basic AWS account information
type AccountInfo struct {
	ID     string
	Name   string
	Email  string
	Status string
	Tags   map[string]string
}

// Summary returns a summary of the execution context
func (ec *AWSExecutionContext) Summary() string {
	var sb strings.Builder

	sb.WriteString("AWS Execution Context:\n")
	sb.WriteString(fmt.Sprintf("  Account ID: %s\n", ec.CallerIdentity.AccountID))
	sb.WriteString(fmt.Sprintf("  ARN: %s\n", ec.CallerIdentity.ARN))

	if ec.OrganizationInfo != nil {
		sb.WriteString(fmt.Sprintf("  Organization ID: %s\n", ec.OrganizationInfo.ID))
		sb.WriteString(fmt.Sprintf("  Management Account: %s\n", ec.OrganizationInfo.MasterAccountID))

		if ec.OrganizationInfo.IsManagementAccount {
			sb.WriteString("  Role: Management Account ⚠️\n")
		} else if ec.OrganizationInfo.IsDelegatedAdmin {
			sb.WriteString("  Role: Delegated Administrator ✓\n")
			sb.WriteString(fmt.Sprintf("  Delegated Services: %v\n", ec.OrganizationInfo.DelegatedServices))
		} else {
			sb.WriteString("  Role: Member Account\n")
		}
	}

	sb.WriteString(fmt.Sprintf("  Control Tower: %v\n", ec.Config.ControlTower.Enabled))
	if ec.Config.ControlTower.Enabled {
		sb.WriteString(fmt.Sprintf("  Execution Role: %s\n", ec.Config.ControlTower.ExecutionRole.Name))
	}

	sb.WriteString(fmt.Sprintf("  Identity Center Access: %v\n", ec.CanAccessIdentityCenter()))
	sb.WriteString(fmt.Sprintf("  Organizations Access: %v\n", ec.CanAccessOrganizations()))

	return sb.String()
}
