package secret

import "sync"

// Provider lazily loads and caches per-directory vaults, so each project seals its secrets with the
// vault.key beside its own project.db (ADR-0049 self-contained projects) rather than the instance master
// key. First access to a directory loads (and, if absent, generates 0600) that directory's key.
type Provider struct {
	mu     sync.Mutex
	vaults map[string]*Vault
}

// NewProvider returns an empty vault cache.
func NewProvider() *Provider { return &Provider{vaults: make(map[string]*Vault)} }

// For returns the vault whose key lives in dir, loading and caching it on first request. It uses the
// directory's own key file only (never OSB_VAULT_KEY) so project vaults stay independent of the global one.
func (p *Provider) For(dir string) (*Vault, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if v, ok := p.vaults[dir]; ok {
		return v, nil
	}
	v, err := LoadVaultDir(dir)
	if err != nil {
		return nil, err
	}
	p.vaults[dir] = v
	return v, nil
}
