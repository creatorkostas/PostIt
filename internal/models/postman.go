package models

import (
	"strings"
	"time"
)

type Collection struct {
	Info Info   `json:"info"`
	Item []Item `json:"item"`
}

type Info struct {
	Name   string `json:"name"`
	Schema string `json:"schema"`
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
	Type   string   `json:"type"`
	Bearer []Header `json:"bearer,omitempty"`
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
}

type MockStat struct {
	Hits       int       `json:"hits"`
	LastAccess time.Time `json:"lastAccess"`
}

type RequestInfo struct {
	Path      string         `json:"path"`
	Request   *Request       `json:"request"`
	Responses []MockResponse `json:"responses,omitempty"`
	Events    []Event        `json:"events,omitempty"`
	Order     int            `json:"order"`
	SQLQuery  string         `json:"sql_query,omitempty"`
	DBPath    string         `json:"db_path,omitempty"`
}

func ReconstructItems(reqs []RequestInfo) []Item {
	root := []Item{}
	for _, req := range reqs {
		parts := strings.Split(req.Path, " > ")
		current := &root
		for i, part := range parts {
			found := false
			if i == len(parts)-1 {
				*current = append(*current, Item{
					Name:     part,
					Request:  req.Request,
					Response: req.Responses,
					Event:    req.Events,
				})
				break
			}
			for j := range *current {
				if (*current)[j].Name == part && (*current)[j].Item != nil {
					current = &(*current)[j].Item
					found = true
					break
				}
			}
			if !found {
				newItem := Item{Name: part, Item: []Item{}}
				*current = append(*current, newItem)
				current = &(*current)[len(*current)-1].Item
			}
		}
	}
	sortItems(root)
	return root
}

func sortItems(items []Item) {
	for i := 0; i < len(items); i++ {
		for j := i + 1; j < len(items); j++ {
			// This is a simple sort, usually Postman has its own order
		}
	}
	for i := range items {
		if len(items[i].Item) > 0 {
			sortItems(items[i].Item)
		}
	}
}
