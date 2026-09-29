package fallback

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

const (
	maxResponseBytes = 512 * 1024
	maxDownloadBytes = 4 * 1024 * 1024
	maxBlobBytes     = 32 * 1024
	maxRequests      = 48
	maxEntries       = 2048
)

var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
var repoPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,99}/[A-Za-z0-9][A-Za-z0-9_.-]{0,99}$`)

type github struct {
	client                   http.Client
	base                     string
	requests, bytes, entries int
}

type treeEntry struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
	Type string `json:"type"`
	SHA  string `json:"sha"`
	Size int    `json:"size"`
}

type tree struct {
	SHA       string      `json:"sha"`
	Entries   []treeEntry `json:"tree"`
	Truncated bool        `json:"truncated"`
}

func newGitHub(repo string, client *http.Client) *github {
	g := &github{client: *client, base: "https://api.github.com/repos/" + repo}
	g.client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("GitHub redirect limit exceeded")
		}
		if err := checkURL(req.URL); err != nil {
			return err
		}
		if g.requests >= maxRequests {
			return fmt.Errorf("GitHub request limit exceeded")
		}
		g.requests++
		return nil
	}
	return g
}

func checkURL(u *url.URL) error {
	if u.Scheme != "https" || u.Host != "api.github.com" || u.User != nil || u.Fragment != "" {
		return fmt.Errorf("download denied: only credential-free https://api.github.com URLs are allowed")
	}
	return nil
}

func (g *github) get(ctx context.Context, endpoint string, out any) error {
	if g.requests >= maxRequests {
		return fmt.Errorf("GitHub request limit exceeded")
	}
	g.requests++
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.base+endpoint, nil)
	if err != nil {
		return err
	}
	if err := checkURL(req.URL); err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "agentflow-skill-installer")
	req.Header.Set("Accept-Encoding", "identity")
	response, err := g.client.Do(req)
	if err != nil {
		return fmt.Errorf("GitHub request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub request failed: HTTP %d (check public access and rate limits)", response.StatusCode)
	}
	if encoding := response.Header.Get("Content-Encoding"); encoding != "" && encoding != "identity" {
		return fmt.Errorf("compressed GitHub responses are not accepted")
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, int64(min(maxResponseBytes, maxDownloadBytes-g.bytes)+1)))
	if err != nil {
		return fmt.Errorf("GitHub response failed: %w", err)
	}
	g.bytes += len(content)
	if len(content) > maxResponseBytes || g.bytes > maxDownloadBytes {
		return fmt.Errorf("GitHub download byte limit exceeded")
	}
	if err := json.Unmarshal(content, out); err != nil {
		return fmt.Errorf("invalid GitHub JSON response")
	}
	return nil
}

func (g *github) commit(ctx context.Context, ref string) (string, error) {
	if commitPattern.MatchString(ref) {
		return ref, nil
	}
	var value struct {
		SHA string `json:"sha"`
	}
	if ref == "" {
		// GitHub defaults this list to the repository's default branch, not necessarily main.
		var commits []struct {
			SHA string `json:"sha"`
		}
		if err := g.get(ctx, "/commits?per_page=1", &commits); err != nil {
			return "", err
		}
		if len(commits) != 1 {
			return "", fmt.Errorf("default branch did not return exactly one latest commit")
		}
		value.SHA = commits[0].SHA
	} else {
		if err := g.get(ctx, "/commits/"+url.PathEscape(ref), &value); err != nil {
			return "", err
		}
	}
	if !commitPattern.MatchString(value.SHA) {
		return "", fmt.Errorf("invalid GitHub commit identity")
	}
	return value.SHA, nil
}

func (g *github) tree(ctx context.Context, id string) (tree, error) {
	var result tree
	if err := g.get(ctx, "/git/trees/"+id, &result); err != nil {
		return result, err
	}
	g.entries += len(result.Entries)
	if result.Truncated || !commitPattern.MatchString(result.SHA) || g.entries > maxEntries {
		return result, fmt.Errorf("invalid or oversized GitHub tree")
	}
	seen := map[string]bool{}
	for _, item := range result.Entries {
		if item.Path == "" || item.Path == "." || item.Path == ".." || len(item.Path) > 256 || strings.ContainsAny(item.Path, "/\\\x00\r\n") || seen[item.Path] || !commitPattern.MatchString(item.SHA) || item.Size < 0 {
			return result, fmt.Errorf("invalid GitHub tree entry")
		}
		seen[item.Path] = true
	}
	return result, nil
}

func (g *github) child(ctx context.Context, item treeEntry) (tree, error) {
	if item.Mode != "040000" || item.Type != "tree" {
		return tree{}, fmt.Errorf("package path is not a directory")
	}
	result, err := g.tree(ctx, item.SHA)
	if err == nil && result.SHA != item.SHA {
		err = fmt.Errorf("GitHub tree identity mismatch")
	}
	return result, err
}

func (g *github) blob(ctx context.Context, item treeEntry) ([]byte, error) {
	if item.Mode != "100644" || item.Type != "blob" || item.Size > maxBlobBytes {
		return nil, fmt.Errorf("selected file is not bounded non-executable text")
	}
	var value struct {
		SHA      string `json:"sha"`
		Size     int    `json:"size"`
		Encoding string `json:"encoding"`
		Content  string `json:"content"`
	}
	if err := g.get(ctx, "/git/blobs/"+item.SHA, &value); err != nil {
		return nil, err
	}
	if value.SHA != item.SHA || value.Size != item.Size || value.Encoding != "base64" {
		return nil, fmt.Errorf("invalid GitHub blob metadata")
	}
	content, err := base64.StdEncoding.DecodeString(value.Content)
	if err != nil || len(content) != value.Size || len(content) > maxBlobBytes {
		return nil, fmt.Errorf("invalid or oversized GitHub blob")
	}
	// SHA-1 is Git's object identity here, not a claim of cryptographic package authenticity.
	id := sha1.New()
	fmt.Fprintf(id, "blob %d\x00", len(content))
	_, _ = id.Write(content)
	if fmt.Sprintf("%x", id.Sum(nil)) != item.SHA {
		return nil, fmt.Errorf("GitHub blob content identity mismatch")
	}
	return content, nil
}
