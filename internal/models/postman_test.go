package models

import (
	"testing"
)

func TestRequestDeepCopy(t *testing.T) {
	orig := &Request{
		Method: "POST",
		URL:    URL{Raw: "https://example.com/api"},
		Header: []Header{
			{Key: "Content-Type", Value: "application/json"},
			{Key: "Authorization", Value: "Bearer token"},
		},
		Body: &Body{
			Mode: "raw",
			Raw:  `{"key": "value"}`,
		},
		Auth: &Auth{Type: "bearer"},
	}

	copy := orig.DeepCopy()

	// Verify values match
	if copy.Method != orig.Method {
		t.Errorf("Method: got %s, want %s", copy.Method, orig.Method)
	}
	if copy.URL.Raw != orig.URL.Raw {
		t.Errorf("URL: got %s, want %s", copy.URL.Raw, orig.URL.Raw)
	}

	// Verify it's a deep copy - mutating original should not affect copy
	orig.Method = "GET"
	if copy.Method != "POST" {
		t.Error("DeepCopy failed: mutation of original propagated to copy")
	}

	// Verify header slice is independent
	orig.Header[0].Key = "X-Modified"
	if copy.Header[0].Key != "Content-Type" {
		t.Error("DeepCopy failed: header mutation propagated")
	}

	// Verify Body is independent
	if copy.Body == nil {
		t.Fatal("Expected non-nil Body")
	}
	orig.Body.Raw = "modified"
	if copy.Body.Raw != `{"key": "value"}` {
		t.Error("DeepCopy failed: body mutation propagated")
	}
}

func TestRequestDeepCopy_NilBody(t *testing.T) {
	orig := &Request{
		Method: "GET",
		URL:    URL{Raw: "https://example.com"},
	}

	copy := orig.DeepCopy()
	if copy.Body != nil {
		t.Error("Expected nil Body in copy")
	}
	if copy.URL.Raw != "https://example.com" {
		t.Errorf("URL: got %s, want %s", copy.URL.Raw, "https://example.com")
	}
}

func TestRequestDeepCopy_UrlEncodedBody(t *testing.T) {
	orig := &Request{
		Method: "POST",
		URL:    URL{Raw: "https://example.com"},
		Body: &Body{
			Mode: "urlencoded",
			UrlEncoded: []UrlEncoded{
				{Key: "field1", Value: "val1"},
				{Key: "field2", Value: "val2"},
			},
		},
	}

	copy := orig.DeepCopy()
	if copy.Body == nil {
		t.Fatal("Expected non-nil Body")
	}
	if len(copy.Body.UrlEncoded) != 2 {
		t.Fatalf("Expected 2 urlencoded fields, got %d", len(copy.Body.UrlEncoded))
	}
	if copy.Body.UrlEncoded[0].Value != "val1" {
		t.Errorf("UrlEncoded[0].Value: got %s, want val1", copy.Body.UrlEncoded[0].Value)
	}

	// Verify deep copy
	orig.Body.UrlEncoded[0].Key = "modified"
	if copy.Body.UrlEncoded[0].Key != "field1" {
		t.Error("DeepCopy failed: urlencoded mutation propagated")
	}
}

func TestRequestDeepCopy_NilAuthBodyOptions(t *testing.T) {
	orig := &Request{
		Method: "GET",
		URL:    URL{Raw: "https://example.com"},
		Body: &Body{
			Mode: "raw",
			Raw:  "data",
		},
	}

	copy := orig.DeepCopy()
	if copy.Body == nil {
		t.Fatal("Expected non-nil Body")
	}
	if copy.Body.Mode != "raw" {
		t.Errorf("Body.Mode: got %s, want raw", copy.Body.Mode)
	}
}

func TestSortInsertPosition(t *testing.T) {
	items := []Item{
		{Name: "Folder A", Item: []Item{}, Order: 1},           // folder
		{Name: "Req A", Request: &Request{}, Order: 2},          // request
		{Name: "Req B", Request: &Request{}, Order: 3},          // request
	}

	t.Run("folder before requests", func(t *testing.T) {
		newItem := Item{Name: "Folder B", Item: []Item{}, Order: 1}
		pos := SortInsertPosition(items, newItem)
		if pos != 1 { // After Folder A, before Req A
			t.Errorf("expected position 1, got %d", pos)
		}
	})

	t.Run("request after folders", func(t *testing.T) {
		newItem := Item{Name: "Req C", Request: &Request{}, Order: 4}
		pos := SortInsertPosition(items, newItem)
		if pos != 3 { // At end
			t.Errorf("expected position 3, got %d", pos)
		}
	})

	t.Run("insert by order", func(t *testing.T) {
		newItem := Item{Name: "Req A.5", Request: &Request{}, Order: 2}
		pos := SortInsertPosition(items, newItem)
		// The function will iterate and find Req A at index 1 (first request after Folder A)
		// Since they're both requests and new item has order 2 vs Req A order 2,
		// it falls to alphabetical comparison: "Req A.5" < "Req A" (false because "A.5" > "A")
		// So it continues, finds Req B at index 2 with order 3, inserts before it at position 2
		if pos != 2 {
			t.Errorf("expected position 2, got %d", pos)
		}
	})

	t.Run("empty list", func(t *testing.T) {
		pos := SortInsertPosition([]Item{}, Item{Name: "Test", Request: &Request{}, Order: 1})
		if pos != 0 {
			t.Errorf("expected position 0, got %d", pos)
		}
	})

	t.Run("folder vs request ordering (folder has higher order)", func(t *testing.T) {
		items := []Item{
			{Name: "Req Z", Request: &Request{}, Order: 5},
		}
		newItem := Item{Name: "Folder Z", Item: []Item{}, Order: 10}
		pos := SortInsertPosition(items, newItem)
		if pos != 0 { // Folders before requests regardless of order
			t.Errorf("expected position 0 (folder before request), got %d", pos)
		}
	})
}

func TestSortItems(t *testing.T) {
	t.Run("folders first, then requests, sorted by order then alpha", func(t *testing.T) {
		items := []Item{
			{Name: "Z Request", Request: &Request{}, Order: 2},
			{Name: "A Folder", Item: []Item{}, Order: 1},
			{Name: "A Request", Request: &Request{}, Order: 1},
		}
		sortItems(items)

		if items[0].Name != "A Folder" {
			t.Errorf("Expected first item to be 'A Folder', got '%s'", items[0].Name)
		}
		if items[1].Name != "A Request" {
			t.Errorf("Expected second item to be 'A Request', got '%s'", items[1].Name)
		}
		if items[2].Name != "Z Request" {
			t.Errorf("Expected third item to be 'Z Request', got '%s'", items[2].Name)
		}
	})

	t.Run("recursive sorting", func(t *testing.T) {
		items := []Item{
			{
				Name: "Folder",
				Item: []Item{
					{Name: "B Req", Request: &Request{}, Order: 2},
					{Name: "A Req", Request: &Request{}, Order: 1},
				},
			},
		}
		sortItems(items)

		if len(items[0].Item) != 2 {
			t.Fatalf("Expected 2 items in folder, got %d", len(items[0].Item))
		}
		if items[0].Item[0].Name != "A Req" {
			t.Errorf("Expected sorted first item 'A Req', got '%s'", items[0].Item[0].Name)
		}
		if items[0].Item[1].Name != "B Req" {
			t.Errorf("Expected sorted second item 'B Req', got '%s'", items[0].Item[1].Name)
		}
	})

	t.Run("alphabetical fallback for same order", func(t *testing.T) {
		items := []Item{
			{Name: "Zebra", Request: &Request{}, Order: 1},
			{Name: "Alpha", Request: &Request{}, Order: 1},
			{Name: "beta", Request: &Request{}, Order: 1},
		}
		sortItems(items)
		if items[0].Name != "Alpha" {
			t.Errorf("Expected 'Alpha' first, got '%s'", items[0].Name)
		}
		if items[1].Name != "beta" {
			t.Errorf("Expected 'beta' second, got '%s'", items[1].Name)
		}
		if items[2].Name != "Zebra" {
			t.Errorf("Expected 'Zebra' third, got '%s'", items[2].Name)
		}
	})
}

func TestReconstructItems(t *testing.T) {
	t.Run("basic hierarchy", func(t *testing.T) {
		reqs := []RequestInfo{
			{Path: "Folder > Req1", Request: &Request{Method: "GET", URL: URL{Raw: "http://a.com"}}, Order: 0},
			{Path: "Folder > Req2", Request: &Request{Method: "POST", URL: URL{Raw: "http://b.com"}}, Order: 1},
		}

		items := ReconstructItems(reqs)
		if len(items) != 1 {
			t.Fatalf("Expected 1 root item (folder), got %d", len(items))
		}
		if items[0].Name != "Folder" {
			t.Errorf("Expected folder 'Folder', got '%s'", items[0].Name)
		}
		if items[0].Request != nil {
			t.Error("Folder should not have a Request")
		}
		if len(items[0].Item) != 2 {
			t.Fatalf("Expected 2 items in folder, got %d", len(items[0].Item))
		}
		if items[0].Item[0].Name != "Req1" {
			t.Errorf("Expected 'Req1', got '%s'", items[0].Item[0].Name)
		}
	})

	t.Run("nested folders", func(t *testing.T) {
		reqs := []RequestInfo{
			{Path: "Folder > Sub > DeepReq", Request: &Request{}, Order: 0},
		}

		items := ReconstructItems(reqs)
		if len(items) != 1 {
			t.Fatalf("Expected 1 root folder, got %d", len(items))
		}
		if len(items[0].Item) != 1 {
			t.Fatalf("Expected 1 sub-folder, got %d", len(items[0].Item))
		}
		if items[0].Item[0].Name != "Sub" {
			t.Errorf("Expected 'Sub', got '%s'", items[0].Item[0].Name)
		}
		if len(items[0].Item[0].Item) != 1 {
			t.Fatalf("Expected 1 request in sub-folder, got %d", len(items[0].Item[0].Item))
		}
		if items[0].Item[0].Item[0].Name != "DeepReq" {
			t.Errorf("Expected 'DeepReq', got '%s'", items[0].Item[0].Item[0].Name)
		}
	})

	t.Run("multiple root items", func(t *testing.T) {
		reqs := []RequestInfo{
			{Path: "A > Req1", Request: &Request{}, Order: 0},
			{Path: "B > Req2", Request: &Request{}, Order: 1},
		}

		items := ReconstructItems(reqs)
		if len(items) != 2 {
			t.Fatalf("Expected 2 root folders, got %d", len(items))
		}
		if items[0].Name != "A" {
			t.Errorf("Expected folder 'A', got '%s'", items[0].Name)
		}
		if items[1].Name != "B" {
			t.Errorf("Expected folder 'B', got '%s'", items[1].Name)
		}
	})

	t.Run("empty input", func(t *testing.T) {
		items := ReconstructItems([]RequestInfo{})
		if len(items) != 0 {
			t.Errorf("Expected empty result, got %d items", len(items))
		}
	})

	t.Run("sorting by order", func(t *testing.T) {
		reqs := []RequestInfo{
			{Path: "Folder > ReqZ", Request: &Request{}, Order: 5},
			{Path: "Folder > ReqA", Request: &Request{}, Order: 1},
		}

		items := ReconstructItems(reqs)
		if len(items) != 1 {
			t.Fatalf("Expected 1 folder, got %d", len(items))
		}
		if items[0].Item[0].Name != "ReqA" {
			t.Errorf("Expected 'ReqA' first (order 1), got '%s'", items[0].Item[0].Name)
		}
		if items[0].Item[1].Name != "ReqZ" {
			t.Errorf("Expected 'ReqZ' second (order 5), got '%s'", items[0].Item[1].Name)
		}
	})
}

func TestReconstructItemsWithDelta(t *testing.T) {
	t.Run("update existing request", func(t *testing.T) {
		existing := []Item{
			{
				Name: "Folder",
				Item: []Item{
					{Name: "Req1", Request: &Request{Method: "GET"}, Order: 0},
				},
			},
		}

		changed := RequestInfo{
			Path:    "Folder > Req1",
			Request: &Request{Method: "POST"},
			Order:   0,
		}

		result := ReconstructItemsWithDelta(existing, changed)
		if len(result) != 1 {
			t.Fatalf("Expected 1 folder, got %d", len(result))
		}
		if result[0].Item[0].Request.Method != "POST" {
			t.Errorf("Expected updated method 'POST', got '%s'", result[0].Item[0].Request.Method)
		}
	})

	t.Run("insert new request in sorted position", func(t *testing.T) {
		existing := []Item{
			{
				Name: "Folder",
				Item: []Item{
					{Name: "ReqA", Request: &Request{}, Order: 1},
					{Name: "ReqC", Request: &Request{}, Order: 3},
				},
			},
		}

		changed := RequestInfo{
			Path:    "Folder > ReqB",
			Request: &Request{Method: "PUT"},
			Order:   2,
		}

		result := ReconstructItemsWithDelta(existing, changed)
		if len(result[0].Item) != 3 {
			t.Fatalf("Expected 3 items, got %d", len(result[0].Item))
		}
		if result[0].Item[1].Name != "ReqB" {
			t.Errorf("Expected 'ReqB' at index 1, got '%s'", result[0].Item[1].Name)
		}
	})

	t.Run("create new folder", func(t *testing.T) {
		existing := []Item{}

		changed := RequestInfo{
			Path:    "NewFolder > NewReq",
			Request: &Request{Method: "GET"},
			Order:   0,
		}

		result := ReconstructItemsWithDelta(existing, changed)
		if len(result) != 1 {
			t.Fatalf("Expected 1 folder, got %d", len(result))
		}
		if result[0].Name != "NewFolder" {
			t.Errorf("Expected 'NewFolder', got '%s'", result[0].Name)
		}
		if len(result[0].Item) != 1 {
			t.Fatalf("Expected 1 item in folder, got %d", len(result[0].Item))
		}
		if result[0].Item[0].Name != "NewReq" {
			t.Errorf("Expected 'NewReq', got '%s'", result[0].Item[0].Name)
		}
	})

	t.Run("update within existing folder hierarchy", func(t *testing.T) {
		existing := []Item{
			{
				Name: "Folder",
				Item: []Item{
					{
						Name: "Sub",
						Item: []Item{
							{Name: "OldReq", Request: &Request{Method: "GET"}, Order: 0},
						},
					},
				},
			},
		}

		changed := RequestInfo{
			Path:    "Folder > Sub > OldReq",
			Request: &Request{Method: "DELETE"},
			Order:   0,
		}

		result := ReconstructItemsWithDelta(existing, changed)
		if result[0].Item[0].Item[0].Request.Method != "DELETE" {
			t.Errorf("Expected updated method 'DELETE', got '%s'", result[0].Item[0].Item[0].Request.Method)
		}
	})
}
