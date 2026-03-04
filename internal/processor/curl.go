package processor

import (
	"postit/internal/models"
	"regexp"
	"strings"
)

func ParseCurl(curl string) *models.Request {
	req := &models.Request{
		Method: "GET",
		URL:    models.URL{},
		Header: []models.Header{},
	}

	// Method
	methodRegex := regexp.MustCompile(`-X\s+([A-Z]+)`)
	if match := methodRegex.FindStringSubmatch(curl); len(match) > 1 {
		req.Method = match[1]
	} else if strings.Contains(curl, "--data") || strings.Contains(curl, "-d") {
		req.Method = "POST"
	}

	// URL
	urlRegex := regexp.MustCompile(`'(https?://[^']+)'|" (https?://[^"]+)"|(https?://[^\s]+)`)
	if match := urlRegex.FindStringSubmatch(curl); len(match) > 0 {
		for _, m := range match[1:] {
			if m != "" {
				req.URL.Raw = m
				break
			}
		}
	}

	// Headers
	headerRegex := regexp.MustCompile(`-H\s+['"]([^'"]+)['"]`)
	matches := headerRegex.FindAllStringSubmatch(curl, -1)
	for _, m := range matches {
		parts := strings.SplitN(m[1], ":", 2)
		if len(parts) == 2 {
			req.Header = append(req.Header, models.Header{
				Key:   strings.TrimSpace(parts[0]),
				Value: strings.TrimSpace(parts[1]),
			})
		}
	}

	// Body
	bodyRegex := regexp.MustCompile(`-d\s+['"]([^'"]+)['"]|--data\s+['"]([^'"]+)['"]`)
	if match := bodyRegex.FindStringSubmatch(curl); len(match) > 0 {
		var body string
		for _, m := range match[1:] {
			if m != "" {
				body = m
				break
			}
		}
		req.Body = &models.Body{
			Mode: "raw",
			Raw:  body,
		}
	}

	return req
}
