package bedrock

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

type Generator interface {
	Generate(ctx context.Context, prompt string) (string, error)
}

type client struct {
	api     *bedrockruntime.Client
	modelID string
}

func New(ctx context.Context, region, modelID string) (Generator, error) {
	region = strings.TrimSpace(region)
	modelID = strings.TrimSpace(modelID)
	if region == "" {
		return nil, errors.New("bedrock region is required")
	}
	if modelID == "" {
		return nil, errors.New("bedrock model id is required")
	}

	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("load aws config for bedrock: %w", err)
	}
	return client{
		api:     bedrockruntime.NewFromConfig(cfg),
		modelID: modelID,
	}, nil
}

func (c client) Generate(ctx context.Context, prompt string) (string, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", errors.New("prompt is required")
	}
	if c.api == nil {
		return "", errors.New("bedrock client is not configured")
	}

	resp, err := c.api.Converse(ctx, &bedrockruntime.ConverseInput{
		ModelId: aws.String(c.modelID),
		Messages: []types.Message{{
			Role: types.ConversationRoleUser,
			Content: []types.ContentBlock{
				&types.ContentBlockMemberText{Value: prompt},
			},
		}},
	})
	if err != nil {
		return "", fmt.Errorf("invoke bedrock model %q: %w", c.modelID, err)
	}

	message, ok := resp.Output.(*types.ConverseOutputMemberMessage)
	if !ok {
		return "", fmt.Errorf("invoke bedrock model %q: unexpected response type", c.modelID)
	}
	var output strings.Builder
	for _, block := range message.Value.Content {
		if text, ok := block.(*types.ContentBlockMemberText); ok {
			output.WriteString(text.Value)
		}
	}
	if strings.TrimSpace(output.String()) == "" {
		return "", fmt.Errorf("invoke bedrock model %q: response contained no text", c.modelID)
	}
	return output.String(), nil
}
