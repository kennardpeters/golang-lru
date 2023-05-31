package simplelru

import (
	"errors"
	"time"
)

// EvictCallback is used to get a callback when a cache entry is evicted
type EvictCallback func(key interface{}, value interface{})

// LRU implements a non-thread safe fixed size LRU cache
type LRU struct {
	size      int
	evictList *List
	freeList  *List
	items     map[interface{}]*Element
	expire    time.Duration
	onEvict   EvictCallback
}

// entry is used to hold a value in the evictList
type entry struct {
	key    interface{}
	value  interface{}
	expire *time.Time
}

func (e *entry) IsExpired() bool {
	if e.expire == nil {
		return false
	}
	return time.Now().After(*e.expire)
}

// NewLRU constructs an LRU of the given size
func NewLRU(size int, onEvict EvictCallback) (*LRU, error) {
	if size <= 0 {
		return nil, errors.New("Must provide a positive size")
	}
	c := &LRU{
		size:      size,
		evictList: New(),
		freeList:  New(),
		items:     make(map[interface{}]*Element),
		expire:    0,
		onEvict:   onEvict,
	}
	for i := 0; i < size; i++ {
		c.freeList.PushFront(&entry{})
	}
	return c, nil
}

// NewLRUWithExpire contrusts an LRU of the given size and expire time
func NewLRUWithExpire(size int, expire time.Duration, onEvict EvictCallback) (*LRU, error) {
	if size <= 0 {
		return nil, errors.New("Must provide a positive size")
	}
	c := &LRU{
		size:      size,
		evictList: New(),
		freeList:  New(),
		items:     make(map[interface{}]*Element),
		expire:    expire,
		onEvict:   onEvict,
	}
	for i := 0; i < size; i++ {
		c.freeList.PushFront(&entry{})
	}

	return c, nil
}

// Purge is used to completely clear the cache
func (c *LRU) Purge() {
	for k, v := range c.items {
		if c.onEvict != nil {
			c.onEvict(k, v.Value.(*entry).value)
		}
		delete(c.items, k)
	}
	c.evictList.Init()
	c.freeList.Init()
	for i := 0; i < c.size; i++ {
		c.freeList.PushFront(&entry{})
	}
}

// Add adds a value to the cache.  Returns true if an eviction occurred.
func (c *LRU) Add(key, value interface{}) bool {
	return c.AddEx(key, value, 0)
}

// AddEx adds a value to the cache with expire.  Returns true if an eviction occurred.
func (c *LRU) AddEx(key, value interface{}, expire time.Duration) bool {
	var ex *time.Time = nil
	if expire > 0 {
		expire := time.Now().Add(expire)
		ex = &expire
	} else if c.expire > 0 {
		expire := time.Now().Add(c.expire)
		ex = &expire
	}
	// Check for existing item
	if ent, ok := c.items[key]; ok {
		c.evictList.MoveToFront(ent)
		ent.Value.(*entry).value = value
		ent.Value.(*entry).expire = ex
		return false
	}

	evict := c.evictList.Len() >= c.size
	// Verify size not exceeded
	if evict {
		c.removeOldest()
	}

	// Add new item
	ent := c.freeList.Front()
	ent.Value.(*entry).key = key
	ent.Value.(*entry).value = value
	ent.Value.(*entry).expire = ex
	c.freeList.Remove(ent)
	c.evictList.PushElementFront(ent)
	c.items[key] = ent

	return evict
}

// Get looks up a key's value from the cache.
func (c *LRU) Get(key interface{}) (value interface{}, ok bool) {
	if ent, ok := c.items[key]; ok {
		if ent.Value.(*entry).IsExpired() {
			return nil, false
		}
		c.evictList.MoveToFront(ent)
		return ent.Value.(*entry).value, true
	}
	return
}

// Check if a key is in the cache, without updating the recent-ness
// or deleting it for being stale.
func (c *LRU) Contains(key interface{}) (ok bool) {
	if ent, ok := c.items[key]; ok {
		if ent.Value.(*entry).IsExpired() {
			return false
		}
		return ok
	}
	return
}

// Returns the key value (or undefined if not found) without updating
// the "recently used"-ness of the key.
func (c *LRU) Peek(key interface{}) (value interface{}, ok bool) {
	v, _, ok := c.PeekWithExpireTime(key)
	return v, ok
}

// Returns the key value (or undefined if not found) and its associated expire
// time without updating the "recently used"-ness of the key.
func (c *LRU) PeekWithExpireTime(key interface{}) (
	value interface{}, expire *time.Time, ok bool) {
	if ent, ok := c.items[key]; ok {
		if ent.Value.(*entry).IsExpired() {
			return nil, nil, false
		}
		return ent.Value.(*entry).value, ent.Value.(*entry).expire, true
	}
	return nil, nil, ok
}

// Remove removes the provided key from the cache, returning if the
// key was contained.
func (c *LRU) Remove(key interface{}) bool {
	if ent, ok := c.items[key]; ok {
		c.removeElement(ent)
		return true
	}
	return false
}

// RemoveOldest removes the oldest item from the cache.
func (c *LRU) RemoveOldest() (interface{}, interface{}, bool) {
	ent := c.evictList.Back()
	if ent != nil {
		c.removeElement(ent)
		kv := ent.Value.(*entry)
		return kv.key, kv.value, true
	}
	return nil, nil, false
}

// GetOldest returns the oldest entry
func (c *LRU) GetOldest() (interface{}, interface{}, bool) {
	ent := c.evictList.Back()
	if ent != nil {
		kv := ent.Value.(*entry)
		return kv.key, kv.value, true
	}
	return nil, nil, false
}

// Keys returns a slice of the keys in the cache, from oldest to newest.
func (c *LRU) Keys() []interface{} {
	keys := make([]interface{}, len(c.items))
	i := 0
	for ent := c.evictList.Back(); ent != nil; ent = ent.Prev() {
		keys[i] = ent.Value.(*entry).key
		i++
	}
	return keys
}

// Len returns the number of items in the cache.
func (c *LRU) Len() int {
	return c.evictList.Len()
}

// Resize changes the cache size.
func (c *LRU) Resize(size int) (evicted int) {
	diff := c.Len() - size
	if diff < 0 {
		diff = 0
	}
	for i := 0; i < diff; i++ {
		c.removeOldest()
	}
	c.size = size
	return diff
}

// removeOldest removes the oldest item from the cache.
func (c *LRU) removeOldest() {
	ent := c.evictList.Back()
	if ent != nil {
		c.removeElement(ent)
	}
}

// removeElement is used to remove a given list element from the cache
func (c *LRU) removeElement(e *Element) {
	c.evictList.Remove(e)
	c.freeList.PushElementFront(e)
	kv := e.Value.(*entry)
	delete(c.items, kv.key)
	if c.onEvict != nil {
		c.onEvict(kv.key, kv.value)
	}
}
