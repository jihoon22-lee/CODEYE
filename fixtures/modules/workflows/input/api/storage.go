package api

import "sync"

var shelf = struct {
	sync.Mutex
	cups map[string]bool
}{cups: map[string]bool{}}

func removeFromShelf(cup string) {
	shelf.Lock()
	defer shelf.Unlock()
	delete(shelf.cups, cup)
}

func cupsOnShelf() []string {
	shelf.Lock()
	defer shelf.Unlock()
	names := []string{}
	for name := range shelf.cups {
		names = append(names, name)
	}
	return names
}
