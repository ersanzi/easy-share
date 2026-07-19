package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"easyshare/internal/api"
	"easyshare/internal/discovery"
	"easyshare/internal/task"
)

type Snapshot struct {
	Status api.Status       `json:"status"`
	Peers  []discovery.Peer `json:"peers"`
	Tasks  []task.Task      `json:"tasks"`
}
type Client struct {
	baseURL, token string
	http           *http.Client
}

func NewClient(baseURL, token string) *Client {
	return &Client{baseURL: baseURL, token: token, http: &http.Client{Timeout: 10 * time.Second}}
}
func (client *Client) request(ctx context.Context, method, path string, input, output any) error {
	var body *bytes.Reader
	if input == nil {
		body = bytes.NewReader(nil)
	} else {
		data, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, client.baseURL+path, body)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+client.token)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 {
		var apiError api.ErrorResponse
		_ = json.NewDecoder(response.Body).Decode(&apiError)
		return fmt.Errorf("%s", apiError.Message)
	}
	if output != nil {
		return json.NewDecoder(response.Body).Decode(output)
	}
	return nil
}
func (client *Client) Snapshot(ctx context.Context) (Snapshot, error) {
	var result Snapshot
	if err := client.request(ctx, http.MethodGet, "/api/status", nil, &result.Status); err != nil {
		return result, err
	}
	if err := client.request(ctx, http.MethodGet, "/api/peers", nil, &result.Peers); err != nil {
		return result, err
	}
	if err := client.request(ctx, http.MethodGet, "/api/tasks", nil, &result.Tasks); err != nil {
		return result, err
	}
	return result, nil
}
func (client *Client) Send(ctx context.Context, peerID, path string) error {
	return client.request(ctx, http.MethodPost, "/api/transfers", map[string]string{"peerId": peerID, "filePath": path}, nil)
}
func (client *Client) Accept(ctx context.Context, id string) error {
	return client.request(ctx, http.MethodPost, "/api/transfers/"+id+"/accept", nil, nil)
}
func (client *Client) AcceptTo(ctx context.Context, id, saveDir string) error {
	return client.request(ctx, http.MethodPost, "/api/transfers/"+id+"/accept", map[string]string{"saveDir": saveDir}, nil)
}
func (client *Client) Reject(ctx context.Context, id string) error {
	return client.request(ctx, http.MethodPost, "/api/transfers/"+id+"/reject", nil, nil)
}
func (client *Client) Action(ctx context.Context, path string) error {
	return client.request(ctx, http.MethodPost, path, nil, nil)
}
func (client *Client) ClearTasks(ctx context.Context) error {
	return client.request(ctx, http.MethodPost, "/api/tasks/clear", nil, nil)
}
