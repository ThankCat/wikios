package service

import (
	"sync"
	"time"

	"wikios/internal/retrieval"
)

const customerChatCacheTTL = 5 * time.Minute

var defaultCustomerChatCache = newCustomerChatCache(customerChatCacheTTL)

type customerChatCache struct {
	mu        sync.Mutex
	ttl       time.Duration
	retrieval map[string]customerChatCacheEntry[[]retrieval.RetrievedPage]
	pages     map[string]customerChatCacheEntry[string]
}

type customerChatCacheEntry[T any] struct {
	value     T
	expiresAt time.Time
}

func newCustomerChatCache(ttl time.Duration) *customerChatCache {
	return &customerChatCache{
		ttl:       ttl,
		retrieval: map[string]customerChatCacheEntry[[]retrieval.RetrievedPage]{},
		pages:     map[string]customerChatCacheEntry[string]{},
	}
}

func (cache *customerChatCache) getRetrieval(key string) ([]retrieval.RetrievedPage, bool) {
	if cache == nil {
		return nil, false
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	entry, ok := cache.retrieval[key]
	if !ok || time.Now().After(entry.expiresAt) {
		if ok {
			delete(cache.retrieval, key)
		}
		return nil, false
	}
	return cloneRetrievedPages(entry.value), true
}

func (cache *customerChatCache) setRetrieval(key string, pages []retrieval.RetrievedPage) {
	if cache == nil || len(pages) == 0 {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.retrieval[key] = customerChatCacheEntry[[]retrieval.RetrievedPage]{
		value:     cloneRetrievedPages(pages),
		expiresAt: time.Now().Add(cache.ttl),
	}
}

func (cache *customerChatCache) getPage(key string) (string, bool) {
	if cache == nil {
		return "", false
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	entry, ok := cache.pages[key]
	if !ok || time.Now().After(entry.expiresAt) {
		if ok {
			delete(cache.pages, key)
		}
		return "", false
	}
	return entry.value, true
}

func (cache *customerChatCache) setPage(key string, content string) {
	if cache == nil || content == "" {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.pages[key] = customerChatCacheEntry[string]{
		value:     content,
		expiresAt: time.Now().Add(cache.ttl),
	}
}

func cloneRetrievedPages(pages []retrieval.RetrievedPage) []retrieval.RetrievedPage {
	return append([]retrieval.RetrievedPage(nil), pages...)
}
