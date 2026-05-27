package service

import (
	"sync"
	"time"

	"wikios/internal/retrieval"
)

const publicAnswerCacheTTL = 5 * time.Minute

var defaultPublicAnswerCache = newPublicAnswerCache(publicAnswerCacheTTL)

type publicAnswerCache struct {
	mu        sync.Mutex
	ttl       time.Duration
	retrieval map[string]publicAnswerCacheEntry[[]retrieval.RetrievedPage]
	pages     map[string]publicAnswerCacheEntry[string]
}

type publicAnswerCacheEntry[T any] struct {
	value     T
	expiresAt time.Time
}

func newPublicAnswerCache(ttl time.Duration) *publicAnswerCache {
	return &publicAnswerCache{
		ttl:       ttl,
		retrieval: map[string]publicAnswerCacheEntry[[]retrieval.RetrievedPage]{},
		pages:     map[string]publicAnswerCacheEntry[string]{},
	}
}

func (cache *publicAnswerCache) getRetrieval(key string) ([]retrieval.RetrievedPage, bool) {
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

func (cache *publicAnswerCache) setRetrieval(key string, pages []retrieval.RetrievedPage) {
	if cache == nil || len(pages) == 0 {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.retrieval[key] = publicAnswerCacheEntry[[]retrieval.RetrievedPage]{
		value:     cloneRetrievedPages(pages),
		expiresAt: time.Now().Add(cache.ttl),
	}
}

func (cache *publicAnswerCache) getPage(key string) (string, bool) {
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

func (cache *publicAnswerCache) setPage(key string, content string) {
	if cache == nil || content == "" {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.pages[key] = publicAnswerCacheEntry[string]{
		value:     content,
		expiresAt: time.Now().Add(cache.ttl),
	}
}

func cloneRetrievedPages(pages []retrieval.RetrievedPage) []retrieval.RetrievedPage {
	return append([]retrieval.RetrievedPage(nil), pages...)
}
