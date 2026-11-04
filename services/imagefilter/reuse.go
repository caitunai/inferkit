package imagefilter

import (
	"sync"
	"time"
)

type pendingCall struct {
	err    error
	done   chan struct{}
	result Result
}

type cachedResult struct {
	expires time.Time
	key     string
	result  Result
}

// The FIFO ring retains at most CacheEntries small records, never bytes or URLs.
// TTL is absolute; access removes expired entries, and insertion evicts one old
// slot in O(1). MaxRequests bounds the separate map of in-flight keys.
type resultStore struct {
	items   map[string]*cachedResult
	pending map[string]*pendingCall
	order   []*cachedResult
	mu      sync.Mutex
	ttl     time.Duration
	next    int
}

func newResultStore(c Config) *resultStore {
	return &resultStore{
		items: make(map[string]*cachedResult), pending: make(map[string]*pendingCall),
		order: make([]*cachedResult, c.CacheEntries), ttl: c.CacheTTL,
	}
}

func (s *resultStore) begin(key string) (Result, *pendingCall, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if entry := s.items[key]; entry != nil {
		if time.Now().Before(entry.expires) {
			return entry.result, nil, false
		}
		delete(s.items, key)
	}
	if call := s.pending[key]; call != nil {
		return Result{}, call, false
	}
	call := &pendingCall{done: make(chan struct{})}
	s.pending[key] = call
	return Result{}, call, true
}

func (s *resultStore) finish(key string, call *pendingCall, result Result, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	call.result, call.err = result, err
	delete(s.pending, key)
	if err == nil && len(s.order) > 0 {
		s.insert(key, result)
	}
	close(call.done)
}

func (s *resultStore) insert(key string, result Result) {
	if old := s.order[s.next]; old != nil && s.items[old.key] == old {
		delete(s.items, old.key)
	}
	entry := &cachedResult{key: key, result: result, expires: time.Now().Add(s.ttl)}
	s.order[s.next], s.items[key] = entry, entry
	s.next = (s.next + 1) % len(s.order)
}
