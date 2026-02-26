package models

type Collection struct {
	Info Info   `json:"info"`
	Item []Item `json:"item"`
}

type Info struct {
	Name   string `json:"name"`
	Schema string `json:"schema"`
}

type Item struct {
	Name    string   `json:"name"`
	Event   []Event  `json:"event,omitempty"`
	Item    []Item   `json:"item,omitempty"`
	Request *Request `json:"request,omitempty"`
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
	Path    string   `json:"path"`
	Request *Request `json:"request"`
	Events  []Event  `json:"events,omitempty"`
}
