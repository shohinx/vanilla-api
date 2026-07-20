package models

import "testing"

func TestMenuItemsPreservesOrderAndSupportsEarlyStop(t *testing.T) {
	menu := Menu{Categories: []Category{
		{Items: []Item{{ID: 1}, {ID: 2}}},
		{Items: []Item{{ID: 3}}},
	}}

	var visited []int64
	for item := range menu.Items() {
		visited = append(visited, item.ID)
		if item.ID == 2 {
			break
		}
	}

	if len(visited) != 2 || visited[0] != 1 || visited[1] != 2 {
		t.Fatalf("unexpected traversal order: %v", visited)
	}
}
