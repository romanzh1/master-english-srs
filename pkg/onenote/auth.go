package onenote

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/microsoft"
)

type AuthService struct {
	clientID     string
	clientSecret string
	redirectURI  string
	scopes       []string
}

func NewAuthService(clientID, clientSecret, redirectURI string, scopes []string) *AuthService {
	return &AuthService{
		clientID:     clientID,
		clientSecret: clientSecret,
		redirectURI:  redirectURI,
		scopes:       scopes,
	}
}

func (a *AuthService) GetAuthURL(state string) string {
	config := a.getOAuthConfig()
	return config.AuthCodeURL(state, oauth2.AccessTypeOffline)
}

func (a *AuthService) ExchangeCode(code string) (*TokenResponse, error) {
	config := a.getOAuthConfig()
	ctx := context.Background()

	token, err := config.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("exchange code: %w", err)
	}

	return &TokenResponse{
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		ExpiresIn:    int(time.Until(token.Expiry).Seconds()),
	}, nil
}

func (a *AuthService) RefreshToken(refreshToken string) (*TokenResponse, error) {
	data := url.Values{}
	data.Set("client_id", a.clientID)
	data.Set("client_secret", a.clientSecret)
	data.Set("refresh_token", refreshToken)
	data.Set("grant_type", "refresh_token")

	req, err := http.NewRequest("POST", "https://login.microsoftonline.com/common/oauth2/v2.0/token",
		strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("create refresh token request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute refresh token request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("refresh token (status: %d): %s", resp.StatusCode, string(body))
	}

	var tokenResp TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, fmt.Errorf("decode refresh token response: %w", err)
	}

	return &tokenResp, nil
}

func (a *AuthService) getOAuthConfig() *oauth2.Config {
	return &oauth2.Config{
		ClientID:     a.clientID,
		ClientSecret: a.clientSecret,
		RedirectURL:  a.redirectURI,
		Scopes:       a.scopes,
		Endpoint:     microsoft.AzureADEndpoint("common"),
	}
}
