package github

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

const (
	githubDeviceCodeURL  = "https://github.com/login/device/code"
	githubAccessTokenURL = "https://github.com/login/oauth/access_token"
	defaultClientID      = "Ov23liDCQc2d5t86pJvK"
)

// DeviceCodeResponse represents the response from the device code endpoint.
type DeviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

// AccessTokenResponse represents the response from the access token endpoint.
type AccessTokenResponse struct {
	AccessToken      string `json:"access_token"`
	TokenType        string `json:"token_type"`
	Scope            string `json:"scope"`
	Error            string `json:"error,omitempty"`
	ErrorDescription string `json:"error_description,omitempty"`
}

// GitHubAuth manages GitHub OAuth device flow authentication.
type GitHubAuth struct {
	TokenPath string
	ClientID  string
}

func getClientID() string {
	if id := os.Getenv("GITHUB_CLIENT_ID"); id != "" {
		return id
	}
	return defaultClientID
}

// NewGitHubAuth creates a new GitHubAuth instance.
func NewGitHubAuth(tokenPath string) *GitHubAuth {
	if tokenPath == "" {
		configDir, _ := os.UserConfigDir()
		tokenPath = filepath.Join(configDir, "pr-filter", "github-token.json")
	}
	return &GitHubAuth{
		TokenPath: tokenPath,
		ClientID:  getClientID(),
	}
}

// GetClient returns an authenticated HTTP client for GitHub API.
func (g *GitHubAuth) GetClient(ctx context.Context) (*http.Client, error) {
	if token, err := g.loadToken(); err == nil && token.Valid() {
		return oauth2.NewClient(ctx, oauth2.StaticTokenSource(token)), nil
	}

	token, err := g.performDeviceFlow(ctx)
	if err != nil {
		return nil, fmt.Errorf("device flow auth failed: %w", err)
	}

	if err := g.saveToken(token); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to save token: %v\n", err)
	}

	return oauth2.NewClient(ctx, oauth2.StaticTokenSource(token)), nil
}

func (g *GitHubAuth) performDeviceFlow(ctx context.Context) (*oauth2.Token, error) {
	deviceCode, err := g.requestDeviceCode(ctx)
	if err != nil {
		return nil, fmt.Errorf("request device code: %w", err)
	}

	fmt.Println()
	fmt.Println("╔════════════════════════════════════════════════════════════════╗")
	fmt.Println("║             GitHub Authentication Required                     ║")
	fmt.Println("╠════════════════════════════════════════════════════════════════╣")
	fmt.Printf("║  1. Visit: %-52s ║\n", deviceCode.VerificationURI)
	fmt.Printf("║  2. Enter code: %-45s ║\n", deviceCode.UserCode)
	fmt.Println("╚════════════════════════════════════════════════════════════════╝")
	fmt.Println()
	fmt.Println("Waiting for authentication... (press Ctrl+C to cancel)")
	fmt.Println()

	token, err := g.pollForToken(ctx, deviceCode)
	if err != nil {
		return nil, fmt.Errorf("poll for token: %w", err)
	}

	fmt.Println("Authentication successful!")
	fmt.Println()

	return token, nil
}

func (g *GitHubAuth) requestDeviceCode(ctx context.Context) (*DeviceCodeResponse, error) {
	if g.ClientID == "" {
		return nil, fmt.Errorf("GITHUB_CLIENT_ID not set - device flow requires a GitHub OAuth App\n" +
			"Create one at https://github.com/settings/developers and set GITHUB_CLIENT_ID")
	}
	data := url.Values{}
	data.Set("client_id", g.ClientID)
	data.Set("scope", "repo read:user")

	req, err := http.NewRequestWithContext(ctx, "POST", githubDeviceCodeURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("device code request failed: %s", resp.Status)
	}

	var result DeviceCodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &result, nil
}

func (g *GitHubAuth) pollForToken(ctx context.Context, deviceCode *DeviceCodeResponse) (*oauth2.Token, error) {
	interval := time.Duration(deviceCode.Interval) * time.Second
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}

	expiresAt := time.Now().Add(time.Duration(deviceCode.ExpiresIn) * time.Second)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			if time.Now().After(expiresAt) {
				return nil, fmt.Errorf("device code expired")
			}

			token, pending, slowDown, err := g.requestAccessToken(ctx, deviceCode.DeviceCode)
			if err != nil {
				return nil, err
			}
			if slowDown {
				interval += 5 * time.Second
				ticker.Reset(interval)
				continue
			}
			if pending {
				continue
			}

			if token != nil {
				return token, nil
			}
		}
	}
}

func (g *GitHubAuth) requestAccessToken(ctx context.Context, deviceCode string) (*oauth2.Token, bool, bool, error) {
	data := url.Values{}
	data.Set("client_id", g.ClientID)
	data.Set("device_code", deviceCode)
	data.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")

	req, err := http.NewRequestWithContext(ctx, "POST", githubAccessTokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, false, false, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, false, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, false, false, fmt.Errorf("token request failed: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var result AccessTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, false, false, fmt.Errorf("decode response: %w", err)
	}

	if result.Error != "" {
		switch result.Error {
		case "authorization_pending":
			return nil, true, false, nil
		case "slow_down":
			return nil, false, true, nil
		case "expired_token":
			return nil, false, false, fmt.Errorf("device code expired")
		case "access_denied":
			return nil, false, false, fmt.Errorf("access denied by user")
		default:
			return nil, false, false, fmt.Errorf("%s: %s", result.Error, result.ErrorDescription)
		}
	}

	if result.AccessToken == "" {
		return nil, false, false, fmt.Errorf("no access token received")
	}

	return &oauth2.Token{
		AccessToken: result.AccessToken,
		TokenType:   result.TokenType,
	}, false, false, nil
}

func (g *GitHubAuth) loadToken() (*oauth2.Token, error) {
	data, err := os.ReadFile(g.TokenPath)
	if err != nil {
		return nil, err
	}

	var token oauth2.Token
	if err := json.Unmarshal(data, &token); err != nil {
		return nil, err
	}

	return &token, nil
}

func (g *GitHubAuth) saveToken(token *oauth2.Token) error {
	dir := filepath.Dir(g.TokenPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create token directory: %w", err)
	}

	data, err := json.MarshalIndent(token, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal token: %w", err)
	}

	if err := os.WriteFile(g.TokenPath, data, 0600); err != nil {
		return fmt.Errorf("write token file: %w", err)
	}

	return nil
}

// ClearToken removes the saved token, forcing re-authentication.
func (g *GitHubAuth) ClearToken() error {
	err := os.Remove(g.TokenPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// PromptAndClearToken prompts the user to clear the saved token.
func (g *GitHubAuth) PromptAndClearToken() error {
	fmt.Print("Clear saved GitHub token? [y/N]: ")
	reader := bufio.NewReader(os.Stdin)
	response, _ := reader.ReadString('\n')
	response = strings.ToLower(strings.TrimSpace(response))

	if response == "y" || response == "yes" {
		if err := g.ClearToken(); err != nil {
			return fmt.Errorf("failed to clear token: %w", err)
		}
		fmt.Println("Token cleared. You'll be prompted to authenticate on next run.")
	} else {
		fmt.Println("Token kept.")
	}
	return nil
}
