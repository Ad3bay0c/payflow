// internal/service/cache.go
//
// Simple in-memory TTL cache for rarely-changing reference data.
// Used for fee tiers and tier limits — data that changes at most
// a few times per year but is read on every transfer.

package service

import (
	"sync"
	"time"

	"github.com/Ad3bay0c/payflow/payment/internal/domain"
)

// referenceDataCache holds fee tiers and tier limits with a TTL.
// After TTL expires, the next request fetches fresh data from the DB
// and repopulates the cache.
type referenceDataCache struct {
	mu sync.RWMutex

	feeTiers       []domain.FeeTier
	feeTiersExpiry time.Time

	tierLimits       map[int16]*domain.TierLimit
	tierLimitsExpiry time.Time

	ttl time.Duration
}

func newReferenceDataCache(ttl time.Duration) *referenceDataCache {
	return &referenceDataCache{
		tierLimits: make(map[int16]*domain.TierLimit),
		ttl:        ttl,
	}
}

// getFeeTiers returns cached fee tiers if still valid.
// Returns nil if the cache is empty or expired.
func (c *referenceDataCache) getFeeTiers() []domain.FeeTier {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if time.Now().After(c.feeTiersExpiry) {
		return nil // expired
	}
	return c.feeTiers
}

// setFeeTiers stores fee tiers with a TTL.
func (c *referenceDataCache) setFeeTiers(tiers []domain.FeeTier) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.feeTiers = tiers
	c.feeTiersExpiry = time.Now().Add(c.ttl)
}

// getTierLimit returns a cached tier limit if still valid.
// Returns nil if not cached or expired.
func (c *referenceDataCache) getTierLimit(tier int16) *domain.TierLimit {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if time.Now().After(c.tierLimitsExpiry) {
		return nil // expired
	}
	return c.tierLimits[tier]
}

// setTierLimit stores a tier limit with a TTL.
func (c *referenceDataCache) setTierLimit(limit *domain.TierLimit) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.tierLimits[limit.Tier] = limit
	c.tierLimitsExpiry = time.Now().Add(c.ttl)
}
