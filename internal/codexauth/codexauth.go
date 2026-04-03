package codexauth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	awsbase "github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/smithy-go"
)

var StoreAPIKeyFunc = StoreAPIKey
var LoadAPIKeyFunc = LoadAPIKey

const defaultSecretName = "openclaw/codex-api-key"

func DefaultSecretName() string {
	return defaultSecretName
}

func StoreAPIKey(ctx context.Context, profile, region, secretName, apiKey string) (string, error) {
	secretName = strings.TrimSpace(secretName)
	apiKey = strings.TrimSpace(apiKey)
	if secretName == "" {
		secretName = defaultSecretName
	}
	if apiKey == "" {
		return "", errors.New("codex api key is required")
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(strings.TrimSpace(region)), awsconfig.WithSharedConfigProfile(strings.TrimSpace(profile)))
	if err != nil {
		return "", fmt.Errorf("load aws config for codex secret: %w", err)
	}
	client := secretsmanager.NewFromConfig(cfg)

	out, err := client.CreateSecret(ctx, &secretsmanager.CreateSecretInput{
		Name:         awsbase.String(secretName),
		SecretString: awsbase.String(apiKey),
	})
	if err == nil {
		return awsString(out.ARN), nil
	}
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "ResourceExistsException" {
		return "", fmt.Errorf("create codex secret %q: %w", secretName, err)
	}

	putOut, putErr := client.PutSecretValue(ctx, &secretsmanager.PutSecretValueInput{
		SecretId:     awsbase.String(secretName),
		SecretString: awsbase.String(apiKey),
	})
	if putErr != nil {
		return "", fmt.Errorf("update codex secret %q: %w", secretName, putErr)
	}
	if arn := awsString(putOut.ARN); arn != "" {
		return arn, nil
	}
	return secretName, nil
}

func LoadAPIKey(ctx context.Context, profile, region, secretID string) (string, error) {
	secretID = strings.TrimSpace(secretID)
	if secretID == "" {
		return "", errors.New("codex secret id is required")
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(strings.TrimSpace(region)), awsconfig.WithSharedConfigProfile(strings.TrimSpace(profile)))
	if err != nil {
		return "", fmt.Errorf("load aws config for codex secret: %w", err)
	}
	client := secretsmanager.NewFromConfig(cfg)
	out, err := client.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: awsbase.String(secretID),
	})
	if err != nil {
		return "", fmt.Errorf("get codex secret %q: %w", secretID, err)
	}
	if strings.TrimSpace(awsString(out.SecretString)) == "" {
		return "", fmt.Errorf("codex secret %q did not contain a secret string", secretID)
	}
	return strings.TrimSpace(awsString(out.SecretString)), nil
}

func awsString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
