package pipelines

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	"uuid"
)

func pipelineTestTransport(t *testing.T, url string) *vew.Transport {
	t.Helper()
	transport, err := vew.NewTransport(vew.Config{APIURL: url, TokenURL: url + "/oauth/token", ClientID: "id", ClientSecret: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	return transport
}

func pipelineToken(w http.ResponseWriter, request *http.Request) bool {
	if request.URL.Path != "/oauth/token" {
		return false
	}
	_, _ = io.WriteString(w, `{"access_token":"token","expires_in":3600}`)
	return true
}

func TestPipelineClientCollectionAndItem(t *testing.T) {
	const pipelineJSON = `{"projectId":"proj","pipelineId":"pipe","pipelineName":"image","pipelineDescription":"description","recipeId":"reci","recipeName":"recipe","recipeVersionId":"vers","recipeVersionName":"1.0.0","buildInstanceTypes":["m8i.2xlarge","m8i.4xlarge"],"pipelineSchedule":"0 0 * * ? *","productId":null,"status":"CREATING","distributionConfigArn":null,"infrastructureConfigArn":"arn:infra","pipelineArn":null,"createDate":"created","createdBy":"creator","lastUpdateDate":"updated","lastUpdatedBy":"updater"}`
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if pipelineToken(w, request) {
			return
		}
		calls++
		switch calls {
		case 1:
			if request.Method != http.MethodPost || request.URL.Path != "/projects/proj/pipelines" {
				t.Fatalf("create = %s %s", request.Method, request.URL.Path)
			}
			if _, err := uuid.Parse(request.Header.Get("Idempotency-Key")); err != nil {
				t.Fatalf("create key is not UUID: %v", err)
			}
			var got map[string]any
			if err := json.NewDecoder(request.Body).Decode(&got); err != nil {
				t.Fatal(err)
			}
			want := map[string]any{"pipelineName": "image", "pipelineDescription": "description", "recipeId": "reci", "recipeVersionId": "vers", "buildInstanceTypes": []any{"m8i.2xlarge", "m8i.4xlarge"}, "pipelineSchedule": "0 0 * * ? *"}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("create body = %#v", got)
			}
			w.Header().Set("Retry-After", "5")
			w.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(w, `{"pipelineId":"pipe"}`)
		case 2:
			if request.Method != http.MethodGet || request.URL.Path != "/projects/proj/pipelines" {
				t.Fatalf("list = %s %s", request.Method, request.URL.Path)
			}
			_, _ = io.WriteString(w, `{"pipelines":[`+pipelineJSON+`]}`)
		case 3:
			if request.Method != http.MethodGet || request.URL.Path != "/projects/proj/pipelines/pipe" {
				t.Fatalf("get = %s %s", request.Method, request.URL.Path)
			}
			w.Header().Set("Retry-After", "3")
			_, _ = io.WriteString(w, `{"pipeline":`+pipelineJSON+`}`)
		default:
			t.Fatalf("unexpected call %d", calls)
		}
	}))
	defer server.Close()
	client := NewClient(pipelineTestTransport(t, server.URL))
	input := CreatePipelineInput{Name: "image", Description: "description", RecipeID: "reci", RecipeVersionID: "vers", BuildInstanceTypes: []string{"m8i.2xlarge", "m8i.4xlarge"}, Schedule: "0 0 * * ? *"}
	created, err := client.CreatePipeline(context.Background(), "proj", input)
	if err != nil || created.ID != "pipe" || created.RetryAfter != 5*time.Second {
		t.Fatalf("create = %#v, %v", created, err)
	}
	list, err := client.ListPipelines(context.Background(), "proj")
	if err != nil || len(list) != 1 || list[0].ID != "pipe" {
		t.Fatalf("list = %#v, %v", list, err)
	}
	got, err := client.GetPipeline(context.Background(), "proj", "pipe")
	if err != nil || got.ID != "pipe" || got.ProjectID != "proj" || got.RecipeName != "recipe" || got.RecipeVersionName != "1.0.0" || got.Status != "CREATING" || got.RetryAfter != 3*time.Second {
		t.Fatalf("get = %#v, %v", got, err)
	}
	if got.ProductID != nil || got.DistributionConfigARN != nil || got.PipelineARN != nil || got.InfrastructureConfigARN == nil || *got.InfrastructureConfigARN != "arn:infra" {
		t.Fatalf("nullable fields = %#v", got)
	}
	if got.CreatedAt != "created" || got.CreatedBy != "creator" || got.UpdatedAt != "updated" || got.UpdatedBy != "updater" || !reflect.DeepEqual(got.BuildInstanceTypes, input.BuildInstanceTypes) {
		t.Fatalf("other fields = %#v", got)
	}
}

func TestCreatePipelineReusesKeyAndBodyAfterLostResponse(t *testing.T) {
	var keys []string
	var bodies [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if pipelineToken(w, request) {
			return
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		keys, bodies = append(keys, request.Header.Get("Idempotency-Key")), append(bodies, body)
		if len(keys) == 1 {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Fatal(err)
			}
			_ = conn.Close()
			return
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"pipelineId":"pipe"}`)
	}))
	defer server.Close()
	client := NewClient(pipelineTestTransport(t, server.URL))
	if _, err := client.CreatePipeline(context.Background(), "proj", CreatePipelineInput{Name: "image", BuildInstanceTypes: []string{"b", "a"}}); err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || keys[0] != keys[1] || !bytes.Equal(bodies[0], bodies[1]) {
		t.Fatalf("keys/bodies = %q/%q", keys, bodies)
	}
	if _, err := uuid.Parse(keys[0]); err != nil {
		t.Fatalf("key is not UUID: %v", err)
	}
}

func TestUpdatePipelineSendsFullDesiredStateWithNullProduct(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if pipelineToken(w, request) {
			return
		}
		if request.Method != http.MethodPut || request.URL.Path != "/projects/proj/pipelines/pipe" || request.Header.Get("Idempotency-Key") != "" {
			t.Fatalf("update = %s %s key=%q", request.Method, request.URL.Path, request.Header.Get("Idempotency-Key"))
		}
		var got map[string]any
		if err := json.NewDecoder(request.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		want := map[string]any{"recipeVersionId": "vers-2", "buildInstanceTypes": []any{"m8i.4xlarge", "m8i.2xlarge"}, "pipelineSchedule": "0 6 * * ? *", "productId": nil}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("update body = %#v", got)
		}
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"pipelineId":"pipe"}`)
	}))
	defer server.Close()
	input := UpdatePipelineInput{RecipeVersionID: "vers-2", BuildInstanceTypes: []string{"m8i.4xlarge", "m8i.2xlarge"}, Schedule: "0 6 * * ? *"}
	result, err := NewClient(pipelineTestTransport(t, server.URL)).UpdatePipeline(context.Background(), "proj", "pipe", input)
	if err != nil || result.ID != "pipe" || result.RetryAfter != 7*time.Second {
		t.Fatalf("update = %#v, %v", result, err)
	}
}

func TestRetirePipelineHasNoIdempotencyKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if pipelineToken(w, request) {
			return
		}
		if request.Method != http.MethodDelete || request.URL.Path != "/projects/proj/pipelines/pipe" || request.Header.Get("Idempotency-Key") != "" {
			t.Fatalf("retire = %s %s key=%q", request.Method, request.URL.Path, request.Header.Get("Idempotency-Key"))
		}
		body, err := io.ReadAll(request.Body)
		if err != nil || len(body) != 0 {
			t.Fatalf("retire body = %q, %v", body, err)
		}
		w.Header().Set("Retry-After", "2")
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"pipelineId":"pipe"}`)
	}))
	defer server.Close()
	result, err := NewClient(pipelineTestTransport(t, server.URL)).RetirePipeline(context.Background(), "proj", "pipe")
	if err != nil || result.ID != "pipe" || result.RetryAfter != 2*time.Second {
		t.Fatalf("retire = %#v, %v", result, err)
	}
}
