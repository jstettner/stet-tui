package clients

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const ouraAPIBaseURL = "https://api.ouraring.com/v2"

// DailyReadiness represents a daily readiness score from the Oura API.
type DailyReadiness struct {
	ID                        string       `json:"id"`
	Day                       string       `json:"day"`
	Score                     int          `json:"score"`
	TemperatureDeviation      float64      `json:"temperature_deviation"`
	TemperatureTrendDeviation float64      `json:"temperature_trend_deviation"`
	Timestamp                 string       `json:"timestamp"`
	Contributors              Contributors `json:"contributors"`
}

// Contributors holds the factors that contribute to the readiness score.
type Contributors struct {
	ActivityBalance     int `json:"activity_balance"`
	BodyTemperature     int `json:"body_temperature"`
	HRVBalance          int `json:"hrv_balance"`
	PreviousDayActivity int `json:"previous_day_activity"`
	PreviousNight       int `json:"previous_night"`
	RecoveryIndex       int `json:"recovery_index"`
	RestingHeartRate    int `json:"resting_heart_rate"`
	SleepBalance        int `json:"sleep_balance"`
}

// ReadinessResponse represents the API response for daily readiness.
type ReadinessResponse struct {
	Data      []DailyReadiness `json:"data"`
	NextToken string           `json:"next_token,omitempty"`
}

// HeartRatePoint represents a single heart rate measurement.
type HeartRatePoint struct {
	BPM       int    `json:"bpm"`
	Source    string `json:"source"`
	Timestamp string `json:"timestamp"`
}

// HeartRateResponse represents the API response for heart rate data.
type HeartRateResponse struct {
	Data      []HeartRatePoint `json:"data"`
	NextToken string           `json:"next_token,omitempty"`
}

// OuraClient is a client for the Oura API.
type OuraClient struct {
	auth       *OuraAuth
	httpClient *http.Client
}

// NewOuraClient creates a new OuraClient.
func NewOuraClient(clientID, clientSecret string) *OuraClient {
	return &OuraClient{
		auth: NewOuraAuth(clientID, clientSecret),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Auth returns the underlying OuraAuth for authentication operations.
func (c *OuraClient) Auth() *OuraAuth {
	return c.auth
}

// IsAuthenticated returns true if valid tokens are available.
func (c *OuraClient) IsAuthenticated() bool {
	tokens, err := c.auth.GetValidTokens()
	return err == nil && tokens != nil
}

// GetTodayReadiness fetches the readiness score for today.
func (c *OuraClient) GetTodayReadiness() (*DailyReadiness, error) {
	tokens, err := c.auth.GetValidTokens()
	if err != nil {
		return nil, fmt.Errorf("failed to get valid tokens: %w", err)
	}
	if tokens == nil {
		return nil, fmt.Errorf("not authenticated")
	}

	today := time.Now().Format("2006-01-02")
	url := fmt.Sprintf("%s/usercollection/daily_readiness?start_date=%s&end_date=%s",
		ouraAPIBaseURL, today, today)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+tokens.AccessToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Handle 401 - try to refresh and retry once
	if resp.StatusCode == http.StatusUnauthorized {
		newTokens, err := c.auth.RefreshTokens(tokens.RefreshToken)
		if err != nil {
			return nil, fmt.Errorf("token refresh failed: %w", err)
		}

		req.Header.Set("Authorization", "Bearer "+newTokens.AccessToken)
		resp, err = c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("retry request failed: %w", err)
		}
		defer resp.Body.Close()
	}

	if resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("subscription expired - Oura data not available")
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("rate limited - please wait")
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API request failed with status: %d", resp.StatusCode)
	}

	var readinessResp ReadinessResponse
	if err := json.NewDecoder(resp.Body).Decode(&readinessResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if len(readinessResp.Data) == 0 {
		return nil, nil // No data for today yet
	}

	// Return the most recent readiness score
	return &readinessResp.Data[len(readinessResp.Data)-1], nil
}

// GetTodayHeartRate fetches heart rate data for today.
func (c *OuraClient) GetTodayHeartRate() ([]HeartRatePoint, error) {
	tokens, err := c.auth.GetValidTokens()
	if err != nil {
		return nil, fmt.Errorf("failed to get valid tokens: %w", err)
	}
	if tokens == nil {
		return nil, fmt.Errorf("not authenticated")
	}

	// Use start_datetime/end_datetime for heart rate (not start_date/end_date)
	// Start from midnight today, end at current time
	now := time.Now()
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	url := fmt.Sprintf("%s/usercollection/heartrate?start_datetime=%s&end_datetime=%s",
		ouraAPIBaseURL, startOfDay.Format(time.RFC3339), now.Format(time.RFC3339))

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+tokens.AccessToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Handle 401 - try to refresh and retry once
	if resp.StatusCode == http.StatusUnauthorized {
		newTokens, err := c.auth.RefreshTokens(tokens.RefreshToken)
		if err != nil {
			return nil, fmt.Errorf("token refresh failed: %w", err)
		}

		req.Header.Set("Authorization", "Bearer "+newTokens.AccessToken)
		resp, err = c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("retry request failed: %w", err)
		}
		defer resp.Body.Close()
	}

	if resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("subscription expired - Oura data not available")
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("rate limited - please wait")
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API request failed with status: %d", resp.StatusCode)
	}

	var hrResp HeartRateResponse
	if err := json.NewDecoder(resp.Body).Decode(&hrResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return hrResp.Data, nil
}
