package sub2api

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DiscoverModels reads candidates with the resolved model credential. A list
// entry is not proof that a particular CLI/protocol can generate a reply.
// Nothing is cached, and neither credential is returned in an error.
func (c *Client) DiscoverModels(ctx context.Context, key Resolved) ([]Model, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	models := []Model{}
	indices := map[string]int{}
	for _, anthropic := range []bool{true, false} {
		protocols := []string{}
		for _, endpoint := range key.Endpoints {
			if (endpoint.APIType == "anthropic-messages") == anthropic {
				protocols = append(protocols, endpoint.APIType)
			}
		}
		if len(protocols) == 0 {
			continue
		}
		seen := map[string]bool{}
		after := ""
		for page := 0; ; page++ {
			if page >= 100 {
				return nil, &Error{Code: "model_discovery_invalid"}
			}
			path := "/v1/models"
			if after != "" {
				path += "?after_id=" + url.QueryEscape(after)
			}
			var response struct {
				Data []struct {
					ID          string `json:"id"`
					DisplayName string `json:"display_name"`
				} `json:"data"`
				HasMore bool   `json:"has_more"`
				LastID  string `json:"last_id"`
			}
			if err := c.requestAuthenticated(ctx, key.BaseURL, key.APIKeyToken, http.MethodGet, path, nil, &response, anthropic); err != nil {
				code := "model_discovery_failed"
				switch Code(err) {
				case "source_unauthorized", "key_forbidden":
					code = "model_auth_failed"
				case "source_rate_limited":
					code = "model_rate_limited"
				case "source_invalid_response":
					code = "model_discovery_invalid"
				}
				return nil, &Error{Code: code}
			}
			if response.Data == nil {
				return nil, &Error{Code: "model_discovery_invalid"}
			}
			for _, item := range response.Data {
				if strings.TrimSpace(item.ID) == "" || len(item.ID) > 512 || strings.ContainsAny(item.ID, "\r\n\x00") || len(item.DisplayName) > 1024 || seen[item.ID] || len(seen) >= 10000 {
					return nil, &Error{Code: "model_discovery_invalid"}
				}
				seen[item.ID] = true
				if idx, ok := indices[item.ID]; ok {
					models[idx].APITypes = append(models[idx].APITypes, protocols...)
				} else {
					indices[item.ID] = len(models)
					models = append(models, Model{ID: item.ID, Name: item.DisplayName, APITypes: append([]string(nil), protocols...)})
				}
			}
			if !response.HasMore {
				break
			}
			if !anthropic || len(response.Data) == 0 || response.LastID != response.Data[len(response.Data)-1].ID || response.LastID == after {
				return nil, &Error{Code: "model_discovery_invalid"}
			}
			after = response.LastID
		}
	}
	if len(models) == 0 {
		return nil, &Error{Code: "model_discovery_empty"}
	}
	return models, nil
}
