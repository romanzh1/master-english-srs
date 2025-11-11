package onenote

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const (
	graphAPIBase = "https://graph.microsoft.com/v1.0"
)

type Client struct {
	httpClient *http.Client
}

func NewClient() *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) GetNotebooks(accessToken string) ([]Notebook, error) {
	url := fmt.Sprintf("%s/me/onenote/notebooks", graphAPIBase)

	var response NotebooksResponse
	if err := c.makeRequest(accessToken, url, &response); err != nil {
		return nil, fmt.Errorf("get notebooks: %w", err)
	}

	return response.Value, nil
}

func (c *Client) GetSections(accessToken, notebookID string) ([]Section, error) {
	url := fmt.Sprintf("%s/me/onenote/notebooks/%s/sections", graphAPIBase, notebookID)

	var response SectionsResponse
	if err := c.makeRequest(accessToken, url, &response); err != nil {
		return nil, fmt.Errorf("get sections (notebook_id: %s): %w", notebookID, err)
	}

	return response.Value, nil
}

func (c *Client) GetPages(accessToken, sectionID string) ([]Page, error) {
	url := fmt.Sprintf("%s/me/onenote/sections/%s/pages", graphAPIBase, sectionID)

	var response PagesResponse
	if err := c.makeRequest(accessToken, url, &response); err != nil {
		return nil, fmt.Errorf("get pages (section_id: %s): %w", sectionID, err)
	}

	return response.Value, nil
}

func (c *Client) GetPageContent(accessToken, pageID string) (string, error) {
	url := fmt.Sprintf("%s/me/onenote/pages/%s/content", graphAPIBase, pageID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("create request (page_id: %s): %w", pageID, err)
	}

	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("execute request (page_id: %s): %w", pageID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("get page content (page_id: %s, status: %d): %s", pageID, resp.StatusCode, string(body))
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response body (page_id: %s): %w", pageID, err)
	}

	content := c.extractTextFromHTML(string(bodyBytes))
	return content, nil
}

func (c *Client) makeRequest(accessToken, url string, result interface{}) error {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return fmt.Errorf("create request (url: %s): %w", url, err)
	}

	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute request (url: %s): %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("request failed (url: %s, status: %d): %s", url, resp.StatusCode, string(body))
	}

	if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
		return fmt.Errorf("decode response (url: %s): %w", url, err)
	}

	return nil
}

func (c *Client) extractTextFromHTML(html string) string {
	re := regexp.MustCompile(`<[^>]*>`)
	text := re.ReplaceAllString(html, "")

	text = strings.ReplaceAll(text, "&nbsp;", " ")
	text = strings.ReplaceAll(text, "&amp;", "&")
	text = strings.ReplaceAll(text, "&lt;", "<")
	text = strings.ReplaceAll(text, "&gt;", ">")
	text = strings.ReplaceAll(text, "&quot;", "\"")

	lines := strings.Split(text, "\n")
	var cleanLines []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			cleanLines = append(cleanLines, trimmed)
		}
	}

	return strings.Join(cleanLines, "\n")
}
