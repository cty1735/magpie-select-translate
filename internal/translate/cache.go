package translate

import (
	"container/list"
	"crypto/sha1"
	"encoding/hex"
	"sync"
)

// Cache 是容量固定的 LRU，只在内存里，不落盘（不把原文写到文件）。
type Cache struct {
	mu    sync.Mutex
	max   int
	ll    *list.List
	items map[string]*list.Element
}

type cacheEntry struct {
	key   string
	value string
}

func NewCache(max int) *Cache {
	if max <= 0 {
		max = 512
	}
	return &Cache{max: max, ll: list.New(), items: map[string]*list.Element{}}
}

func CacheKey(text, target, model string) string {
	sum := sha1.Sum([]byte(text + "|" + target + "|" + model))
	return hex.EncodeToString(sum[:])
}

func (c *Cache) Get(key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.ll.MoveToFront(el)
		return el.Value.(*cacheEntry).value, true
	}
	return "", false
}

func (c *Cache) Put(key, value string) {
	if value == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		el.Value.(*cacheEntry).value = value
		c.ll.MoveToFront(el)
		return
	}
	el := c.ll.PushFront(&cacheEntry{key: key, value: value})
	c.items[key] = el
	for c.ll.Len() > c.max {
		last := c.ll.Back()
		if last == nil {
			break
		}
		c.ll.Remove(last)
		delete(c.items, last.Value.(*cacheEntry).key)
	}
}

func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len()
}
