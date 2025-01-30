package permissions

// PERM CACHE
type PermissionCache interface {
	Get(key string) (bool, bool)
	Set(key string, value bool)
	Clear()
}

// CACHE
type InMemoryCache struct {
	data map[string]bool
}

func NewInMemoryCache() *InMemoryCache {
	return &InMemoryCache{
		data: make(map[string]bool),
	}
}

func (c *InMemoryCache) Get(key string) (bool, bool) {
	val, exists := c.data[key]
	return val, exists
}

func (c *InMemoryCache) Set(key string, value bool) {
	c.data[key] = value
}

func (c *InMemoryCache) Clear() {
	c.data = make(map[string]bool)
}
