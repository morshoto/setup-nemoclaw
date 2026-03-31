package aws

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	awsbase "github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"openclaw/internal/provider"
)

type Config struct {
	Profile string
}

type Provider struct {
	Config Config

	loadDefaultConfig func(ctx context.Context, optFns ...func(*awsconfig.LoadOptions) error) (awsbase.Config, error)
	newSTSClient      func(cfg awsbase.Config) stsClient
}

type stsClient interface {
	GetCallerIdentity(ctx context.Context, params *sts.GetCallerIdentityInput, optFns ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error)
}

const QuotaSourceMock = "mock"

func New(cfg Config) *Provider {
	return &Provider{
		Config:            cfg,
		loadDefaultConfig: awsconfig.LoadDefaultConfig,
		newSTSClient:      func(cfg awsbase.Config) stsClient { return sts.NewFromConfig(cfg) },
	}
}

var _ provider.CloudProvider = (*Provider)(nil)

func (p *Provider) AuthCheck(ctx context.Context) (provider.AuthStatus, error) {
	p.ensureDeps()
	cfg, err := p.loadAWSConfig(ctx)
	if err != nil {
		return provider.AuthStatus{}, err
	}

	if _, err := cfg.Credentials.Retrieve(ctx); err != nil {
		return provider.AuthStatus{}, classifyAuthError(err, p.Config.Profile, authStageCredentials)
	}

	client := p.newSTSClient(cfg)
	out, err := client.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return provider.AuthStatus{}, classifyAuthError(err, p.Config.Profile, authStageAPI)
	}

	return provider.AuthStatus{
		Profile: p.Config.Profile,
		Account: awsString(out.Account),
		Arn:     awsString(out.Arn),
		UserID:  awsString(out.UserId),
	}, nil
}

func (p *Provider) ListRegions(ctx context.Context) ([]string, error) {
	return []string{"ap-northeast-1", "us-east-1", "us-west-2"}, nil
}

func (p *Provider) CheckGPUQuota(ctx context.Context, region, instanceFamily string) (provider.GPUQuotaReport, error) {
	family := strings.ToLower(strings.TrimSpace(instanceFamily))
	if family == "" {
		family = "g5"
	}

	switch family {
	case "g5", "g4dn", "g4ad", "g6":
	default:
		return provider.GPUQuotaReport{}, fmt.Errorf("unsupported GPU family %q", instanceFamily)
	}

	report := provider.GPUQuotaReport{
		Source:         QuotaSourceMock,
		Region:         region,
		InstanceFamily: family,
		Checks: []provider.GPUQuotaCheck{
			{
				QuotaName:          "Running On-Demand G and VT instances",
				CurrentLimit:       0,
				CurrentUsage:       nil,
				EstimatedRemaining: 0,
				UsageIsEstimated:   true,
			},
			{
				QuotaName:          "All G and VT Spot Instance Requests",
				CurrentLimit:       0,
				CurrentUsage:       nil,
				EstimatedRemaining: 0,
				UsageIsEstimated:   true,
			},
		},
		LikelyCreatable: false,
		Notes: []string{
			"Mock report only: live AWS Service Quotas access is not wired yet.",
			"Do not treat these values as a real capacity check.",
		},
	}

	return report, nil
}

func (p *Provider) ListInstanceTypes(ctx context.Context, region string) ([]provider.InstanceType, error) {
	return []provider.InstanceType{
		{Name: "g5.xlarge", GPUCount: 1, MemoryGB: 16},
		{Name: "g4dn.xlarge", GPUCount: 1, MemoryGB: 16},
		{Name: "t3.medium", MemoryGB: 4},
	}, nil
}

func (p *Provider) ListBaseImages(ctx context.Context, region string) ([]provider.BaseImage, error) {
	return []provider.BaseImage{
		{Name: "ubuntu-24.04", ID: "ubuntu-24.04"},
		{Name: "amazon-linux-2023", ID: "amazon-linux-2023"},
	}, nil
}

func (p *Provider) CreateInstance(ctx context.Context, req provider.CreateInstanceRequest) (*provider.Instance, error) {
	return nil, errors.New("aws instance creation not implemented yet")
}

func (p *Provider) DeleteInstance(ctx context.Context, instanceID string) error {
	return errors.New("aws instance deletion not implemented yet")
}

const (
	authStageConfig      = "config"
	authStageCredentials = "credentials"
	authStageAPI         = "api"
)

type AuthError struct {
	Kind    string
	Profile string
	Stage   string
	Cause   error
}

func (e *AuthError) Error() string {
	if e == nil {
		return ""
	}
	return e.message()
}

func (e *AuthError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func (e *AuthError) message() string {
	switch e.Kind {
	case "profile_not_found":
		if e.Profile != "" {
			return fmt.Sprintf("AWS profile %q was not found; pass a valid --profile or configure AWS_PROFILE", e.Profile)
		}
		return "AWS profile was not found; pass a valid --profile or configure AWS_PROFILE"
	case "no_credentials":
		return "AWS credentials are not configured; set environment credentials, configure an AWS profile, or run aws sso login"
	case "permission_denied":
		return "AWS credentials were found, but sts:GetCallerIdentity was denied; check IAM permissions for the selected profile"
	case "api_call_failed":
		return "AWS auth check failed while calling sts:GetCallerIdentity; verify credentials, network access, and the selected profile"
	default:
		if e.Cause != nil {
			return e.Cause.Error()
		}
		return "AWS auth check failed"
	}
}

func (p *Provider) loadAWSConfig(ctx context.Context) (awsbase.Config, error) {
	p.ensureDeps()
	optFns := make([]func(*awsconfig.LoadOptions) error, 0, 1)
	if profile := strings.TrimSpace(p.Config.Profile); profile != "" {
		optFns = append(optFns, awsconfig.WithSharedConfigProfile(profile))
	}
	cfg, err := p.loadDefaultConfig(ctx, optFns...)
	if err != nil {
		return awsbase.Config{}, classifyAuthError(err, p.Config.Profile, authStageConfig)
	}
	if strings.TrimSpace(cfg.Region) == "" {
		cfg.Region = "us-east-1"
	}
	return cfg, nil
}

func classifyAuthError(err error, profile, stage string) error {
	if err == nil {
		return nil
	}

	lower := strings.ToLower(err.Error())
	switch {
	case stage == authStageConfig && (strings.Contains(lower, "shared config profile") || (strings.Contains(lower, "profile") && strings.Contains(lower, "not found"))):
		return &AuthError{Kind: "profile_not_found", Profile: profile, Stage: stage, Cause: err}
	case strings.Contains(lower, "no credential providers") ||
		strings.Contains(lower, "no valid providers") ||
		strings.Contains(lower, "failed to refresh cached credentials") ||
		strings.Contains(lower, "no ec2 imds role found"):
		return &AuthError{Kind: "no_credentials", Profile: profile, Stage: stage, Cause: err}
	}

	if isPermissionDenied(err) {
		return &AuthError{Kind: "permission_denied", Profile: profile, Stage: stage, Cause: err}
	}
	return &AuthError{Kind: "api_call_failed", Profile: profile, Stage: stage, Cause: err}
}

func isPermissionDenied(err error) bool {
	var responseErr *smithyhttp.ResponseError
	if errors.As(err, &responseErr) && responseErr.Response != nil {
		switch responseErr.Response.StatusCode {
		case http.StatusForbidden, http.StatusUnauthorized:
			return true
		}
	}

	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch strings.ToLower(apiErr.ErrorCode()) {
		case "accessdenied", "accessdeniedexception", "unauthorizedoperation", "invalidclienttokenid", "unrecognizedclientexception", "signaturedoesnotmatch":
			return true
		}
	}

	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "access denied") || strings.Contains(lower, "not authorized") || strings.Contains(lower, "unauthorized")
}

func awsString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (p *Provider) ensureDeps() {
	if p.loadDefaultConfig == nil {
		p.loadDefaultConfig = awsconfig.LoadDefaultConfig
	}
	if p.newSTSClient == nil {
		p.newSTSClient = func(cfg awsbase.Config) stsClient { return sts.NewFromConfig(cfg) }
	}
}
