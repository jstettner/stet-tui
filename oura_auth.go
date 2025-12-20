package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	ouraAuthURL     = "https://cloud.ouraring.com/oauth/authorize"
	ouraTokenURL    = "https://api.ouraring.com/oauth/token"
	ouraRedirectURI = "http://localhost:8089/callback"
	ouraCallbackPort = ":8089"
)

// OuraTokens holds OAuth2 tokens for the Oura API.
type OuraTokens struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	TokenType    string    `json:"token_type"`
	ExpiresIn    int       `json:"expires_in"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// IsExpired returns true if the access token has expired or will expire soon.
func (t *OuraTokens) IsExpired() bool {
	if t.AccessToken == "" {
		return true
	}
	// Consider expired if within 5 minutes of expiry
	return time.Now().Add(5 * time.Minute).After(t.ExpiresAt)
}

// OuraAuth handles OAuth2 authentication for the Oura API.
type OuraAuth struct {
	ClientID     string
	ClientSecret string
	tokensPath   string
}

// NewOuraAuth creates a new OuraAuth instance.
func NewOuraAuth(clientID, clientSecret string) *OuraAuth {
	tokensPath := os.ExpandEnv("$HOME/.local/share/stet/oura_tokens.json")
	return &OuraAuth{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		tokensPath:   tokensPath,
	}
}

// LoadTokens loads tokens from disk.
func (a *OuraAuth) LoadTokens() (*OuraTokens, error) {
	data, err := os.ReadFile(a.tokensPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // No tokens yet
		}
		return nil, fmt.Errorf("failed to read tokens: %w", err)
	}

	var tokens OuraTokens
	if err := json.Unmarshal(data, &tokens); err != nil {
		return nil, fmt.Errorf("failed to parse tokens: %w", err)
	}

	return &tokens, nil
}

// SaveTokens saves tokens to disk.
func (a *OuraAuth) SaveTokens(tokens *OuraTokens) error {
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
func (a *OuraAuth) GetValidTokens() (*OuraTokens, error) {
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

// RefreshTokens exchanges a refresh token for new tokens.
func (a *OuraAuth) RefreshTokens(refreshToken string) (*OuraTokens, error) {
	data := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {a.ClientID},
		"client_secret": {a.ClientSecret},
	}

	resp, err := http.PostForm(ouraTokenURL, data)
	if err != nil {
		return nil, fmt.Errorf("failed to refresh tokens: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token refresh failed with status: %d", resp.StatusCode)
	}

	var tokens OuraTokens
	if err := json.NewDecoder(resp.Body).Decode(&tokens); err != nil {
		return nil, fmt.Errorf("failed to decode token response: %w", err)
	}

	// Calculate expiry time
	tokens.ExpiresAt = time.Now().Add(time.Duration(tokens.ExpiresIn) * time.Second)

	// Save the new tokens
	if err := a.SaveTokens(&tokens); err != nil {
		return nil, err
	}

	return &tokens, nil
}

// StartAuthFlow initiates the OAuth2 authorization flow.
// It opens the browser and waits for the callback.
// Returns a channel that will receive the tokens or an error.
func (a *OuraAuth) StartAuthFlow(ctx context.Context) (<-chan *OuraTokens, <-chan error) {
	tokensChan := make(chan *OuraTokens, 1)
	errChan := make(chan error, 1)

	go func() {
		defer close(tokensChan)
		defer close(errChan)

		// Channel to receive the auth code from the callback
		codeChan := make(chan string, 1)
		codeErrChan := make(chan error, 1)

		// Start local server for callback
		server := &http.Server{Addr: ouraCallbackPort}
		http.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
			code := r.URL.Query().Get("code")
			errParam := r.URL.Query().Get("error")

			if errParam != "" {
				errDesc := r.URL.Query().Get("error_description")
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprintf(w, "Authorization failed: %s - %s", errParam, errDesc)
				codeErrChan <- fmt.Errorf("authorization failed: %s - %s", errParam, errDesc)
				return
			}

			if code == "" {
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprint(w, "No authorization code received")
				codeErrChan <- fmt.Errorf("no authorization code received")
				return
			}

			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, "<html><body><h1>Authorization successful!</h1><p>You can close this window and return to the app.</p></body></html>")
			codeChan <- code
		})

		// Start server in goroutine
		go func() {
			if err := server.ListenAndServe(); err != http.ErrServerClosed {
				codeErrChan <- fmt.Errorf("callback server error: %w", err)
			}
		}()

		// Build authorization URL
		authURL := fmt.Sprintf("%s?client_id=%s&redirect_uri=%s&response_type=code&scope=daily+heartrate",
			ouraAuthURL,
			url.QueryEscape(a.ClientID),
			url.QueryEscape(ouraRedirectURI),
		)

		// Open browser
		if err := openBrowser(authURL); err != nil {
			errChan <- fmt.Errorf("failed to open browser: %w", err)
			server.Shutdown(ctx)
			return
		}

		// Wait for callback or context cancellation
		select {
		case code := <-codeChan:
			// Exchange code for tokens
			tokens, err := a.exchangeCode(code)
			if err != nil {
				errChan <- err
			} else {
				tokensChan <- tokens
			}
		case err := <-codeErrChan:
			errChan <- err
		case <-ctx.Done():
			errChan <- ctx.Err()
		}

		// Shutdown server
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		server.Shutdown(shutdownCtx)
	}()

	return tokensChan, errChan
}

// exchangeCode exchanges an authorization code for tokens.
func (a *OuraAuth) exchangeCode(code string) (*OuraTokens, error) {
	data := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {a.ClientID},
		"client_secret": {a.ClientSecret},
		"redirect_uri":  {ouraRedirectURI},
	}

	resp, err := http.PostForm(ouraTokenURL, data)
	if err != nil {
		return nil, fmt.Errorf("failed to exchange code: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("code exchange failed with status: %d", resp.StatusCode)
	}

	var tokens OuraTokens
	if err := json.NewDecoder(resp.Body).Decode(&tokens); err != nil {
		return nil, fmt.Errorf("failed to decode token response: %w", err)
	}

	// Calculate expiry time
	tokens.ExpiresAt = time.Now().Add(time.Duration(tokens.ExpiresIn) * time.Second)

	// Save the tokens
	if err := a.SaveTokens(&tokens); err != nil {
		return nil, err
	}

	return &tokens, nil
}

// openBrowser opens the specified URL in the default browser.
func openBrowser(url string) error {
	var cmd string
	var args []string

	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
		args = []string{url}
	case "linux":
		cmd = "xdg-open"
		args = []string{url}
	case "windows":
		cmd = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", url}
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}

	return exec.Command(cmd, args...).Start()
}

// HasCredentials returns true if OAuth2 client credentials are configured.
func (a *OuraAuth) HasCredentials() bool {
	return a.ClientID != "" && a.ClientSecret != "" &&
		!strings.HasPrefix(a.ClientID, "your_") &&
		!strings.HasPrefix(a.ClientSecret, "your_")
}
