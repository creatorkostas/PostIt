package models

import (
	"sort"
	"strings"
	"time"
)

type Collection struct {
	Info Info   `json:"info"`
	Item []Item `json:"item"`
}

type Info struct {
	PostmanID string `json:"_postman_id,omitempty"`
	Name      string `json:"name"`
	Schema    string `json:"schema"`
	ExporterID string `json:"_exporter_id,omitempty"`
}

type Item struct {
	Name     string         `json:"name"`
	Request  *Request       `json:"request,omitempty"`
	Response []MockResponse `json:"response,omitempty"`
	Item     []Item         `json:"item,omitempty"`
	Event    []Event        `json:"event,omitempty"`
	Order    int            `json:"order"`
}

type Request struct {
	Method string   `json:"method"`
	Header []Header `json:"header"`
	Body   *Body    `json:"body,omitempty"`
	URL    URL      `json:"url"`
	Auth   *Auth    `json:"auth,omitempty"`
}

func (r *Request) DeepCopy() *Request {
	copy := *r
	copy.Header = append([]Header{}, r.Header...)
	if r.Body != nil {
		copy.Body = &Body{
			Mode: r.Body.Mode,
			Raw:  r.Body.Raw,
		}
		if r.Body.UrlEncoded != nil {
			copy.Body.UrlEncoded = append([]UrlEncoded{}, r.Body.UrlEncoded...)
		}
	}
	return &copy
}

type Auth struct {
	Type   string      `json:"type"`
	Bearer []Header    `json:"bearer,omitempty"`
	Basic  []BasicAuth `json:"basic,omitempty"`
}

type BasicAuth struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type Body struct {
	Mode       string       `json:"mode"`
	Raw        string       `json:"raw,omitempty"`
	UrlEncoded []UrlEncoded `json:"urlencoded,omitempty"`
	Options    *Options     `json:"options,omitempty"`
}

type Options struct {
	Raw *RawOptions `json:"raw,omitempty"`
}

type RawOptions struct {
	Language string `json:"language"`
}

type UrlEncoded struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type URL struct {
	Raw string `json:"raw"`
}

type Header struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type Event struct {
	Listen string `json:"listen"`
	Script Script `json:"script"`
}

type Script struct {
	Exec []string `json:"exec"`
	Type string   `json:"type"`
}

type MockResponse struct {
	Name      string   `json:"name"`
	Code      int      `json:"code"`
	Status    string   `json:"status"`
	Body      string   `json:"body"`
	Header    []Header `json:"header"`
	Condition string   `json:"condition,omitempty"`
	Delay     int      `json:"delay,omitempty"` // Delay in ms
}

type MockStat struct {
	Hits       int       `json:"hits"`
	LastAccess time.Time `json:"lastAccess"`
}

type RequestInfo struct {
	Path string `json:"path"`
	Request *Request `json:"request"`
	Responses []MockResponse `json:"responses,omitempty"`
	Events []Event `json:"events,omitempty"`
	Order int `json:"order"`
	SQLQuery string `json:"sql_query,omitempty"`
	DBPath string `json:"db_path,omitempty"`
	SQLDriver string `json:"sql_driver,omitempty"`
	SQLTargetVar string `json:"sql_target_var,omitempty"`
	SQLTargetCol string `json:"sql_target_col,omitempty"`
	Schema string `json:"schema,omitempty"`
	Note string `json:"note,omitempty"`
}

func ReconstructItems(reqs []RequestInfo) []Item {
	// Sort requests by absolute order first to help determine placement
	sort.Slice(reqs, func(i, j int) bool {
		return reqs[i].Order < reqs[j].Order
	})

	root := []Item{}
	for _, req := range reqs {
		parts := strings.Split(req.Path, " > ")
		currentItems := &root
		
		for i, part := range parts {
			isLast := i == len(parts)-1
			
			// Try to find existing folder
			var foundIdx = -1
			for j, itm := range *currentItems {
				if itm.Name == part && itm.Request == nil && !isLast {
					foundIdx = j
					break
				}
			}

			if isLast {
				// It's the request itself
				*currentItems = append(*currentItems, Item{
					Name:     part,
					Request:  req.Request,
					Response: req.Responses,
					Event:    req.Events,
					Order:    req.Order,
				})
			} else if foundIdx != -1 {
				// Folder already exists, go deeper
				currentItems = &(*currentItems)[foundIdx].Item
			} else {
				// Create new folder
				newItem := Item{
					Name:  part,
					Item:  []Item{},
					Order: req.Order, // Folder takes order of its first item
				}
				*currentItems = append(*currentItems, newItem)
				currentItems = &(*currentItems)[len(*currentItems)-1].Item
			}
		}
	}
	
	sortItems(root)
	return root
}

// SortInsertPosition finds the correct insertion position for a new item
// Returns the index where the item should be inserted to maintain sorted order
func SortInsertPosition(items []Item, newItem Item) int {
	for i, item := range items {
		// Compare folders vs requests
		newIsFolder := newItem.Request == nil && newItem.Item != nil
		existingIsFolder := item.Request == nil && item.Item != nil

		if newIsFolder && !existingIsFolder {
			return i // Folders come before requests
		}
		if !newIsFolder && existingIsFolder {
			continue // Keep looking for a request
		}

		// Same type - compare by Order
		if newItem.Order < item.Order {
			return i
		}

		// Same Order - compare alphabetically
		if newItem.Order == item.Order {
			if strings.ToLower(newItem.Name) < strings.ToLower(item.Name) {
				return i
			}
		}
	}
	return len(items)
}

func sortItems(items []Item) {
	sort.Slice(items, func(i, j int) bool {
		// Rule 1: Folders first
		iIsFolder := items[i].Request == nil && items[i].Item != nil
		jIsFolder := items[j].Request == nil && items[j].Item != nil

		if iIsFolder && !jIsFolder {
			return true
		}
		if !iIsFolder && jIsFolder {
			return false
		}

		// Rule 2: If both are the same type, use Order
		if items[i].Order != items[j].Order {
			return items[i].Order < items[j].Order
		}

		// Rule 3: Alphabetical fallback
		return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
	})

	for i := range items {
		if items[i].Item != nil {
			sortItems(items[i].Item)
		}
	}
}

// ReconstructItemsWithDelta rebuilds only the affected subtree when a single request changes
// This is more efficient than ReconstructItems when only one request is modified
func ReconstructItemsWithDelta(existing []Item, changedReq RequestInfo) []Item {
	parts := strings.Split(changedReq.Path, " > ")

	// Find or create the folder path
	currentItems := &existing
	for i, part := range parts {
		isLast := i == len(parts)-1

		if isLast {
			// Update or insert the request
			foundIdx := -1
			for j, item := range *currentItems {
				if item.Name == part && item.Request != nil {
					foundIdx = j
					break
				}
			}
			if foundIdx >= 0 {
				// Update existing
				(*currentItems)[foundIdx] = Item{
					Name:     part,
					Request:  changedReq.Request,
					Response: changedReq.Responses,
					Event:    changedReq.Events,
					Order:    changedReq.Order,
				}
			} else {
				// Insert new in sorted position
				newItem := Item{
					Name:     part,
					Request:  changedReq.Request,
					Response: changedReq.Responses,
					Event:    changedReq.Events,
					Order:    changedReq.Order,
				}
				pos := SortInsertPosition(*currentItems, newItem)
				*currentItems = append(*currentItems, Item{})
				copy((*currentItems)[pos+1:], (*currentItems)[pos:])
				(*currentItems)[pos] = newItem
			}
		} else {
			// Find or create folder
			foundIdx := -1
			for j, item := range *currentItems {
				if item.Name == part && item.Request == nil {
					foundIdx = j
					break
				}
			}
			if foundIdx >= 0 {
				currentItems = &(*currentItems)[foundIdx].Item
			} else {
				// Create new folder at sorted position
				newFolder := Item{
					Name:  part,
					Item:  []Item{},
					Order: changedReq.Order,
				}
				pos := SortInsertPosition(*currentItems, newFolder)
				*currentItems = append(*currentItems, Item{})
				copy((*currentItems)[pos+1:], (*currentItems)[pos:])
				(*currentItems)[pos] = newFolder
				currentItems = &(*currentItems)[pos].Item
			}
		}
	}

	return existing
}
