package state

import (
	"crypto/sha256"
	"encoding/json"
)

type Cache struct {
	ServiceState map[string]string
	OpenPorts    map[int]bool
	JavaHash     [32]byte
}

func NewCache() *Cache {
	return &Cache{
		ServiceState: make(map[string]string),
		OpenPorts:    make(map[int]bool),
	}
}

func (c *Cache) UpdateJavaSnapshot(snapshot any) bool {
	data, _ := json.Marshal(snapshot)
	h := sha256.Sum256(data)
	if h == c.JavaHash {
		return false
	}
	c.JavaHash = h
	return true
}
