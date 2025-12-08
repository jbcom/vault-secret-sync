package backend

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/hashicorp/vault/sdk/logical"
	"github.com/jbcom/secretsync/api/v1alpha1"
	"github.com/jbcom/secretsync/internal/metrics"
	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
)

// FileBackend reads VaultSecretSync configurations from YAML files
type FileBackend struct {
	ConfigDir string
	Watch     bool
	watcher   *fsnotify.Watcher
}

func NewFileBackend() *FileBackend {
	return &FileBackend{}
}

func (b *FileBackend) Type() BackendType {
	return BackendTypeFile
}

func (b *FileBackend) Start(ctx context.Context, params map[string]any) error {
	l := log.WithFields(log.Fields{
		"action":  "FileBackend.Start",
		"backend": "file",
	})
	l.Info("starting file backend")

	// Get config directory from params
	if dir, ok := params["configDir"].(string); ok {
		b.ConfigDir = dir
	}
	if b.ConfigDir == "" {
		b.ConfigDir = "/config/syncs"
	}

	// Check if we should watch for changes
	if watch, ok := params["watch"].(bool); ok {
		b.Watch = watch
	}

	l = l.WithField("configDir", b.ConfigDir)

	// Initial load
	if err := b.loadConfigs(); err != nil {
		l.WithError(err).Error("failed to load initial configs")
		return err
	}

	// Setup file watcher if enabled
	if b.Watch {
		if err := b.startWatcher(ctx); err != nil {
			l.WithError(err).Warn("failed to start file watcher, continuing without watch")
		}
	}

	// Trigger initial sync for all configs
	if triggerInitial, ok := params["triggerInitial"].(bool); ok && triggerInitial {
		b.triggerAllSyncs(ctx)
	}

	l.Info("file backend started")
	return nil
}

func (b *FileBackend) loadConfigs() error {
	l := log.WithFields(log.Fields{
		"action":    "FileBackend.loadConfigs",
		"configDir": b.ConfigDir,
	})
	l.Debug("loading configs from directory")

	// Check if directory exists
	if _, err := os.Stat(b.ConfigDir); os.IsNotExist(err) {
		l.Debug("config directory does not exist, creating")
		if err := os.MkdirAll(b.ConfigDir, 0700); err != nil {
			return fmt.Errorf("failed to create config directory: %w", err)
		}
		return nil
	}

	// Find all YAML files
	files, err := filepath.Glob(filepath.Join(b.ConfigDir, "*.yaml"))
	if err != nil {
		return fmt.Errorf("failed to glob config files: %w", err)
	}

	ymlFiles, err := filepath.Glob(filepath.Join(b.ConfigDir, "*.yml"))
	if err != nil {
		return fmt.Errorf("failed to glob yml files: %w", err)
	}
	files = append(files, ymlFiles...)

	l.WithField("fileCount", len(files)).Debug("found config files")

	// Load each file
	for _, file := range files {
		if err := b.loadConfigFile(file); err != nil {
			l.WithError(err).WithField("file", file).Warn("failed to load config file")
			continue
		}
	}

	metrics.RegisterServiceHealth("file-backend", metrics.ServiceHealthStatusOK)
	return nil
}

func (b *FileBackend) loadConfigFile(path string) error {
	l := log.WithFields(log.Fields{
		"action": "FileBackend.loadConfigFile",
		"file":   path,
	})
	l.Debug("loading config file")

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}

	// Split by YAML document separator
	docs := strings.Split(string(data), "---")

	for _, doc := range docs {
		doc = strings.TrimSpace(doc)
		if doc == "" {
			continue
		}

		var sync v1alpha1.VaultSecretSync
		if err := yaml.Unmarshal([]byte(doc), &sync); err != nil {
			l.WithError(err).Debug("failed to unmarshal as VaultSecretSync, trying raw spec")
			
			// Try parsing as raw spec (for simpler config format)
			var rawConfig map[string]interface{}
			if err := yaml.Unmarshal([]byte(doc), &rawConfig); err != nil {
				return fmt.Errorf("failed to unmarshal config: %w", err)
			}

			// Check if it's a Kubernetes-style manifest
			if kind, ok := rawConfig["kind"].(string); ok && kind == "VaultSecretSync" {
				// Re-parse with proper structure
				if err := yaml.Unmarshal([]byte(doc), &sync); err != nil {
					return fmt.Errorf("failed to unmarshal VaultSecretSync: %w", err)
				}
			} else {
				continue // Skip non-VaultSecretSync documents
			}
		}

		// Skip if no name
		if sync.Name == "" {
			// Try to derive name from filename
			sync.Name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		}

		// Default namespace
		if sync.Namespace == "" {
			sync.Namespace = "default"
		}

		l.WithFields(log.Fields{
			"name":      sync.Name,
			"namespace": sync.Namespace,
		}).Debug("loaded sync config")

		if err := AddSyncConfig(sync); err != nil {
			l.WithError(err).Warn("failed to add sync config")
			continue
		}
	}

	return nil
}

func (b *FileBackend) startWatcher(ctx context.Context) error {
	l := log.WithFields(log.Fields{
		"action":    "FileBackend.startWatcher",
		"configDir": b.ConfigDir,
	})

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create watcher: %w", err)
	}
	b.watcher = watcher

	if err := watcher.Add(b.ConfigDir); err != nil {
		return fmt.Errorf("failed to watch directory: %w", err)
	}

	l.Info("started file watcher")

	go func() {
		defer watcher.Close()
		for {
			select {
			case <-ctx.Done():
				l.Debug("stopping file watcher")
				return
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				b.handleFileEvent(ctx, event)
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				l.WithError(err).Error("file watcher error")
			}
		}
	}()

	return nil
}

func (b *FileBackend) handleFileEvent(ctx context.Context, event fsnotify.Event) {
	l := log.WithFields(log.Fields{
		"action": "FileBackend.handleFileEvent",
		"file":   event.Name,
		"op":     event.Op.String(),
	})

	// Only handle YAML files
	ext := filepath.Ext(event.Name)
	if ext != ".yaml" && ext != ".yml" {
		return
	}

	l.Debug("handling file event")

	switch {
	case event.Op&fsnotify.Create == fsnotify.Create, event.Op&fsnotify.Write == fsnotify.Write:
		// Small delay to ensure file is fully written
		time.Sleep(100 * time.Millisecond)
		if err := b.loadConfigFile(event.Name); err != nil {
			l.WithError(err).Warn("failed to reload config")
			return
		}
		// Trigger sync for updated configs
		b.triggerSyncsFromFile(ctx, event.Name)

	case event.Op&fsnotify.Remove == fsnotify.Remove:
		// Remove configs from this file
		// Note: This is simplified - in practice we'd need to track which configs came from which file
		l.Debug("file removed, config will remain until next full reload")
	}
}

func (b *FileBackend) triggerAllSyncs(ctx context.Context) {
	l := log.WithFields(log.Fields{
		"action": "FileBackend.triggerAllSyncs",
	})
	l.Info("triggering initial sync for all configs")

	for name := range SyncConfigs {
		cfg := SyncConfigs[name]
		if ManualTrigger != nil {
			if err := ManualTrigger(ctx, cfg, logical.UpdateOperation); err != nil {
				l.WithError(err).WithField("config", name).Warn("failed to trigger sync")
			}
		}
	}
}

func (b *FileBackend) triggerSyncsFromFile(ctx context.Context, path string) {
	l := log.WithFields(log.Fields{
		"action": "FileBackend.triggerSyncsFromFile",
		"file":   path,
	})
	l.Debug("triggering syncs from updated file")

	// For now, trigger all syncs when a file changes
	// A more sophisticated implementation would track file->config mappings
	b.triggerAllSyncs(ctx)
}

// TriggerSync triggers a sync for a specific config by name
func (b *FileBackend) TriggerSync(ctx context.Context, namespace, name string, op logical.Operation) error {
	internalName := InternalName(namespace, name)
	cfg, err := GetSyncConfigByName(internalName)
	if err != nil {
		return fmt.Errorf("config not found: %s", internalName)
	}

	if ManualTrigger == nil {
		return fmt.Errorf("ManualTrigger not initialized")
	}

	return ManualTrigger(ctx, cfg, op)
}

// LoadFromPipelineConfig loads VaultSecretSync configs generated by the pipeline
func (b *FileBackend) LoadFromPipelineConfig(configs []v1alpha1.VaultSecretSync) error {
	l := log.WithFields(log.Fields{
		"action": "FileBackend.LoadFromPipelineConfig",
		"count":  len(configs),
	})
	l.Debug("loading configs from pipeline")

	for _, cfg := range configs {
		if cfg.Namespace == "" {
			cfg.Namespace = "default"
		}
		if err := AddSyncConfig(cfg); err != nil {
			l.WithError(err).WithField("name", cfg.Name).Warn("failed to add config")
			continue
		}
	}

	return nil
}

// GetAllConfigs returns all loaded sync configurations
func GetAllConfigs() []v1alpha1.VaultSecretSync {
	configs := make([]v1alpha1.VaultSecretSync, 0, len(SyncConfigs))
	for _, cfg := range SyncConfigs {
		configs = append(configs, cfg)
	}
	return configs
}
