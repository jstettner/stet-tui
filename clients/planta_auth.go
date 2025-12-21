package clients

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	plantaAuthURL    = "https://public.planta-api.com/v1/auth/authorize"
	plantaRefreshURL = "https://public.planta-api.com/v1/auth/refreshToken"
)

// PlantaTokens holds OAuth2 tokens for the Planta API.
type PlantaTokens struct {
	AccessToken  string    `json:"accessToken"`
	RefreshToken string    `json:"refreshToken"`
	TokenType    string    `json:"tokenType"`
	ExpiresAt    time.Time `json:"expiresAt"`
}

// IsExpired returns true if the access token has expired or will expire soon.
func (t *PlantaTokens) IsExpired() bool {
	if t.AccessToken == "" {
		return true
	}
	// Consider expired if within 5 minutes of expiry
	return time.Now().Add(5 * time.Minute).After(t.ExpiresAt)
}

// PlantaAuth handles authentication for the Planta API.
type PlantaAuth struct {
	AppCode    string
	tokensPath string
}

// NewPlantaAuth creates a new PlantaAuth instance.
func NewPlantaAuth(appCode string) *PlantaAuth {
	tokensPath := os.ExpandEnv("$HOME/.local/share/stet/planta_tokens.json")
	return &PlantaAuth{
		AppCode:    appCode,
		tokensPath: tokensPath,
	}
}

// LoadTokens loads tokens from disk.
func (a *PlantaAuth) LoadTokens() (*PlantaTokens, error) {
	data, err := os.ReadFile(a.tokensPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // No tokens yet
		}
		return nil, fmt.Errorf("failed to read tokens: %w", err)
	}

	var tokens PlantaTokens
	if err := json.Unmarshal(data, &tokens); err != nil {
		return nil, fmt.Errorf("failed to parse tokens: %w", err)
	}

	return &tokens, nil
}

// SaveTokens saves tokens to disk.
func (a *PlantaAuth) SaveTokens(tokens *PlantaTokens) error {
	// Ensure directory exists
	dir := filepath.Dir(a.tokensPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create tokens directory: %w", err)
	}

	data, err := json.MarshalIndent(tokens, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal tokens: %w", err)
	}

	if err := os.WriteFile(a.tokensPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write tokens: %w", err)
	}

	return nil
}

// GetValidTokens returns valid tokens, refreshing if necessary.
func (a *PlantaAuth) GetValidTokens() (*PlantaTokens, error) {
	tokens, err := a.LoadTokens()
	if err != nil {
		return nil, err
	}
	if tokens == nil {
		return nil, nil // No tokens, need to authenticate
	}

	if tokens.IsExpired() {
		// Try to refresh
		newTokens, err := a.RefreshTokens(tokens.RefreshToken)
		if err != nil {
			return nil, nil // Refresh failed, need to re-authenticate
		}
		return newTokens, nil
	}

	return tokens, nil
}

// plantaAuthResponse represents the API response from auth endpoints.
type plantaAuthResponse struct {
	Status int `json:"status"`
	Data   struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
		TokenType    string `json:"tokenType"`
		ExpiresAt    string `json:"expiresAt"`
	} `json:"data"`
}

// ExchangeCode exchanges the app code for tokens.
func (a *PlantaAuth) ExchangeCode() (*PlantaTokens, error) {
	body := map[string]string{"code": a.AppCode}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	resp, err := http.Post(plantaAuthURL, "application/json", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to exchange code: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("code exchange failed with status: %d", resp.StatusCode)
	}

	var authResp plantaAuthResponse
	if err := json.NewDecoder(resp.Body).Decode(&authResp); err != nil {
		return nil, fmt.Errorf("failed to decode token response: %w", err)
	}

	// Parse expiry time
	expiresAt, err := time.Parse(time.RFC3339, authResp.Data.ExpiresAt)
	if err != nil {
		// Default to 1 hour if parsing fails
		expiresAt = time.Now().Add(1 * time.Hour)
	}

	tokens := &PlantaTokens{
		AccessToken:  authResp.Data.AccessToken,
		RefreshToken: authResp.Data.RefreshToken,
		TokenType:    authResp.Data.TokenType,
		ExpiresAt:    expiresAt,
	}

	// Save the tokens
	if err := a.SaveTokens(tokens); err != nil {
		return nil, err
	}

	return tokens, nil
}

// RefreshTokens exchanges a refresh token for new tokens.
func (a *PlantaAuth) RefreshTokens(refreshToken string) (*PlantaTokens, error) {
	body := map[string]string{"refreshToken": refreshToken}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	resp, err := http.Post(plantaRefreshURL, "application/json", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to refresh tokens: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token refresh failed with status: %d", resp.StatusCode)
	}

	var authResp plantaAuthResponse
	if err := json.NewDecoder(resp.Body).Decode(&authResp); err != nil {
		return nil, fmt.Errorf("failed to decode token response: %w", err)
	}

	// Parse expiry time
	expiresAt, err := time.Parse(time.RFC3339, authResp.Data.ExpiresAt)
	if err != nil {
		expiresAt = time.Now().Add(1 * time.Hour)
	}

	tokens := &PlantaTokens{
		AccessToken:  authResp.Data.AccessToken,
		RefreshToken: authResp.Data.RefreshToken,
		TokenType:    authResp.Data.TokenType,
		ExpiresAt:    expiresAt,
	}

	// Save the new tokens
	if err := a.SaveTokens(tokens); err != nil {
		return nil, err
	}

	return tokens, nil
}

// HasCredentials returns true if the app code is configured.
func (a *PlantaAuth) HasCredentials() bool {
	return a.AppCode != "" &&
		!strings.HasPrefix(a.AppCode, "your_")
}
