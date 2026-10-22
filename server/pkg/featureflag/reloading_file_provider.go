package featureflag

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"time"
)

// ReloadingFileProvider reloads a YAML flag file when its metadata changes.
// Writers should replace the file atomically so readers observe a complete
// configuration, never a partial write.
type ReloadingFileProvider struct {
	path     string
	provider *StaticProvider
	logger   *slog.Logger

	mu      sync.Mutex
	modTime time.Time
	size    int64
}

func NewReloadingFileProvider(path string, rules map[string]Rule, logger *slog.Logger) *ReloadingFileProvider {
	p := &ReloadingFileProvider{
		path:     path,
		provider: NewStaticProvider(),
		logger:   logger,
	}
	p.provider.LoadRules(rules)
	if info, err := os.Stat(path); err == nil {
		p.modTime = info.ModTime()
		p.size = info.Size()
	}
	return p
}

func (*ReloadingFileProvider) Name() string { return "reloading-file" }

func (p *ReloadingFileProvider) Lookup(ctx context.Context, key string) (Decision, bool) {
	p.reloadIfChanged(ctx)
	return p.provider.Lookup(ctx, key)
}

func (p *ReloadingFileProvider) reloadIfChanged(ctx context.Context) {
	p.mu.Lock()
	defer p.mu.Unlock()

	info, err := os.Stat(p.path)
	if err != nil {
		if p.logger != nil {
			p.logger.WarnContext(ctx, "feature flag file stat failed", slog.String("error", err.Error()))
		}
		return
	}
	if info.ModTime().Equal(p.modTime) && info.Size() == p.size {
		return
	}

	rules, err := LoadRulesFromYAMLFile(p.path)
	p.modTime = info.ModTime()
	p.size = info.Size()
	if err != nil {
		if p.logger != nil {
			p.logger.ErrorContext(ctx, "feature flag file reload failed; keeping last valid rules", slog.String("error", err.Error()))
		}
		return
	}
	p.provider.LoadRules(rules)
}
