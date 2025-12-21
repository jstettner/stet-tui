package clients

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"
)

const plantaAPIBaseURL = "https://public.planta-api.com/v1"

// ActionType represents the type of plant care action.
type ActionType string

const (
	ActionWatering       ActionType = "watering"
	ActionFertilizing    ActionType = "fertilizing"
	ActionMisting        ActionType = "misting"
	ActionCleaning       ActionType = "cleaning"
	ActionRepotting      ActionType = "repotting"
	ActionProgressUpdate ActionType = "progressUpdate"
)

// CompletableActions lists actions that can be completed via API.
var CompletableActions = map[ActionType]bool{
	ActionWatering:    true,
	ActionFertilizing: true,
	ActionMisting:     true,
	ActionCleaning:    true,
}

// ActionSchedule represents a scheduled action for a plant.
type ActionSchedule struct {
	Next      *ActionDate `json:"next"`
	Completed *ActionDate `json:"completed"`
}

// ActionDate holds the date for an action.
type ActionDate struct {
	Date string `json:"date"`
}

// PlantNames holds the various names for a plant.
type PlantNames struct {
	LocalizedName string  `json:"localizedName"`
	Variety       *string `json:"variety"`
	Custom        *string `json:"custom"`
	Scientific    string  `json:"scientific"`
}

// PlantActions holds all action schedules for a plant.
type PlantActions struct {
	Watering       *ActionSchedule `json:"watering"`
	Fertilizing    *ActionSchedule `json:"fertilizing"`
	Misting        *ActionSchedule `json:"misting"`
	Cleaning       *ActionSchedule `json:"cleaning"`
	Repotting      *ActionSchedule `json:"repotting"`
	ProgressUpdate *ActionSchedule `json:"progressUpdate"`
}

// Plant represents a plant from the API.
type Plant struct {
	ID      string       `json:"id"`
	Names   PlantNames   `json:"names"`
	Actions PlantActions `json:"actions"`
}

// DisplayName returns the best display name for the plant.
func (p *Plant) DisplayName() string {
	if p.Names.Custom != nil && *p.Names.Custom != "" {
		return *p.Names.Custom
	}
	return p.Names.LocalizedName
}

// AddedPlantsResponse is the paginated response from /v1/addedPlants.
type AddedPlantsResponse struct {
	Status int `json:"status"`
	Data   []Plant `json:"data"`
	Pagination struct {
		NextPage *string `json:"nextPage"`
	} `json:"pagination"`
}

// PlantTask is a flattened view of a due/upcoming task for display.
type PlantTask struct {
	PlantID     string
	PlantName   string
	ActionType  ActionType
	DueDate     time.Time
	IsOverdue   bool
	IsToday     bool
	Completable bool
}

// PlantaClient is a client for the Planta API.
type PlantaClient struct {
	auth       *PlantaAuth
	httpClient *http.Client
}

// NewPlantaClient creates a new PlantaClient.
func NewPlantaClient(appCode string) *PlantaClient {
	return &PlantaClient{
		auth: NewPlantaAuth(appCode),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Auth returns the underlying PlantaAuth for authentication operations.
func (c *PlantaClient) Auth() *PlantaAuth {
	return c.auth
}

// IsAuthenticated returns true if valid tokens are available.
func (c *PlantaClient) IsAuthenticated() bool {
	tokens, err := c.auth.GetValidTokens()
	return err == nil && tokens != nil
}

// EnsureAuthenticated ensures we have valid tokens, exchanging code if needed.
func (c *PlantaClient) EnsureAuthenticated() error {
	tokens, err := c.auth.GetValidTokens()
	if err != nil {
		return fmt.Errorf("failed to get valid tokens: %w", err)
	}
	if tokens != nil {
		return nil // Already authenticated
	}

	// No tokens, try to exchange code
	if !c.auth.HasCredentials() {
		return fmt.Errorf("missing PLANTA_APP_CODE")
	}

	_, err = c.auth.ExchangeCode()
	if err != nil {
		return fmt.Errorf("failed to exchange code: %w", err)
	}

	return nil
}

// GetAllPlants fetches all plants, handling pagination.
func (c *PlantaClient) GetAllPlants() ([]Plant, error) {
	tokens, err := c.auth.GetValidTokens()
	if err != nil {
		return nil, fmt.Errorf("failed to get valid tokens: %w", err)
	}
	if tokens == nil {
		return nil, fmt.Errorf("not authenticated")
	}

	var allPlants []Plant
	var cursor string

	for {
		url := fmt.Sprintf("%s/addedPlants", plantaAPIBaseURL)
		if cursor != "" {
			url += "?cursor=" + cursor
		}

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
			tokens = newTokens

			req.Header.Set("Authorization", "Bearer "+newTokens.AccessToken)
			resp, err = c.httpClient.Do(req)
			if err != nil {
				return nil, fmt.Errorf("retry request failed: %w", err)
			}
			defer resp.Body.Close()
		}

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("API request failed with status: %d", resp.StatusCode)
		}

		var plantsResp AddedPlantsResponse
		if err := json.NewDecoder(resp.Body).Decode(&plantsResp); err != nil {
			return nil, fmt.Errorf("failed to decode response: %w", err)
		}

		allPlants = append(allPlants, plantsResp.Data...)

		if plantsResp.Pagination.NextPage == nil || *plantsResp.Pagination.NextPage == "" {
			break // No more pages
		}
		cursor = *plantsResp.Pagination.NextPage
	}

	return allPlants, nil
}

// GetDueTasks fetches plants and extracts tasks due within the specified days.
func (c *PlantaClient) GetDueTasks(withinDays int) ([]PlantTask, error) {
	plants, err := c.GetAllPlants()
	if err != nil {
		return nil, err
	}

	today := time.Now().Truncate(24 * time.Hour)
	cutoff := today.AddDate(0, 0, withinDays)
	var tasks []PlantTask

	for _, plant := range plants {
		// Check each action type
		actionSchedules := []struct {
			actionType ActionType
			schedule   *ActionSchedule
		}{
			{ActionWatering, plant.Actions.Watering},
			{ActionFertilizing, plant.Actions.Fertilizing},
			{ActionMisting, plant.Actions.Misting},
			{ActionCleaning, plant.Actions.Cleaning},
			{ActionRepotting, plant.Actions.Repotting},
			{ActionProgressUpdate, plant.Actions.ProgressUpdate},
		}

		for _, as := range actionSchedules {
			if as.schedule == nil || as.schedule.Next == nil {
				continue
			}

			// Try RFC3339 first (API returns "2025-12-19T00:00:00.000000000Z")
			dueDate, err := time.Parse(time.RFC3339Nano, as.schedule.Next.Date)
			if err != nil {
				// Fallback to RFC3339 without nanos
				dueDate, err = time.Parse(time.RFC3339, as.schedule.Next.Date)
				if err != nil {
					// Fallback to date-only format
					dueDate, err = time.Parse("2006-01-02", as.schedule.Next.Date)
					if err != nil {
						continue
					}
				}
			}
			// Truncate to date only for comparison
			dueDate = dueDate.Truncate(24 * time.Hour)

			if dueDate.After(cutoff) {
				continue // Not within our window
			}

			tasks = append(tasks, PlantTask{
				PlantID:     plant.ID,
				PlantName:   plant.DisplayName(),
				ActionType:  as.actionType,
				DueDate:     dueDate,
				IsOverdue:   dueDate.Before(today),
				IsToday:     dueDate.Equal(today),
				Completable: CompletableActions[as.actionType],
			})
		}
	}

	// Sort: overdue first, then by date, then by plant name
	sort.Slice(tasks, func(i, j int) bool {
		if !tasks[i].DueDate.Equal(tasks[j].DueDate) {
			return tasks[i].DueDate.Before(tasks[j].DueDate)
		}
		return tasks[i].PlantName < tasks[j].PlantName
	})

	return tasks, nil
}

// CompleteAction marks an action as complete for a plant.
func (c *PlantaClient) CompleteAction(plantID string, actionType ActionType) error {
	if !CompletableActions[actionType] {
		return fmt.Errorf("%s cannot be completed via API", actionType)
	}

	tokens, err := c.auth.GetValidTokens()
	if err != nil {
		return fmt.Errorf("failed to get valid tokens: %w", err)
	}
	if tokens == nil {
		return fmt.Errorf("not authenticated")
	}

	url := fmt.Sprintf("%s/addedPlants/%s/actions/complete", plantaAPIBaseURL, plantID)

	body := map[string]string{"actionType": string(actionType)}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("failed to marshal request body: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Handle 401 - try to refresh and retry once
	if resp.StatusCode == http.StatusUnauthorized {
		newTokens, err := c.auth.RefreshTokens(tokens.RefreshToken)
		if err != nil {
			return fmt.Errorf("token refresh failed: %w", err)
		}

		req.Header.Set("Authorization", "Bearer "+newTokens.AccessToken)
		resp, err = c.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("retry request failed: %w", err)
		}
		defer resp.Body.Close()
	}

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("API request failed with status: %d", resp.StatusCode)
	}

	return nil
}
