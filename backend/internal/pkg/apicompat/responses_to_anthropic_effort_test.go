package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// Regression coverage for the Codex CLI /model picker bug where a stale
// model_reasoning_effort value (from a previously selected model) gets
// persisted into config.toml and resent on a later request. Before this
// fix, mapResponsesEffortToAnthropic forwarded whatever string it was given
// straight into output_config.effort, so an invalid or model-unsupported
// value made Claude's native API reject the whole request with a raw enum
// error instead of sub2api just dropping the unusable field.
func newMinimalResponsesRequest(model, effort string) *ResponsesRequest {
	input, _ := json.Marshal("hi")
	return &ResponsesRequest{
		Model: model,
		Input: input,
		Reasoning: &ResponsesReasoning{
			Effort: effort,
		},
	}
}

func TestResponsesToAnthropicRequest_ValidEffortForwarded(t *testing.T) {
	req := newMinimalResponsesRequest("claude-sonnet-5", "high")
	out, err := ResponsesToAnthropicRequest(req)
	require.NoError(t, err)
	require.NotNil(t, out.OutputConfig)
	require.Equal(t, "high", out.OutputConfig.Effort)
	require.NotNil(t, out.Thinking)
}

func TestResponsesToAnthropicRequest_XHighMapsToMax(t *testing.T) {
	req := newMinimalResponsesRequest("claude-sonnet-5", "xhigh")
	out, err := ResponsesToAnthropicRequest(req)
	require.NoError(t, err)
	require.NotNil(t, out.OutputConfig)
	require.Equal(t, "max", out.OutputConfig.Effort)
}

func TestResponsesToAnthropicRequest_InvalidEffortDropped(t *testing.T) {
	// "none" is not part of Claude's output_config.effort enum
	// (low/medium/high/xhigh/max). A stale/invalid persisted value must be
	// omitted, not forwarded verbatim to Claude's API.
	req := newMinimalResponsesRequest("claude-sonnet-5", "none")
	out, err := ResponsesToAnthropicRequest(req)
	require.NoError(t, err)
	require.Nil(t, out.OutputConfig)
	require.Nil(t, out.Thinking)
}

func TestResponsesToAnthropicRequest_EffortUnsupportedByModelDropped(t *testing.T) {
	// claude-opus-4-5's effort catalog is low/medium/high only (no xhigh/max).
	req := newMinimalResponsesRequest("claude-opus-4-5", "xhigh")
	out, err := ResponsesToAnthropicRequest(req)
	require.NoError(t, err)
	require.Nil(t, out.OutputConfig)
}

func TestResponsesToAnthropicRequest_UnrecognizedModelFailsOpenOnEnumOnly(t *testing.T) {
	// Models EffortLevelsForModel doesn't recognize get the plain enum check
	// only, so a valid Claude effort level still passes through.
	req := newMinimalResponsesRequest("claude-some-future-model", "medium")
	out, err := ResponsesToAnthropicRequest(req)
	require.NoError(t, err)
	require.NotNil(t, out.OutputConfig)
	require.Equal(t, "medium", out.OutputConfig.Effort)
}
