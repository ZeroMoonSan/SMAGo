package main

import (
	"regexp"
	"sync"
)

// regexCache memoises compiled patterns. markdown.go calls these many
// times per response, so we don't want to recompile every time.
var regexCache = &regexCacheT{patterns: map[string]*regexp.Regexp{}}

type regexCacheT struct {
	mu       sync.Mutex
	patterns map[string]*regexp.Regexp
}

func (c *regexCacheT) MustCompile(p string) *regexp.Regexp {
	c.mu.Lock()
	defer c.mu.Unlock()
	if re, ok := c.patterns[p]; ok {
		return re
	}
	re := regexp.MustCompile(p)
	c.patterns[p] = re
	return re
}
