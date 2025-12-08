// Package pipeline provides a unified secrets synchronization pipeline that works
// identically across all execution modes: CLI, Kubernetes, and Vault event-driven.
//
// Architecture:
//
//	┌─────────────────────────────────────────────────────────────────────────┐
//	│                         Pipeline Configuration                          │
//	│  (YAML file, Kubernetes CRD, ConfigMap, or programmatic)               │
//	└─────────────────────────────────────────────────────────────────────────┘
//	                                    │
//	                                    ▼
//	┌─────────────────────────────────────────────────────────────────────────┐
//	│                           Pipeline Engine                               │
//	│  • Dependency graph resolution                                          │
//	│  • Topological ordering                                                 │
//	│  • Parallel execution within levels                                     │
//	│  • Operation modes: merge, sync, pipeline (merge+sync)                  │
//	└─────────────────────────────────────────────────────────────────────────┘
//	                                    │
//	          ┌─────────────────────────┼─────────────────────────┐
//	          ▼                         ▼                         ▼
//	┌─────────────────┐       ┌─────────────────┐       ┌─────────────────┐
//	│   CLI Runner    │       │  K8s Operator   │       │  Vault Events   │
//	│  (vss pipeline) │       │  (CRD watch)    │       │  (webhook)      │
//	└─────────────────┘       └─────────────────┘       └─────────────────┘
//
// Operations:
//   - merge:    Source stores → Merge store (with inheritance resolution)
//   - sync:     Merge store → Destination stores (AWS, etc.)
//   - pipeline: merge + sync in correct dependency order
package pipeline

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/hashicorp/vault/sdk/logical"
	"github.com/robertlestak/vault-secret-sync/api/v1alpha1"
	"github.com/robertlestak/vault-secret-sync/internal/backend"
	"github.com/robertlestak/vault-secret-sync/internal/queue"
	internalSync "github.com/robertlestak/vault-secret-sync/internal/sync"
	"github.com/robertlestak/vault-secret-sync/stores/aws"
	"github.com/robertlestak/vault-secret-sync/stores/vault"
	log "github.com/sirupsen/logrus"
)

// Operation defines what the pipeline should do
type Operation string

const (
	// OperationMerge only performs the merge phase (sources → merge store)
	OperationMerge Operation = "merge"
	// OperationSync only performs the sync phase (merge store → destinations)
	OperationSync Operation = "sync"
	// OperationPipeline performs both merge and sync in order
	OperationPipeline Operation = "pipeline"
)

// Pipeline is the main orchestrator for secrets synchronization
type Pipeline struct {
	config      *Config
	graph       *Graph
	initialized bool
	mu          sync.Mutex

	// AWS context for cross-account operations
	awsCtx *AWSExecutionContext

	// S3 merge store (if configured)
	s3Store *S3MergeStore

	// Execution tracking
	results   []Result
	resultsMu sync.Mutex
}

// New creates a new Pipeline from configuration
func New(cfg *Config) (*Pipeline, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	graph, err := BuildGraph(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to build dependency graph: %w", err)
	}

	return &Pipeline{
		config: cfg,
		graph:  graph,
	}, nil
}

// NewWithContext creates a Pipeline with AWS context for dynamic target discovery
func NewWithContext(ctx context.Context, cfg *Config) (*Pipeline, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	// Initialize AWS execution context if we have AWS config
	var awsCtx *AWSExecutionContext
	var err error
	if cfg.AWS.Region != "" {
		awsCtx, err = NewAWSExecutionContext(ctx, &cfg.AWS)
		if err != nil {
			log.WithError(err).Warn("Failed to create AWS execution context, continuing without it")
		}
	}

	// Expand dynamic targets if AWS context is available
	if awsCtx != nil && len(cfg.DynamicTargets) > 0 {
		if err := ExpandDynamicTargets(ctx, cfg, awsCtx); err != nil {
			log.WithError(err).Warn("Failed to expand dynamic targets")
		}
	}

	// Build dependency graph (after dynamic target expansion)
	graph, err := BuildGraph(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to build dependency graph: %w", err)
	}

	p := &Pipeline{
		config: cfg,
		graph:  graph,
		awsCtx: awsCtx,
	}

	// Initialize S3 merge store if configured
	if cfg.MergeStore.S3 != nil {
		p.s3Store, err = NewS3MergeStore(ctx, cfg.MergeStore.S3, cfg.AWS.Region)
		if err != nil {
			return nil, fmt.Errorf("failed to create S3 merge store: %w", err)
		}
	}

	return p, nil
}

// NewFromFile creates a Pipeline from a configuration file
func NewFromFile(path string) (*Pipeline, error) {
	cfg, err := LoadConfig(path)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}
	return New(cfg)
}

// NewFromFileWithContext creates a Pipeline from a configuration file with AWS context
// This enables dynamic target discovery from Organizations and Identity Center
func NewFromFileWithContext(ctx context.Context, path string) (*Pipeline, error) {
	cfg, err := LoadConfig(path)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}
	return NewWithContext(ctx, cfg)
}

// Options configures pipeline execution
type Options struct {
	// Operation to perform (merge, sync, or pipeline)
	Operation Operation

	// Targets to process (empty = all targets)
	Targets []string

	// DryRun performs all operations without making changes
	DryRun bool

	// ContinueOnError continues processing even if some targets fail
	ContinueOnError bool

	// Parallelism controls max concurrent operations per phase
	Parallelism int
}

// DefaultOptions returns sensible defaults
func DefaultOptions() Options {
	return Options{
		Operation:       OperationPipeline,
		DryRun:          false,
		ContinueOnError: true,
		Parallelism:     4,
	}
}

// Result represents the outcome of a single target operation
type Result struct {
	Target    string        `json:"target"`
	Phase     string        `json:"phase"` // "merge" or "sync"
	Operation string        `json:"operation"`
	Success   bool          `json:"success"`
	Error     error         `json:"error,omitempty"`
	Duration  time.Duration `json:"duration"`
	Details   ResultDetails `json:"details,omitempty"`
}

// ResultDetails contains additional information about the operation
type ResultDetails struct {
	SecretsProcessed int      `json:"secrets_processed,omitempty"`
	SourcePaths      []string `json:"source_paths,omitempty"`
	DestinationPath  string   `json:"destination_path,omitempty"`
	RoleARN          string   `json:"role_arn,omitempty"`
	FailedImports    []string `json:"failed_imports,omitempty"`
}

// Run executes the pipeline with the given options
func (p *Pipeline) Run(ctx context.Context, opts Options) ([]Result, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	l := log.WithFields(log.Fields{
		"action":    "Pipeline.Run",
		"operation": opts.Operation,
		"dryRun":    opts.DryRun,
	})

	// Initialize infrastructure
	if err := p.initialize(ctx); err != nil {
		return nil, fmt.Errorf("failed to initialize pipeline: %w", err)
	}

	// Reset results (protected by mutex for concurrent safety)
	p.resultsMu.Lock()
	p.results = nil
	p.resultsMu.Unlock()

	// Resolve targets
	targets := p.resolveTargets(opts.Targets)
	l.WithField("targets", targets).Info("Starting pipeline execution")

	// Apply options from config if not specified
	if opts.Parallelism <= 0 {
		opts.Parallelism = p.config.Pipeline.Merge.Parallel
		if opts.Parallelism <= 0 {
			opts.Parallelism = 4
		}
	}

	// Execute based on operation
	switch opts.Operation {
	case OperationMerge:
		return p.runMerge(ctx, targets, opts)
	case OperationSync:
		return p.runSync(ctx, targets, opts)
	case OperationPipeline:
		return p.runPipeline(ctx, targets, opts)
	default:
		return nil, fmt.Errorf("unknown operation: %s", opts.Operation)
	}
}

// initialize sets up the sync infrastructure
func (p *Pipeline) initialize(ctx context.Context) error {
	if p.initialized {
		return nil
	}

	l := log.WithFields(log.Fields{
		"action": "Pipeline.initialize",
	})
	l.Debug("Initializing pipeline infrastructure")

	// Initialize ManualTrigger
	backend.ManualTrigger = internalSync.ManualTrigger

	// Initialize queue
	if queue.Q == nil {
		if err := queue.Init(queue.QueueTypeMemory, nil); err != nil {
			return fmt.Errorf("failed to initialize queue: %w", err)
		}
	}

	// Set default stores
	p.setDefaultStores()

	// Start event processor
	go func() {
		workerPoolSize := p.config.Pipeline.Merge.Parallel
		if workerPoolSize <= 0 {
			workerPoolSize = 4
		}
		if err := internalSync.EventProcessor(ctx, workerPoolSize, workerPoolSize); err != nil {
			l.WithError(err).Error("Event processor exited")
		}
	}()

	// Allow processor to start
	// TODO: Replace with proper synchronization - EventProcessor should signal readiness via channel
	time.Sleep(100 * time.Millisecond)

	p.initialized = true
	l.Info("Pipeline infrastructure initialized")
	return nil
}

// setDefaultStores configures default store settings
func (p *Pipeline) setDefaultStores() {
	stores := &v1alpha1.StoreConfig{
		Vault: &vault.VaultClient{
			Address:   p.config.Vault.Address,
			Namespace: p.config.Vault.Namespace,
		},
		AWS: &aws.AwsClient{
			Region: p.config.AWS.Region,
		},
	}
	internalSync.SetStoreDefaults(stores)
}

// resolveTargets returns the targets to process, including dependencies
func (p *Pipeline) resolveTargets(requested []string) []string {
	if len(requested) == 0 {
		return p.graph.TopologicalOrder()
	}
	return p.graph.IncludeDependencies(requested)
}

// runMerge executes only the merge phase
func (p *Pipeline) runMerge(ctx context.Context, targets []string, opts Options) ([]Result, error) {
	l := log.WithFields(log.Fields{
		"action":  "Pipeline.runMerge",
		"targets": targets,
	})
	l.Info("Starting merge phase")

	results, err := p.executeMergePhase(ctx, targets, opts)
	p.resultsMu.Lock()
	p.results = results
	p.resultsMu.Unlock()
	return results, err
}

// runSync executes only the sync phase
func (p *Pipeline) runSync(ctx context.Context, targets []string, opts Options) ([]Result, error) {
	l := log.WithFields(log.Fields{
		"action":  "Pipeline.runSync",
		"targets": targets,
	})
	l.Info("Starting sync phase")

	results, err := p.executeSyncPhase(ctx, targets, opts)
	p.resultsMu.Lock()
	p.results = results
	p.resultsMu.Unlock()
	return results, err
}

// runPipeline executes both merge and sync phases
func (p *Pipeline) runPipeline(ctx context.Context, targets []string, opts Options) ([]Result, error) {
	l := log.WithFields(log.Fields{
		"action":  "Pipeline.runPipeline",
		"targets": targets,
	})
	l.Info("Starting full pipeline (merge + sync)")

	var allResults []Result

	// Merge phase
	l.Info("Phase 1: Merge")
	mergeResults, mergeErr := p.executeMergePhase(ctx, targets, opts)
	allResults = append(allResults, mergeResults...)

	if mergeErr != nil && !opts.ContinueOnError {
		p.resultsMu.Lock()
		p.results = allResults
		p.resultsMu.Unlock()
		return allResults, fmt.Errorf("merge phase failed: %w", mergeErr)
	}

	// Sync phase
	l.Info("Phase 2: Sync")
	syncResults, syncErr := p.executeSyncPhase(ctx, targets, opts)
	allResults = append(allResults, syncResults...)

	p.resultsMu.Lock()
	p.results = allResults
	p.resultsMu.Unlock()

	if syncErr != nil {
		return allResults, fmt.Errorf("sync phase failed: %w", syncErr)
	}

	return allResults, nil
}

// executeMergePhase runs merge operations in dependency order
func (p *Pipeline) executeMergePhase(ctx context.Context, targets []string, opts Options) ([]Result, error) {
	var results []Result
	var lastErr error

	// Process by dependency level
	levels := p.graph.GroupByLevel()

	for levelIdx, level := range levels {
		// Filter to requested targets
		var levelTargets []string
		for _, t := range level {
			for _, requested := range targets {
				if t == requested {
					levelTargets = append(levelTargets, t)
					break
				}
			}
		}

		if len(levelTargets) == 0 {
			continue
		}

		log.WithFields(log.Fields{
			"level":   levelIdx,
			"targets": levelTargets,
		}).Debug("Processing merge level")

		// Execute level in parallel
		levelResults := p.executeParallel(ctx, levelTargets, opts.Parallelism, func(target string) Result {
			return p.mergeTarget(ctx, target, opts.DryRun)
		})

		results = append(results, levelResults...)

		// Check for errors
		for _, r := range levelResults {
			if !r.Success {
				lastErr = r.Error
				if !opts.ContinueOnError {
					return results, lastErr
				}
			}
		}
	}

	return results, lastErr
}

// executeSyncPhase runs sync operations (can be fully parallel)
func (p *Pipeline) executeSyncPhase(ctx context.Context, targets []string, opts Options) ([]Result, error) {
	results := p.executeParallel(ctx, targets, opts.Parallelism, func(target string) Result {
		return p.syncTarget(ctx, target, opts.DryRun)
	})

	var lastErr error
	for _, r := range results {
		if !r.Success {
			lastErr = r.Error
			if !opts.ContinueOnError {
				return results, lastErr
			}
		}
	}

	return results, lastErr
}

// executeParallel runs a function for each target with limited concurrency
func (p *Pipeline) executeParallel(ctx context.Context, targets []string, maxParallel int, fn func(string) Result) []Result {
	if maxParallel <= 0 {
		maxParallel = 1
	}

	results := make([]Result, len(targets))
	sem := make(chan struct{}, maxParallel)
	var wg sync.WaitGroup

	for i, target := range targets {
		select {
		case <-ctx.Done():
			results[i] = Result{
				Target:  target,
				Success: false,
				Error:   ctx.Err(),
			}
			continue
		case sem <- struct{}{}:
		}

		wg.Add(1)
		go func(idx int, t string) {
			defer wg.Done()
			defer func() { <-sem }()
			results[idx] = fn(t)
		}(i, target)
	}

	wg.Wait()
	return results
}

// mergeTarget executes merge operations for a single target
func (p *Pipeline) mergeTarget(ctx context.Context, targetName string, dryRun bool) Result {
	start := time.Now()
	l := log.WithFields(log.Fields{
		"action": "mergeTarget",
		"target": targetName,
		"dryRun": dryRun,
	})

	target, ok := p.config.Targets[targetName]
	if !ok {
		return Result{
			Target:   targetName,
			Phase:    "merge",
			Success:  false,
			Error:    fmt.Errorf("target not found"),
			Duration: time.Since(start),
		}
	}

	// Determine merge path based on merge store type
	var mergePath string
	if p.config.MergeStore.Vault != nil {
		mergePath = fmt.Sprintf("%s/%s", p.config.MergeStore.Vault.Mount, targetName)
	} else if p.s3Store != nil {
		mergePath = p.s3Store.GetMergePath(targetName)
	} else {
		return Result{
			Target:   targetName,
			Phase:    "merge",
			Success:  false,
			Error:    fmt.Errorf("no merge store configured"),
			Duration: time.Since(start),
		}
	}
	l.WithField("mergePath", mergePath).Info("Starting merge")

	var sourcePaths []string
	var failedImports []string
	var lastErr error
	successCount := 0

	for _, importName := range target.Imports {
		sourcePath := p.config.GetSourcePath(importName)
		sourcePaths = append(sourcePaths, sourcePath)

		l.WithFields(log.Fields{
			"import":     importName,
			"sourcePath": sourcePath,
		}).Debug("Processing import")

		// Use Vault merge store (standard path)
		if p.config.MergeStore.Vault != nil {
			syncConfig := p.createMergeSync(importName, targetName, sourcePath, mergePath, dryRun)

			if err := backend.AddSyncConfig(syncConfig); err != nil {
				l.WithError(err).WithField("import", importName).Error("Failed to add sync config")
				failedImports = append(failedImports, importName)
				lastErr = err
				continue
			}

			if err := backend.ManualTrigger(ctx, syncConfig, logical.UpdateOperation); err != nil {
				l.WithError(err).WithField("import", importName).Error("Failed to trigger merge")
				failedImports = append(failedImports, importName)
				lastErr = err
				continue
			}
		}

		// Use S3 merge store
		if p.s3Store != nil && !dryRun {
			// For S3, we need to read secrets from Vault and write to S3
			// This is a simplified implementation - in production you'd want
			// to properly read the secret data from the source
			secretData := map[string]interface{}{
				"_source":    importName,
				"_target":    targetName,
				"_timestamp": time.Now().UTC().Format(time.RFC3339),
			}
			if err := p.s3Store.WriteSecret(ctx, targetName, importName, secretData); err != nil {
				l.WithError(err).WithField("import", importName).Error("Failed to write to S3 merge store")
				failedImports = append(failedImports, importName)
				lastErr = err
				continue
			}
		}

		successCount++
	}

	// Allow time for async processing (only for Vault merge store)
	// TODO: Replace with proper synchronization mechanism (channels/WaitGroups)
	if p.config.MergeStore.Vault != nil {
		time.Sleep(time.Duration(len(target.Imports)*300) * time.Millisecond)
	}

	success := lastErr == nil
	l.WithFields(log.Fields{
		"duration":      time.Since(start),
		"success":       success,
		"failedImports": failedImports,
	}).Info("Merge completed")

	return Result{
		Target:    targetName,
		Phase:     "merge",
		Operation: string(OperationMerge),
		Success:   success,
		Error:     lastErr,
		Duration:  time.Since(start),
		Details: ResultDetails{
			SecretsProcessed: successCount,
			SourcePaths:      sourcePaths,
			DestinationPath:  mergePath,
			FailedImports:    failedImports,
		},
	}
}

// syncTarget syncs merged secrets to AWS for a single target
func (p *Pipeline) syncTarget(ctx context.Context, targetName string, dryRun bool) Result {
	start := time.Now()
	l := log.WithFields(log.Fields{
		"action": "syncTarget",
		"target": targetName,
		"dryRun": dryRun,
	})

	target, ok := p.config.Targets[targetName]
	if !ok {
		return Result{
			Target:   targetName,
			Phase:    "sync",
			Success:  false,
			Error:    fmt.Errorf("target not found"),
			Duration: time.Since(start),
		}
	}

	roleARN := p.config.GetRoleARN(target.AccountID)

	// Determine source path based on merge store type
	var sourcePath string
	if p.config.MergeStore.Vault != nil {
		sourcePath = fmt.Sprintf("%s/%s", p.config.MergeStore.Vault.Mount, targetName)
	} else if p.s3Store != nil {
		sourcePath = p.s3Store.GetMergePath(targetName)
	} else {
		return Result{
			Target:   targetName,
			Phase:    "sync",
			Success:  false,
			Error:    fmt.Errorf("no merge store configured"),
			Duration: time.Since(start),
		}
	}

	region := target.Region
	if region == "" {
		region = p.config.AWS.Region
	}

	l.WithFields(log.Fields{
		"accountID":  target.AccountID,
		"roleARN":    roleARN,
		"sourcePath": sourcePath,
		"region":     region,
	}).Info("Starting sync to AWS")

	// Create and execute sync
	syncConfig := p.createAWSSync(targetName, sourcePath, roleARN, region, dryRun)

	if err := backend.AddSyncConfig(syncConfig); err != nil {
		return Result{
			Target:   targetName,
			Phase:    "sync",
			Success:  false,
			Error:    fmt.Errorf("failed to add sync config: %w", err),
			Duration: time.Since(start),
		}
	}

	if err := backend.ManualTrigger(ctx, syncConfig, logical.UpdateOperation); err != nil {
		return Result{
			Target:   targetName,
			Phase:    "sync",
			Success:  false,
			Error:    fmt.Errorf("failed to trigger sync: %w", err),
			Duration: time.Since(start),
		}
	}

	// Allow time for async processing
	// TODO: Replace with proper synchronization - ManualTrigger should return completion signal
	time.Sleep(500 * time.Millisecond)

	l.WithField("duration", time.Since(start)).Info("Sync completed")

	return Result{
		Target:    targetName,
		Phase:     "sync",
		Operation: string(OperationSync),
		Success:   true,
		Duration:  time.Since(start),
		Details: ResultDetails{
			SourcePaths:     []string{sourcePath},
			DestinationPath: fmt.Sprintf("aws:%s", target.AccountID),
			RoleARN:         roleARN,
		},
	}
}

// createMergeSync creates a VaultSecretSync for merging sources
func (p *Pipeline) createMergeSync(importName, targetName, sourcePath, mergePath string, dryRun bool) v1alpha1.VaultSecretSync {
	sync := v1alpha1.VaultSecretSync{
		Spec: v1alpha1.VaultSecretSyncSpec{
			DryRun:     boolPtr(dryRun),
			SyncDelete: boolPtr(false),
			Source: &vault.VaultClient{
				Address:   p.config.Vault.Address,
				Namespace: p.config.Vault.Namespace,
				Path:      fmt.Sprintf("%s/(.*)", sourcePath),
			},
			Dest: []*v1alpha1.StoreConfig{
				{
					Vault: &vault.VaultClient{
						Address:   p.config.Vault.Address,
						Namespace: p.config.Vault.Namespace,
						Path:      fmt.Sprintf("%s/$1", mergePath),
						Merge:     true,
					},
				},
			},
		},
	}
	sync.Name = fmt.Sprintf("merge-%s-to-%s", importName, targetName)
	sync.Namespace = "pipeline"
	return sync
}

// createAWSSync creates a VaultSecretSync for syncing to AWS
func (p *Pipeline) createAWSSync(targetName, sourcePath, roleARN, region string, dryRun bool) v1alpha1.VaultSecretSync {
	sync := v1alpha1.VaultSecretSync{
		Spec: v1alpha1.VaultSecretSyncSpec{
			DryRun:     boolPtr(dryRun),
			SyncDelete: boolPtr(p.config.Pipeline.Sync.DeleteOrphans),
			Source: &vault.VaultClient{
				Address:   p.config.Vault.Address,
				Namespace: p.config.Vault.Namespace,
				Path:      fmt.Sprintf("%s/(.*)", sourcePath),
			},
			Dest: []*v1alpha1.StoreConfig{
				{
					AWS: &aws.AwsClient{
						Name:    "$1",
						Region:  region,
						RoleArn: roleARN,
					},
				},
			},
		},
	}
	sync.Name = fmt.Sprintf("sync-%s", targetName)
	sync.Namespace = "pipeline"
	return sync
}

// Config returns the pipeline configuration
func (p *Pipeline) Config() *Config {
	return p.config
}

// Graph returns the dependency graph
func (p *Pipeline) Graph() *Graph {
	return p.graph
}

// Results returns the results from the last Run
func (p *Pipeline) Results() []Result {
	p.resultsMu.Lock()
	defer p.resultsMu.Unlock()
	return p.results
}

// GenerateConfigs generates VaultSecretSync configs without executing them
// Useful for GitOps workflows or Kubernetes CRD generation
// Note: S3 merge store doesn't generate VaultSecretSync configs (it's handled differently)
func (p *Pipeline) GenerateConfigs(opts Options) ([]v1alpha1.VaultSecretSync, error) {
	var configs []v1alpha1.VaultSecretSync

	// S3 merge store doesn't use VaultSecretSync for the merge phase
	if p.config.MergeStore.Vault == nil {
		log.Warn("GenerateConfigs only supports Vault merge store; S3 merge store operations are handled inline")
	}

	targets := p.resolveTargets(opts.Targets)

	// Generate merge configs (only for Vault merge store)
	if (opts.Operation == OperationMerge || opts.Operation == OperationPipeline) && p.config.MergeStore.Vault != nil {
		for _, targetName := range targets {
			target := p.config.Targets[targetName]
			mergePath := fmt.Sprintf("%s/%s", p.config.MergeStore.Vault.Mount, targetName)

			for _, importName := range target.Imports {
				sourcePath := p.config.GetSourcePath(importName)
				cfg := p.createMergeSync(importName, targetName, sourcePath, mergePath, opts.DryRun)
				configs = append(configs, cfg)
			}
		}
	}

	// Generate sync configs (only for Vault merge store - S3 requires different handling)
	if opts.Operation == OperationSync || opts.Operation == OperationPipeline {
		for _, targetName := range targets {
			target := p.config.Targets[targetName]
			roleARN := p.config.GetRoleARN(target.AccountID)

			// Determine source path based on merge store
			var sourcePath string
			if p.config.MergeStore.Vault != nil {
				sourcePath = fmt.Sprintf("%s/%s", p.config.MergeStore.Vault.Mount, targetName)
			} else if p.config.MergeStore.S3 != nil {
				// S3 merge store - sync configs would need to read from S3
				// This is a limitation: VaultSecretSync expects Vault as source
				log.WithField("target", targetName).Warn("S3 merge store sync requires custom handling")
				continue
			}

			region := target.Region
			if region == "" {
				region = p.config.AWS.Region
			}

			cfg := p.createAWSSync(targetName, sourcePath, roleARN, region, opts.DryRun)
			configs = append(configs, cfg)
		}
	}

	return configs, nil
}

// Helper
func boolPtr(b bool) *bool {
	return &b
}
