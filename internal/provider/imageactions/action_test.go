package imageactions

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	"github.com/elva-labs/terraform-provider-vew/internal/vew/images"
	"github.com/hashicorp/terraform-plugin-framework/action"
	"github.com/hashicorp/terraform-plugin-framework/action/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

const testKey = "123e4567-e89b-42d3-a456-426614174000"

type imageStub struct {
	buildResult images.ActionResult
	buildErr    error
	onBuild     func()
	reads       []images.Image
	readErr     error
	buildCalls  [][3]string
	readCalls   [][2]string
}

func (s *imageStub) BuildImage(_ context.Context, projectID, pipelineID, key string) (images.ActionResult, error) {
	s.buildCalls = append(s.buildCalls, [3]string{projectID, pipelineID, key})
	if s.onBuild != nil {
		s.onBuild()
	}
	return s.buildResult, s.buildErr
}

func (s *imageStub) GetImage(_ context.Context, projectID, imageID string) (images.Image, error) {
	s.readCalls = append(s.readCalls, [2]string{projectID, imageID})
	if s.readErr != nil {
		return images.Image{}, s.readErr
	}
	if len(s.reads) == 0 {
		return images.Image{ID: imageID, Status: "CREATING"}, nil
	}
	result := s.reads[0]
	s.reads = s.reads[1:]
	return result, nil
}

type fastWaiter struct {
	timeout time.Duration
	delay   time.Duration
	forced  error
}

func (w *fastWaiter) Until(ctx context.Context, timeout, delay time.Duration, read vew.StatusReader, evaluate vew.StatusEvaluator) error {
	w.timeout, w.delay = timeout, delay
	if w.forced != nil {
		return w.forced
	}
	for i := 0; i < 8; i++ {
		result, err := read(ctx)
		if err != nil {
			return err
		}
		done, err := evaluate(result.Status)
		if done || err != nil {
			return err
		}
	}
	return &vew.TimeoutError{LastStatus: "CREATING"}
}

func testConfig(t *testing.T, values map[string]any) tfsdk.Config {
	t.Helper()
	a := NewImageBuildAction()
	var response action.SchemaResponse
	a.Schema(context.Background(), action.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("schema: %v", response.Diagnostics)
	}
	raw := make(map[string]tftypes.Value, len(response.Schema.Attributes))
	for name := range response.Schema.Attributes {
		var typ tftypes.Type = tftypes.String
		if name == "timeout_minutes" {
			typ = tftypes.Number
		}
		if name == "wait_for_completion" {
			typ = tftypes.Bool
		}
		raw[name] = tftypes.NewValue(typ, values[name])
	}
	return tfsdk.Config{Schema: response.Schema, Raw: tftypes.NewValue(response.Schema.Type().TerraformType(context.Background()), raw)}
}

func runAction(t *testing.T, client *imageStub, waiter *fastWaiter, values map[string]any) (action.InvokeResponse, []string) {
	t.Helper()
	a := &imageBuildAction{images: client, waiter: waiter}
	var response action.InvokeResponse
	var progress []string
	response.SendProgress = func(event action.InvokeProgressEvent) { progress = append(progress, event.Message) }
	a.Invoke(context.Background(), action.InvokeRequest{Config: testConfig(t, values)}, &response)
	return response, progress
}

func validValues() map[string]any {
	return map[string]any{"project_id": "project", "pipeline_id": "pipeline", "idempotency_key": testKey}
}

func upstream(value string) *string { return &value }

func TestSchemaAndSuccessfulBuild(t *testing.T) {
	a := NewImageBuildAction()
	var metadata action.MetadataResponse
	a.Metadata(context.Background(), action.MetadataRequest{ProviderTypeName: "vew"}, &metadata)
	if metadata.TypeName != "vew_image_build" {
		t.Fatalf("type name = %q", metadata.TypeName)
	}
	var declaration action.SchemaResponse
	a.Schema(context.Background(), action.SchemaRequest{}, &declaration)
	for _, name := range []string{"project_id", "pipeline_id", "idempotency_key"} {
		attribute, ok := declaration.Schema.Attributes[name].(schema.StringAttribute)
		if !ok || !attribute.Required {
			t.Errorf("%s should be a required string", name)
		}
	}
	if attribute, ok := declaration.Schema.Attributes["timeout_minutes"].(schema.Int64Attribute); !ok || !attribute.Optional {
		t.Fatal("timeout_minutes should be an optional integer")
	}
	client := &imageStub{
		buildResult: images.ActionResult{ID: "image", RetryAfter: 3 * time.Second},
		reads: []images.Image{
			{ID: "image", Status: "CREATING"},
			{ID: "image", Status: "CREATING"},
			{ID: "image", Status: "CREATED", UpstreamID: upstream("ami-123")},
		},
	}
	waiter := &fastWaiter{}
	response, progress := runAction(t, client, waiter, validValues())
	if response.Diagnostics.HasError() {
		t.Fatalf("diagnostics: %v", response.Diagnostics)
	}
	if waiter.timeout != 120*time.Minute || waiter.delay != 3*time.Second {
		t.Fatalf("wait arguments = %v, %v", waiter.timeout, waiter.delay)
	}
	if len(client.buildCalls) != 1 || client.buildCalls[0] != [3]string{"project", "pipeline", testKey} || len(client.readCalls) != 3 {
		t.Fatalf("calls = %v %v", client.buildCalls, client.readCalls)
	}
	want := []string{"VEW image build reserved image image.", "VEW image image status: CREATING.", "VEW image image status: CREATED.", "VEW image image created with upstream ID ami-123."}
	if !reflect.DeepEqual(progress, want) {
		t.Fatalf("progress = %#v, want %#v", progress, want)
	}
}

func TestNoWaitReturnsAfterTheBuildIsAccepted(t *testing.T) {
	client := &imageStub{
		buildResult: images.ActionResult{ID: "image", RetryAfter: 3 * time.Second},
		reads:       []images.Image{{ID: "image", Status: "CREATING"}},
	}
	waiter := &fastWaiter{}
	values := validValues()
	values["wait_for_completion"] = false
	values["timeout_minutes"] = int64(180)
	response, progress := runAction(t, client, waiter, values)
	if response.Diagnostics.HasError() {
		t.Fatalf("diagnostics: %v", response.Diagnostics)
	}
	if len(client.buildCalls) != 1 || len(client.readCalls) != 0 {
		t.Fatalf("calls = %v %v, want one build and no reads", client.buildCalls, client.readCalls)
	}
	want := []string{"VEW image build reserved image image.", "VEW image image is building in VEW; not waiting for it (wait_for_completion = false)."}
	if !reflect.DeepEqual(progress, want) {
		t.Fatalf("progress = %#v, want %#v", progress, want)
	}
}

func TestUnknownWaitNeverStartsBuild(t *testing.T) {
	client := &imageStub{buildResult: images.ActionResult{ID: "image"}}
	values := validValues()
	values["wait_for_completion"] = tftypes.UnknownValue
	response, _ := runAction(t, client, &fastWaiter{}, values)
	if !response.Diagnostics.HasError() || len(client.buildCalls) != 0 {
		t.Fatalf("want an error and no build, got %v / %v", response.Diagnostics, client.buildCalls)
	}
}

func TestSameKeyReinvocationResumesSameImage(t *testing.T) {
	client := &imageStub{
		buildResult: images.ActionResult{ID: "image"},
		reads: []images.Image{
			{ID: "image", Status: "CREATED", UpstreamID: upstream("ami-123")},
			{ID: "image", Status: "CREATED", UpstreamID: upstream("ami-123")},
		},
	}
	for range 2 {
		response, progress := runAction(t, client, &fastWaiter{}, validValues())
		if response.Diagnostics.HasError() {
			t.Fatalf("diagnostics: %v", response.Diagnostics)
		}
		if len(progress) == 0 || progress[0] != "VEW image build reserved image image." {
			t.Fatalf("progress = %v", progress)
		}
	}
	if len(client.buildCalls) != 2 || client.buildCalls[0] != client.buildCalls[1] || client.buildCalls[0][2] != testKey {
		t.Fatalf("reinvocation build calls = %v", client.buildCalls)
	}
}

func TestInvalidInputsNeverStartBuild(t *testing.T) {
	for _, tc := range []struct {
		name, field string
		value       any
	}{
		{"missing project", "project_id", nil},
		{"unknown pipeline", "pipeline_id", tftypes.UnknownValue},
		{"blank pipeline", "pipeline_id", "  "},
		{"bad UUID", "idempotency_key", "not-a-uuid"},
		{"wrong UUID variant", "idempotency_key", "123e4567-e89b-42d3-7456-426614174000"},
		{"zero timeout", "timeout_minutes", int64(0)},
		{"negative timeout", "timeout_minutes", int64(-1)},
		{"unknown timeout", "timeout_minutes", tftypes.UnknownValue},
		{"overflow timeout", "timeout_minutes", int64(1 << 62)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			values := validValues()
			values[tc.field] = tc.value
			client := &imageStub{}
			response, _ := runAction(t, client, &fastWaiter{}, values)
			if !response.Diagnostics.HasError() || len(client.buildCalls) != 0 {
				t.Fatalf("diagnostics=%v, calls=%v", response.Diagnostics, client.buildCalls)
			}
		})
	}
}

func TestTerminalAndIncompleteBuilds(t *testing.T) {
	for _, tc := range []struct {
		name, status string
		upstream     *string
		want         string
	}{
		{"failed", "FAILED", nil, "terminal status FAILED"},
		{"retired", "RETIRED", nil, "terminal status RETIRED"},
		{"deleted", "DELETED", nil, "terminal status DELETED"},
		{"unsupported", "UNKNOWN", nil, "unsupported status"},
		{"created without upstream", "CREATED", nil, "without an upstream ID"},
		{"created with blank upstream", "CREATED", upstream(" "), "without an upstream ID"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &imageStub{buildResult: images.ActionResult{ID: "image"}, reads: []images.Image{{ID: "image", Status: tc.status, UpstreamID: tc.upstream}}}
			response, progress := runAction(t, client, &fastWaiter{}, validValues())
			if !response.Diagnostics.HasError() || !strings.Contains(response.Diagnostics[0].Detail(), tc.want) {
				t.Fatalf("diagnostics = %v", response.Diagnostics)
			}
			if strings.Contains(strings.Join(progress, " "), testKey) {
				t.Fatal("key leaked in progress")
			}
		})
	}
}

func TestSafeFailureDiagnostics(t *testing.T) {
	secret := "Bearer secret-token"
	for _, tc := range []struct {
		name, phase string
		err         error
		want        []string
	}{
		{"execute permission", "execute", &vew.APIError{Status: 403, Problem: vew.Problem{Code: "ACCESS_DENIED", RequestID: "request-1", Detail: secret}}, []string{"pipeline.execute", "HTTP status 403", "request-1"}},
		{"read permission", "read", &vew.APIError{Status: 403, Problem: vew.Problem{Code: "ACCESS_DENIED", RequestID: "request-1", Detail: secret}}, []string{"pipeline.read", "image ID: image", "HTTP status 403"}},
		{"transport", "read", errors.New(secret + " raw body"), []string{"image ID: image", "same idempotency_key"}},
		{"timeout", "read", &vew.TimeoutError{LastStatus: "CREATING"}, []string{"Timed out", "Last status: CREATING"}},
		{"cancel", "read", context.Canceled, []string{"canceled", "same idempotency_key"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &imageStub{buildResult: images.ActionResult{ID: "image"}}
			waiter := &fastWaiter{}
			if tc.phase == "execute" {
				client.buildErr = tc.err
			} else if tc.name == "read permission" || tc.name == "transport" {
				client.readErr = tc.err
			} else {
				waiter.forced = tc.err
			}
			response, progress := runAction(t, client, waiter, validValues())
			if !response.Diagnostics.HasError() {
				t.Fatal("expected error")
			}
			message := response.Diagnostics[0].Detail()
			for _, value := range tc.want {
				if !strings.Contains(message, value) {
					t.Errorf("missing %q from %q", value, message)
				}
			}
			for _, unsafe := range []string{secret, "raw body", testKey} {
				if strings.Contains(message+strings.Join(progress, " "), unsafe) {
					t.Errorf("leaked %q", unsafe)
				}
			}
		})
	}
}

func TestCustomTimeoutAndMissingImageID(t *testing.T) {
	values := validValues()
	values["timeout_minutes"] = int64(7)
	client := &imageStub{buildResult: images.ActionResult{ID: "image"}, reads: []images.Image{{ID: "image", Status: "CREATED", UpstreamID: upstream("upstream")}}}
	waiter := &fastWaiter{}
	response, _ := runAction(t, client, waiter, values)
	if response.Diagnostics.HasError() || waiter.timeout != 7*time.Minute {
		t.Fatalf("diagnostics=%v timeout=%v", response.Diagnostics, waiter.timeout)
	}
	client = &imageStub{}
	response, _ = runAction(t, client, &fastWaiter{}, validValues())
	if !response.Diagnostics.HasError() || len(client.readCalls) != 0 || !strings.Contains(response.Diagnostics[0].Detail(), "same idempotency_key") {
		t.Fatalf("missing ID result: %v, reads=%v", response.Diagnostics, client.readCalls)
	}
}

func TestReadIdentityAndCurrentUpstreamAreRequired(t *testing.T) {
	for _, tc := range []struct {
		name  string
		reads []images.Image
		want  string
	}{
		{"different image", []images.Image{{ID: "another", Status: "CREATED", UpstreamID: upstream("upstream")}}, "different image ID"},
		{"stale upstream", []images.Image{{ID: "image", Status: "CREATING", UpstreamID: upstream("upstream")}, {ID: "image", Status: "CREATED"}}, "without an upstream ID"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &imageStub{buildResult: images.ActionResult{ID: "image"}, reads: tc.reads}
			response, _ := runAction(t, client, &fastWaiter{}, validValues())
			if !response.Diagnostics.HasError() || !strings.Contains(response.Diagnostics[0].Detail(), tc.want) {
				t.Fatalf("diagnostics = %v", response.Diagnostics)
			}
		})
	}
}

func TestUntrustedAPIFieldsAndIdentifiersAreNotEmitted(t *testing.T) {
	secret := "Bearer secret-token"
	client := &imageStub{buildErr: &vew.APIError{Status: 403, Problem: vew.Problem{
		Code: "BAD\n" + secret, RequestID: "request " + secret, Detail: "body " + secret,
	}}}
	values := validValues()
	values["project_id"] = "project\n" + secret
	values["pipeline_id"] = "pipeline " + secret
	response, progress := runAction(t, client, &fastWaiter{}, values)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected error")
	}
	message := response.Diagnostics[0].Detail() + strings.Join(progress, " ")
	for _, unsafe := range []string{secret, "body", "BAD", "request ", testKey, "project ID:", "pipeline ID:"} {
		if strings.Contains(message, unsafe) {
			t.Errorf("leaked %q in %q", unsafe, message)
		}
	}
}

func TestIdempotencyKeyNeverAppearsInDiagnosticsOrProgress(t *testing.T) {
	for _, tc := range []struct {
		name, phase string
		err         error
	}{
		{"problem code", "execute", &vew.APIError{Status: 400, Problem: vew.Problem{Code: testKey}}},
		{"request ID", "execute", &vew.APIError{Status: 400, Problem: vew.Problem{RequestID: testKey}}},
		{"detail", "execute", &vew.APIError{Status: 400, Problem: vew.Problem{Detail: testKey}}},
		{"transport", "execute", errors.New(testKey)},
		{"read metadata", "read", &vew.APIError{Status: 403, Problem: vew.Problem{Code: testKey, RequestID: testKey, Detail: testKey}}},
		{"read transport", "read", errors.New(testKey)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &imageStub{buildResult: images.ActionResult{ID: "image"}}
			if tc.phase == "execute" {
				client.buildErr = tc.err
			} else {
				client.readErr = tc.err
			}
			response, progress := runAction(t, client, &fastWaiter{}, validValues())
			if !response.Diagnostics.HasError() {
				t.Fatal("expected an error")
			}
			for _, diagnostic := range response.Diagnostics {
				if strings.Contains(diagnostic.Summary()+diagnostic.Detail()+strings.Join(progress, " "), testKey) {
					t.Fatalf("key leaked in diagnostic or progress: %v %v", response.Diagnostics, progress)
				}
			}
		})
	}
	client := &imageStub{buildResult: images.ActionResult{ID: testKey}, reads: []images.Image{{ID: testKey, Status: "CREATED", UpstreamID: upstream(testKey)}}}
	response, progress := runAction(t, client, &fastWaiter{}, validValues())
	if response.Diagnostics.HasError() || strings.Contains(strings.Join(progress, " "), testKey) {
		t.Fatalf("key exposed as returned identifiers: %v %v", response.Diagnostics, progress)
	}
}

func TestCancellationAfterBuildResponse(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := &imageStub{buildResult: images.ActionResult{ID: "image"}, onBuild: cancel}
	a := &imageBuildAction{images: client, waiter: vew.NewWaiter()}
	var response action.InvokeResponse
	var progress []string
	response.SendProgress = func(event action.InvokeProgressEvent) { progress = append(progress, event.Message) }
	a.Invoke(ctx, action.InvokeRequest{Config: testConfig(t, validValues())}, &response)
	if !response.Diagnostics.HasError() || !strings.Contains(response.Diagnostics[0].Detail(), "canceled") {
		t.Fatalf("canceled invocation diagnostics: %v", response.Diagnostics)
	}
	if len(client.buildCalls) != 1 || len(client.readCalls) != 0 {
		t.Fatalf("canceled invocation calls: build=%v reads=%v", client.buildCalls, client.readCalls)
	}
	if strings.Contains(response.Diagnostics[0].Detail()+strings.Join(progress, " "), testKey) {
		t.Fatal("cancellation output exposed the key")
	}
}
