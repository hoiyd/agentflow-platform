package tool

import (
	"bytes"
	"strings"

	"golang.org/x/net/html"
)

const webFetchMaxTextBytes = 8192

func extractWebText(body []byte, mediaType string) (string, bool, error) {
	text := string(body)
	complete := true
	if mediaType == "text/html" {
		root, err := html.Parse(bytes.NewReader(body))
		if err != nil {
			return "", false, executionError(ErrorResultEncoding, "Web fetch HTML could not be parsed", nil)
		}
		// Prefer the article/main landmark; fall back to the body, not head data.
		for _, tag := range []string{"main", "article", "body"} {
			if node := webTextElement(root, tag); node != nil {
				root = node
				break
			}
		}
		var buffer strings.Builder
		complete = appendWebText(root, &buffer)
		text = buffer.String()
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", false, executionError(ErrorNoResults, "Web fetch found no readable body text", nil)
	}
	complete = complete && len(text) <= webFetchMaxTextBytes
	return truncateUTF8([]byte(text), min(len(text), webFetchMaxTextBytes)), complete, nil
}

func webTextElement(node *html.Node, tag string) *html.Node {
	if node.Type == html.ElementNode && node.Data == tag {
		return node
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if found := webTextElement(child, tag); found != nil {
			return found
		}
	}
	return nil
}

func appendWebText(node *html.Node, buffer *strings.Builder) bool {
	if node.Type == html.ElementNode {
		switch node.Data {
		case "head", "script", "style", "nav", "footer", "aside", "iframe", "noscript", "form", "svg", "canvas", "template":
			return true
		}
	}
	if node.Type == html.TextNode {
		text := strings.Join(strings.Fields(node.Data), " ")
		remaining := max(0, webFetchMaxTextBytes-buffer.Len())
		if text != "" {
			buffer.WriteString(truncateUTF8([]byte(text), min(len(text), remaining)))
			if len(text) > remaining {
				return false
			}
			buffer.WriteByte(' ')
		}
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if !appendWebText(child, buffer) {
			return false
		}
	}
	return true
}
