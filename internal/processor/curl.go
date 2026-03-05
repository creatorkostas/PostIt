package processor

import (
	"postit/internal/models"
	"strings"

	"github.com/kballard/go-shellquote"
)

func ParseCurl(curlCommand string) *models.Request {
	// Clean up potential multi-line commands
	curlCommand = strings.ReplaceAll(curlCommand, "\\\n", " ")
	curlCommand = strings.ReplaceAll(curlCommand, "\\\r\n", " ")

	parts, err := shellquote.Split(curlCommand)
	if err != nil || len(parts) == 0 {
		return nil
	}

	req := &models.Request{
		Method: "GET",
		URL:    models.URL{},
		Header: []models.Header{},
	}

	for i := 0; i < len(parts); i++ {
		p := parts[i]

		switch p {
		case "-X", "--request":
			if i+1 < len(parts) {
				req.Method = strings.ToUpper(parts[i+1])
				i++
			}
		case "-H", "--header":
			if i+1 < len(parts) {
				headerLine := parts[i+1]
				headerParts := strings.SplitN(headerLine, ":", 2)
				if len(headerParts) == 2 {
					req.Header = append(req.Header, models.Header{
						Key:   strings.TrimSpace(headerParts[0]),
						Value: strings.TrimSpace(headerParts[1]),
					})
				}
				i++
			}
		case "-d", "--data", "--data-raw", "--data-binary":
			if i+1 < len(parts) {
				if req.Body == nil {
					req.Body = &models.Body{Mode: "raw"}
				}
				if req.Body.Raw != "" {
					req.Body.Raw += "&"
				}
				req.Body.Raw += parts[i+1]
				if req.Method == "GET" {
					req.Method = "POST"
				}
				i++
			}
		case "-u", "--user":
			if i+1 < len(parts) {
				// Basic Auth
				req.Auth = &models.Auth{
					Type: "basic",
					Basic: []models.BasicAuth{
						{Key: "username", Value: strings.Split(parts[i+1], ":")[0]},
						{Key: "password", Value: strings.SplitN(parts[i+1], ":", 2)[1]},
					},
				}
				i++
			}
		default:
			if strings.HasPrefix(p, "http://") || strings.HasPrefix(p, "https://") {
				req.URL.Raw = p
			} else if p != "curl" && !strings.HasPrefix(p, "-") && req.URL.Raw == "" {
				// Potential URL without protocol
				req.URL.Raw = p
			}
		}
	}

	return req
}
