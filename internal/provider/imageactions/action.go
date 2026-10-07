package imageactions

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/elva-labs/terraform-provider-vew/internal/providerdata"
	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	"github.com/elva-labs/terraform-provider-vew/internal/vew/images"
	"github.com/hashicorp/terraform-plugin-framework/action"
	"github.com/hashicorp/terraform-plugin-framework/action/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const defaultTimeoutMinutes = int64(120)

var uuidPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
var safeIdentifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)

type imageBuildAPI interface {
	BuildImage(context.Context, string, string, string) (images.ActionResult, error)
	GetImage(context.Context, string, string) (images.Image, error)
}

type imageBuildAction struct {
	images imageBuildAPI
	waiter vew.Waiter
}

type imageBuildModel struct {
	ProjectID      types.String `tfsdk:"project_id"`
	PipelineID     types.String `tfsdk:"pipeline_id"`
	IdempotencyKey types.String `tfsdk:"idempotency_key"`
	TimeoutMinutes types.Int64  `tfsdk:"timeout_minutes"`
	// WaitForCompletion false returns once VEW accepted the build: the build continues in VEW and a
	// long image build does not hold the Terraform run (or its agent) for hours.
	WaitForCompletion types.Bool `tfsdk:"wait_for_completion"`
}

var _ action.ActionWithConfigure = (*imageBuildAction)(nil)

func NewImageBuildAction() action.Action { return &imageBuildAction{} }

func (a *imageBuildAction) Metadata(_ context.Context, request action.MetadataRequest, response *action.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_image_build"
}

func (a *imageBuildAction) Schema(_ context.Context, _ action.SchemaRequest, response *action.SchemaResponse) {
	response.Schema = schema.Schema{
		Description: "Builds a VEW image from a pipeline and waits for it to be created. Use the same idempotency key when retrying an interrupted invocation.",
		Attributes: map[string]schema.Attribute{
			"project_id":          schema.StringAttribute{Required: true, Description: "Project containing the pipeline."},
			"pipeline_id":         schema.StringAttribute{Required: true, Description: "Pipeline used to build the image."},
			"idempotency_key":     schema.StringAttribute{Required: true, Description: "Caller-supplied RFC 4122 UUID. Reuse it when retrying the same build."},
			"timeout_minutes":     schema.Int64Attribute{Optional: true, Description: "Maximum time to wait for image creation, in minutes. Defaults to 120. Ignored when wait_for_completion is false."},
			"wait_for_completion": schema.BoolAttribute{Optional: true, Description: "Wait until the image is created (the default, true). With false the action returns once VEW has accepted the build; the build continues in VEW and its result is read from VEW (for example the product's versions) on a later run."},
		},
	}
}

func (a *imageBuildAction) Configure(_ context.Context, request action.ConfigureRequest, response *action.ConfigureResponse) {
	if request.ProviderData == nil {
		return
	}
	data, ok := request.ProviderData.(providerdata.Data)
	if !ok {
		response.Diagnostics.AddError("Unable to configure VEW image build action", "Unexpected provider data type.")
		return
	}
	a.images = data.Images
	a.waiter = data.Waiter
}

func (a *imageBuildAction) Invoke(ctx context.Context, request action.InvokeRequest, response *action.InvokeResponse) {
	var model imageBuildModel
	response.Diagnostics.Append(request.Config.Get(ctx, &model)...)
	if response.Diagnostics.HasError() {
		return
	}
	valid := requiredID(response, "project_id", model.ProjectID)
	valid = requiredID(response, "pipeline_id", model.PipelineID) && valid
	if model.IdempotencyKey.IsNull() || model.IdempotencyKey.IsUnknown() || !uuidPattern.MatchString(model.IdempotencyKey.ValueString()) {
		response.Diagnostics.AddError("Invalid image build idempotency key", "idempotency_key must be a known RFC 4122 UUID.")
		valid = false
	}
	wait := true
	if !model.WaitForCompletion.IsNull() {
		if model.WaitForCompletion.IsUnknown() {
			response.Diagnostics.AddError("Invalid image build wait", "wait_for_completion must be known when the action is invoked.")
			valid = false
		} else {
			wait = model.WaitForCompletion.ValueBool()
		}
	}
	minutes := defaultTimeoutMinutes
	if !model.TimeoutMinutes.IsNull() {
		if model.TimeoutMinutes.IsUnknown() {
			response.Diagnostics.AddError("Invalid image build timeout", "timeout_minutes must be known when the action is invoked.")
			valid = false
		} else {
			minutes = model.TimeoutMinutes.ValueInt64()
		}
	}
	if minutes <= 0 || minutes > math.MaxInt64/int64(time.Minute) {
		response.Diagnostics.AddError("Invalid image build timeout", "timeout_minutes must be positive and within the supported duration range.")
		valid = false
	}
	if !valid {
		return
	}
	if a.images == nil || a.waiter == nil {
		response.Diagnostics.AddError("Unable to build VEW image", "The image build and status clients are not configured.")
		return
	}

	projectID, pipelineID, key := model.ProjectID.ValueString(), model.PipelineID.ValueString(), model.IdempotencyKey.ValueString()
	started, err := a.images.BuildImage(ctx, projectID, pipelineID, key)
	if err != nil {
		addImageBuildError(response, "execute", projectID, pipelineID, "", key, err)
		return
	}
	if strings.TrimSpace(started.ID) == "" {
		response.Diagnostics.AddError("VEW image build response is incomplete", "The build endpoint did not return an image ID. Retry with the same idempotency_key; the build may already have started.")
		return
	}
	imageID := started.ID
	sendProgress(response, "VEW image build reserved image "+displayID(imageID, key)+".")
	if !wait {
		sendProgress(response, "VEW image "+displayID(imageID, key)+" is building in VEW; not waiting for it (wait_for_completion = false).")
		return
	}

	lastStatus := ""
	upstreamID := ""
	err = a.waiter.Until(ctx, time.Duration(minutes)*time.Minute, started.RetryAfter, func(ctx context.Context) (vew.PollResult, error) {
		image, readErr := a.images.GetImage(ctx, projectID, imageID)
		if readErr != nil {
			return vew.PollResult{}, readErr
		}
		if image.ID != "" && image.ID != imageID {
			return vew.PollResult{}, errImageIDMismatch
		}
		if image.Status != lastStatus {
			lastStatus = image.Status
			if safeStatus(image.Status) {
				sendProgress(response, "VEW image "+displayID(imageID, key)+" status: "+image.Status+".")
			}
		}
		upstreamID = ""
		if image.UpstreamID != nil {
			upstreamID = *image.UpstreamID
		}
		return vew.PollResult{Status: image.Status, RetryAfter: image.RetryAfter}, nil
	}, func(status string) (bool, error) {
		switch status {
		case "CREATING":
			return false, nil
		case "CREATED":
			if strings.TrimSpace(upstreamID) == "" {
				return false, errMissingUpstreamID
			}
			return true, nil
		case "FAILED", "RETIRED", "DELETED":
			return false, &vew.TerminalStatusError{Status: status}
		default:
			return false, errUnsupportedStatus
		}
	})
	if err != nil {
		addImageBuildError(response, "read", projectID, pipelineID, imageID, key, err)
		return
	}
	sendProgress(response, "VEW image "+displayID(imageID, key)+" created with upstream ID "+displayID(upstreamID, key)+".")
}

var (
	errImageIDMismatch   = errors.New("image ID mismatch")
	errMissingUpstreamID = errors.New("missing upstream ID")
	errUnsupportedStatus = errors.New("unsupported image status")
)

func requiredID(response *action.InvokeResponse, name string, value types.String) bool {
	if value.IsNull() || value.IsUnknown() || strings.TrimSpace(value.ValueString()) == "" {
		response.Diagnostics.AddError("Invalid image build ID", name+" must be known and nonempty when the action is invoked.")
		return false
	}
	return true
}

func safeStatus(status string) bool {
	switch status {
	case "CREATING", "CREATED", "FAILED", "RETIRED", "DELETED":
		return true
	default:
		return false
	}
}

func displayID(id, key string) string {
	if safeIdentifier.MatchString(id) && !containsKey(id, key) {
		return id
	}
	return "(identifier omitted)"
}

func sendProgress(response *action.InvokeResponse, message string) {
	if response.SendProgress != nil {
		response.SendProgress(action.InvokeProgressEvent{Message: message})
	}
}

func safeTarget(label, id, key string) string {
	if safeIdentifier.MatchString(id) && !containsKey(id, key) {
		return fmt.Sprintf(" %s ID: %s.", label, id)
	}
	return ""
}

func containsKey(value, key string) bool {
	return key != "" && strings.Contains(strings.ToLower(value), strings.ToLower(key))
}
