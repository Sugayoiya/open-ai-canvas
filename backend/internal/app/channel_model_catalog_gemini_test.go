package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"yingce/backend/internal/model"
	"yingce/backend/internal/outbound/outboundtest"
)

func TestPreviewAdminChannelModelsRecognizesGeminiAddress(t *testing.T) {
	for _, tc := range []struct {
		name      string
		host      string
		basePath  string
		apiFormat string
		wantPath  string
		gemini    bool
	}{
		{"official root", "generativelanguage.googleapis.com", "", "openai", "/v1beta/models", true},
		{"official default protocol", "generativelanguage.googleapis.com", "", "", "/v1beta/models", true},
		{"official trailing slash", "generativelanguage.googleapis.com", "/", "openai", "/v1beta/models", true},
		{"official beta", "generativelanguage.googleapis.com", "/v1beta", "openai", "/v1beta/models", true},
		{"official stable", "generativelanguage.googleapis.com", "/v1", "openai", "/v1/models", true},
		{"official OpenAI compatibility", "generativelanguage.googleapis.com", "/v1beta/openai", "openai", "/v1beta/openai/models", false},
		{"explicit Gemini gateway", "gateway.example", "/v1beta", "gemini", "/v1beta/models", true},
		{"explicit stable Gemini gateway", "gateway.example", "/v1", "gemini", "/v1/models", true},
		{"OpenAI gateway", "gateway.example", "", "openai", "/v1/models", false},
		{"lookalike host", "generativelanguage.googleapis.com.example", "", "openai", "/v1/models", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			outboundtest.DNS(t, [4]byte{127, 0, 0, 1}, tc.host)
			t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", tc.host)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != tc.wantPath || r.URL.RawQuery != "" {
					t.Errorf("catalog request = %s %s, want GET %s without query", r.Method, r.URL.String(), tc.wantPath)
					http.Error(w, "unexpected catalog path", http.StatusNotFound)
					return
				}
				if tc.gemini {
					if r.Header.Get("x-goog-api-key") != "synthetic-key" || r.Header.Get("Authorization") != "" {
						t.Error("native Gemini catalog must use only x-goog-api-key authentication")
						http.Error(w, "incorrect authentication", http.StatusUnauthorized)
						return
					}
				} else if r.Header.Get("Authorization") != "Bearer synthetic-key" || r.Header.Get("x-goog-api-key") != "" {
					t.Error("OpenAI catalog must retain Bearer authentication")
					http.Error(w, "incorrect authentication", http.StatusUnauthorized)
					return
				}
				if r.Header.Get("X-Catalog-Test") != "preserved" {
					t.Error("configured catalog header was lost")
				}
				w.Header().Set("Content-Type", "application/json")
				if tc.gemini {
					_ = json.NewEncoder(w).Encode(map[string]any{"models": []map[string]string{{"name": "models/gemini-test"}}})
				} else {
					_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"id": "gemini-test"}}})
				}
			}))
			defer upstream.Close()

			svc, db := newChannelModelTestService(t)
			admin := &model.User{ID: "admin", Role: model.UserRoleAdmin}
			baseURL := strings.Replace(upstream.URL, "127.0.0.1", tc.host, 1) + tc.basePath
			channel := model.ModelChannel{
				ID: "catalog-gemini", UserID: admin.ID, Scope: model.ChannelScopeSystem,
				Enabled: true, Name: "Catalog test", BaseURL: baseURL, APIKey: "synthetic-key",
				APIFormat: tc.apiFormat, ModelsJSON: `[]`, HeadersJSON: `[{"name":"X-Catalog-Test","value":"preserved"}]`,
			}
			if err := db.Create(&channel).Error; err != nil {
				t.Fatal(err)
			}
			models, err := svc.PreviewAdminChannelModels(context.Background(), admin, channel.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(models) != 1 || models[0] != "gemini-test" {
				t.Fatalf("catalog models = %v, want [gemini-test]", models)
			}
			var count int64
			if err := db.Model(&model.ChannelModel{}).Count(&count).Error; err != nil || count != 0 {
				t.Fatalf("preview must not import models: count=%d, error=%v", count, err)
			}
		})
	}
}
