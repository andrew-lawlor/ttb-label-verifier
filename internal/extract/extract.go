// Package extract calls the Claude vision API to pull structured field
// data off a label image. This is the only place in the system an LLM is
// invoked — see SPEC.md section 5: extraction is the model's job,
// comparison is not.
package extract

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/andrewlawlor/ttb-label-verifier/internal/model"
)

const (
	anthropicAPIURL  = "https://api.anthropic.com/v1/messages"
	anthropicVersion = "2023-06-01"
	modelName        = "claude-sonnet-4-5"
	// requestTimeout leaves headroom under the ~5s end-to-end target
	// (see SPEC.md section 2) while still failing fast enough that a
	// stuck call doesn't stall a batch worker indefinitely.
	requestTimeout = 8 * time.Second
)

// toolSchema forces the model to return exactly the fields we need, with a
// per-field confidence score, instead of free-form prose we'd have to
// parse ourselves — this is what keeps extraction to a single round trip.
var toolSchema = map[string]any{
	"name":        "record_label_fields",
	"description": "Record the fields read from an alcohol beverage label image, with a confidence score per field.",
	"input_schema": map[string]any{
		"type": "object",
		"properties": fieldProps(
			"brand_name", "The brand name as printed on the label.",
			"class_type", "The class/type designation, e.g. 'Kentucky Straight Bourbon Whiskey'.",
			"alcohol_content", "The alcohol content as printed, e.g. '45% Alc./Vol. (90 Proof)'.",
			"net_contents", "The net contents as printed, e.g. '750 mL'.",
			"government_warning", "The full government health warning statement text, verbatim including punctuation and casing as printed.",
		),
		"required": []string{"brand_name", "class_type", "alcohol_content", "net_contents", "government_warning"},
	},
}

func fieldProps(pairs ...string) map[string]any {
	props := map[string]any{}
	for i := 0; i < len(pairs); i += 2 {
		name, desc := pairs[i], pairs[i+1]
		props[name] = map[string]any{
			"type": "object",
			"properties": map[string]any{
				"value":      map[string]any{"type": "string", "description": desc},
				"confidence": map[string]any{"type": "number", "description": "0-1 confidence that this value was read correctly, e.g. lower if the image is blurry or the field wasn't clearly visible."},
			},
			"required": []string{"value", "confidence"},
		}
	}
	return props
}

// Client calls the Anthropic API. Zero value is not usable — construct
// with New.
type Client struct {
	apiKey string
	http   *http.Client
}

func New(apiKey string) *Client {
	return &Client{
		apiKey: apiKey,
		http:   &http.Client{Timeout: requestTimeout},
	}
}

// ErrUnreadableImage is returned when the model itself reports it could
// not make out the label (as opposed to a network/API failure).
var ErrUnreadableImage = fmt.Errorf("label image could not be read")

// Extract sends one label image to Claude and returns the structured
// fields. imageBytes should be a JPEG or PNG.
func (c *Client) Extract(ctx context.Context, imageBytes []byte, mediaType string) (model.ExtractedFields, error) {
	if c.apiKey == "" {
		return model.ExtractedFields{}, fmt.Errorf("ANTHROPIC_API_KEY not configured")
	}

	b64 := base64.StdEncoding.EncodeToString(imageBytes)

	reqBody := map[string]any{
		"model":       modelName,
		"max_tokens":  1024,
		"tools":       []any{toolSchema},
		"tool_choice": map[string]any{"type": "tool", "name": "record_label_fields"},
		"messages": []any{
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{
						"type": "image",
						"source": map[string]any{
							"type":       "base64",
							"media_type": mediaType,
							"data":       b64,
						},
					},
					map[string]any{
						"type": "text",
						"text": "This is a photo of an alcohol beverage label submitted with a TTB label approval application. Read the label carefully and record each requested field exactly as printed, including capitalization and punctuation — this matters most for the government warning field, which must be transcribed verbatim. If a field is not visible or legible, set its value to an empty string and confidence to 0.",
					},
				},
			},
		},
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return model.ExtractedFields{}, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicAPIURL, bytes.NewReader(payload))
	if err != nil {
		return model.ExtractedFields{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", anthropicVersion)

	resp, err := c.http.Do(req)
	if err != nil {
		return model.ExtractedFields{}, fmt.Errorf("call anthropic api: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return model.ExtractedFields{}, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return model.ExtractedFields{}, fmt.Errorf("anthropic api error (%d): %s", resp.StatusCode, string(body))
	}

	return parseResponse(body)
}

type apiResponse struct {
	Content []struct {
		Type  string          `json:"type"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	} `json:"content"`
}

type toolInput struct {
	BrandName         model.FieldExtraction `json:"brand_name"`
	ClassType         model.FieldExtraction `json:"class_type"`
	AlcoholContent    model.FieldExtraction `json:"alcohol_content"`
	NetContents       model.FieldExtraction `json:"net_contents"`
	GovernmentWarning model.FieldExtraction `json:"government_warning"`
}

func parseResponse(body []byte) (model.ExtractedFields, error) {
	var resp apiResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return model.ExtractedFields{}, fmt.Errorf("unmarshal response: %w", err)
	}

	for _, block := range resp.Content {
		if block.Type != "tool_use" || block.Name != "record_label_fields" {
			continue
		}
		var in toolInput
		if err := json.Unmarshal(block.Input, &in); err != nil {
			return model.ExtractedFields{}, fmt.Errorf("unmarshal tool input: %w", err)
		}
		fields := model.ExtractedFields{
			BrandName:         in.BrandName,
			ClassType:         in.ClassType,
			AlcoholContent:    in.AlcoholContent,
			NetContents:       in.NetContents,
			GovernmentWarning: in.GovernmentWarning,
		}
		if allEmpty(fields) {
			return fields, ErrUnreadableImage
		}
		return fields, nil
	}

	return model.ExtractedFields{}, fmt.Errorf("no tool_use block in response")
}

func allEmpty(f model.ExtractedFields) bool {
	return f.BrandName.Value == "" && f.ClassType.Value == "" &&
		f.AlcoholContent.Value == "" && f.NetContents.Value == "" &&
		f.GovernmentWarning.Value == ""
}
