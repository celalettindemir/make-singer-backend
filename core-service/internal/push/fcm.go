package push

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const (
	fcmScope   = "https://www.googleapis.com/auth/firebase.messaging"
	fcmBaseURL = "https://fcm.googleapis.com"
	// androidChannel is the notification channel the app creates for job results.
	androidChannel = "uretim"
)

// ErrUnregistered: FCM no longer knows the token; the device row should go.
var ErrUnregistered = errors.New("push token unregistered")

// Sender delivers one message to one device token.
type Sender interface {
	Send(ctx context.Context, token string, m Message) error
}

// FCM sends through the FCM HTTP v1 API.
type FCM struct {
	projectID string
	baseURL   string
	client    *http.Client
}

var _ Sender = (*FCM)(nil)

// NewFCM builds a sender from a service-account JSON; the project id is read
// from it. httpClient (may be nil) carries the token requests and the sends.
func NewFCM(credentialsJSON []byte, httpClient *http.Client) (*FCM, error) {
	var meta struct {
		ProjectID string `json:"project_id"`
	}
	if err := json.Unmarshal(credentialsJSON, &meta); err != nil || meta.ProjectID == "" {
		return nil, errors.New("push: credentials JSON has no project_id")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, httpClient)
	creds, err := google.CredentialsFromJSONWithType(ctx, credentialsJSON, google.ServiceAccount, fcmScope)
	if err != nil {
		return nil, fmt.Errorf("push: credentials: %w", err)
	}
	client := oauth2.NewClient(ctx, creds.TokenSource)
	client.Timeout = httpClient.Timeout
	return &FCM{projectID: meta.ProjectID, baseURL: fcmBaseURL, client: client}, nil
}

type fcmError struct {
	Error struct {
		Status  string `json:"status"`
		Details []struct {
			ErrorCode string `json:"errorCode"`
		} `json:"details"`
	} `json:"error"`
}

func (f *FCM) Send(ctx context.Context, token string, m Message) error {
	body, err := json.Marshal(map[string]any{"message": map[string]any{
		"token":        token,
		"notification": map[string]string{"title": m.Title, "body": m.Body},
		"data":         m.Data,
		"android": map[string]any{
			"priority":     "HIGH",
			"notification": map[string]string{"channel_id": androidChannel},
		},
		"apns": map[string]any{"payload": map[string]any{"aps": map[string]any{"sound": "default"}}},
	}})
	if err != nil {
		return err
	}
	endpoint := f.baseURL + "/v1/projects/" + url.PathEscape(f.projectID) + "/messages:send"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.client.Do(req)
	if err != nil {
		return fmt.Errorf("push: fcm request: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode/100 == 2 {
		return nil
	}
	if resp.StatusCode == http.StatusNotFound {
		return ErrUnregistered
	}
	var fe fcmError
	if json.Unmarshal(raw, &fe) == nil {
		if fe.Error.Status == "UNREGISTERED" {
			return ErrUnregistered
		}
		for _, d := range fe.Error.Details {
			if d.ErrorCode == "UNREGISTERED" {
				return ErrUnregistered
			}
		}
	}
	return fmt.Errorf("push: fcm status %d", resp.StatusCode)
}
