package models

import "strings"

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
	Event    []Event        `json:"event,omitempty"`
	Item     []Item         `json:"item,omitempty"`
	Request  *Request       `json:"request,omitempty"`
	Response []MockResponse `json:"response,omitempty"`
	Order    int            `json:"order"`
}

type MockResponse struct {
	Name   string   `json:"name"`
	Status string   `json:"status"`
	Code   int      `json:"code"`
	Header []Header `json:"header"`
	Body   string   `json:"body"`
}

type Event struct {
	Listen string `json:"listen"`
	Script Script `json:"script"`
}

type Script struct {
	Exec []string `json:"exec"`
	Type string   `json:"type"`
}

type Request struct {
	Method string   `json:"method"`
	Header []Header `json:"header"`
	Body   *Body    `json:"body,omitempty"`
	URL    URL      `json:"url"`
	Auth   *Auth    `json:"auth,omitempty"`
}

func (r *Request) DeepCopy() *Request {
	if r == nil {
		return nil
	}
	reqCopy := *r
	
	// Copy Headers
	if r.Header != nil {
		reqCopy.Header = make([]Header, len(r.Header))
		for i, h := range r.Header {
			reqCopy.Header[i] = h
		}
	}

	// Copy Body
	if r.Body != nil {
		bodyCopy := *r.Body
		if r.Body.Urlencoded != nil {
			bodyCopy.Urlencoded = make([]Urlencoded, len(r.Body.Urlencoded))
			for i, u := range r.Body.Urlencoded {
				bodyCopy.Urlencoded[i] = u
			}
		}
		reqCopy.Body = &bodyCopy
	}

	// Copy URL
	urlCopy := r.URL
	if r.URL.Host != nil {
		urlCopy.Host = make([]string, len(r.URL.Host))
		copy(urlCopy.Host, r.URL.Host)
	}
	if r.URL.Path != nil {
		urlCopy.Path = make([]string, len(r.URL.Path))
		copy(urlCopy.Path, r.URL.Path)
	}
	if r.URL.Query != nil {
		urlCopy.Query = make([]Query, len(r.URL.Query))
		for i, q := range r.URL.Query {
			urlCopy.Query[i] = q
		}
	}
	reqCopy.URL = urlCopy

	// Copy Auth
	if r.Auth != nil {
		authCopy := *r.Auth
		if r.Auth.Bearer != nil {
			authCopy.Bearer = make([]Bearer, len(r.Auth.Bearer))
			for i, b := range r.Auth.Bearer {
				authCopy.Bearer[i] = b
			}
		}
		reqCopy.Auth = &authCopy
	}

	return &reqCopy
}


type Header struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type Body struct {
	Mode       string       `json:"mode"`
	Raw        string       `json:"raw,omitempty"`
	Urlencoded []Urlencoded `json:"urlencoded,omitempty"`
	Options    *BodyOptions `json:"options,omitempty"`
}

type BodyOptions struct {
	Raw *RawOptions `json:"raw,omitempty"`
}

type RawOptions struct {
	Language string `json:"language,omitempty"`
}

type Urlencoded struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Type  string `json:"type,omitempty"`
}

type URL struct {
	Raw   string   `json:"raw"`
	Host  []string `json:"host"`
	Path  []string `json:"path"`
	Query []Query  `json:"query"`
}

type Query struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type Auth struct {
	Type   string   `json:"type"`
	Bearer []Bearer `json:"bearer,omitempty"`
}

type Bearer struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Type  string `json:"type"`
}

type RequestInfo struct {
	Path      string         `json:"path"`
	Request   *Request       `json:"request"`
	Responses []MockResponse `json:"responses,omitempty"`
	Events    []Event        `json:"events,omitempty"`
	Order     int            `json:"order"`
}

func ReconstructItems(requests []RequestInfo) []Item {
	root := &Item{}

	for _, req := range requests {
		parts := strings.Split(req.Path, " > ")
		current := root

		for i, part := range parts {
			isLast := i == len(parts)-1
			found := false

			for j := range current.Item {
				if current.Item[j].Name == part {
					current = &current.Item[j]
					found = true
					break
				}
			}

			if !found {
				newItem := Item{Name: part, Order: req.Order}
				current.Item = append(current.Item, newItem)
				current = &current.Item[len(current.Item)-1]
			}

			if isLast {
				current.Request = req.Request
				current.Event = req.Events
				current.Response = req.Responses
				current.Order = req.Order
			}
		}
	}

	sortItems(root.Item)
	return root.Item
}

func sortItems(items []Item) {
	for i := 0; i < len(items); i++ {
		for j := i + 1; j < len(items); j++ {
			if items[i].Order > items[j].Order {
				items[i], items[j] = items[j], items[i]
			}
		}
	}
	for i := range items {
		if len(items[i].Item) > 0 {
			sortItems(items[i].Item)
		}
	}
}
