package aws

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	awsbase "github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/servicequotas"
	sqtypes "github.com/aws/aws-sdk-go-v2/service/servicequotas/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"openclaw/internal/config"
	"openclaw/internal/provider"
)

type Config struct {
	Profile      string
	ComputeClass string
}

type Provider struct {
	Config Config

	loadDefaultConfig func(ctx context.Context, optFns ...func(*awsconfig.LoadOptions) error) (awsbase.Config, error)
	newSSMClient      func(cfg awsbase.Config) ssmClient
	newSTSClient      func(cfg awsbase.Config) stsClient
	newSQClient       func(cfg awsbase.Config) serviceQuotasClient
	newEC2Client      func(cfg awsbase.Config) ec2Client
}

type ssmClient interface {
	GetParameter(ctx context.Context, params *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
}

type stsClient interface {
	GetCallerIdentity(ctx context.Context, params *sts.GetCallerIdentityInput, optFns ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error)
}

type serviceQuotasClient interface {
	StartQuotaUtilizationReport(ctx context.Context, params *servicequotas.StartQuotaUtilizationReportInput, optFns ...func(*servicequotas.Options)) (*servicequotas.StartQuotaUtilizationReportOutput, error)
	GetQuotaUtilizationReport(ctx context.Context, params *servicequotas.GetQuotaUtilizationReportInput, optFns ...func(*servicequotas.Options)) (*servicequotas.GetQuotaUtilizationReportOutput, error)
}

type ec2Client interface {
	DescribeInstances(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
}

const (
	QuotaSourceMock            = "mock"
	QuotaSourceServiceQuotas   = "aws-service-quotas"
	serviceCodeEC2             = "ec2"
	quotaReportPollInterval    = 2 * time.Second
	quotaReportPollAttempts    = 5
	defaultQuotaReportPageSize = int32(1000)
)

var gpuQuotaFamilyNames = map[string][]string{
	"g5":   {"Running On-Demand G and VT instances", "All G and VT Spot Instance Requests"},
	"g4dn": {"Running On-Demand G and VT instances", "All G and VT Spot Instance Requests"},
	"g4ad": {"Running On-Demand G and VT instances", "All G and VT Spot Instance Requests"},
	"g6":   {"Running On-Demand G and VT instances", "All G and VT Spot Instance Requests"},
}

func New(cfg Config) *Provider {
	return &Provider{
		Config:            cfg,
		loadDefaultConfig: awsconfig.LoadDefaultConfig,
		newSSMClient:      func(cfg awsbase.Config) ssmClient { return ssm.NewFromConfig(cfg) },
		newSTSClient:      func(cfg awsbase.Config) stsClient { return sts.NewFromConfig(cfg) },
		newSQClient:       func(cfg awsbase.Config) serviceQuotasClient { return servicequotas.NewFromConfig(cfg) },
		newEC2Client:      func(cfg awsbase.Config) ec2Client { return ec2.NewFromConfig(cfg) },
	}
}

var _ provider.CloudProvider = (*Provider)(nil)

func (p *Provider) CheckAuth(ctx context.Context) (provider.AuthStatus, error) {
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

func (p *Provider) AuthCheck(ctx context.Context) (provider.AuthStatus, error) {
	return p.CheckAuth(ctx)
}

func (p *Provider) ListRegions(ctx context.Context) ([]string, error) {
	return []string{"ap-northeast-1", "us-east-1", "us-west-2"}, nil
}

func (p *Provider) CheckGPUQuota(ctx context.Context, region, instanceFamily string) (provider.GPUQuotaReport, error) {
	region = strings.TrimSpace(region)
	if region == "" {
		return provider.GPUQuotaReport{}, errors.New("region is required")
	}

	family := strings.ToLower(strings.TrimSpace(instanceFamily))
	if family == "" {
		family = "g5"
	}

	quotaNames, ok := gpuQuotaFamilyNames[family]
	if !ok {
		return provider.GPUQuotaReport{}, fmt.Errorf("unsupported GPU family %q", instanceFamily)
	}

	cfg, err := p.loadAWSConfig(ctx)
	if err != nil {
		return provider.GPUQuotaReport{}, err
	}
	cfg.Region = region

	client := p.newSQClient(cfg)
	reportID, err := p.startQuotaUtilizationReport(ctx, client)
	if err != nil {
		return provider.GPUQuotaReport{}, err
	}

	utilization, err := p.waitForQuotaUtilizationReport(ctx, client, reportID)
	if err != nil {
		return provider.GPUQuotaReport{}, err
	}

	return buildQuotaReport(region, family, quotaNames, utilization), nil
}

func (p *Provider) RecommendInstanceTypes(ctx context.Context, region, computeClass string) ([]provider.InstanceType, error) {
	switch config.EffectiveComputeClass(computeClass) {
	case config.ComputeClassCPU:
		return []provider.InstanceType{
			{Name: "t3.xlarge", MemoryGB: 16},
			{Name: "t3.2xlarge", MemoryGB: 32},
			{Name: "t3.medium", MemoryGB: 4},
		}, nil
	default:
		return []provider.InstanceType{
			{Name: "g5.xlarge", GPUCount: 1, MemoryGB: 16},
			{Name: "g4dn.xlarge", GPUCount: 1, MemoryGB: 16},
			{Name: "g6.xlarge", GPUCount: 1, MemoryGB: 16},
		}, nil
	}
}

func (p *Provider) ListInstanceTypes(ctx context.Context, region string) ([]provider.InstanceType, error) {
	return p.RecommendInstanceTypes(ctx, region, p.Config.ComputeClass)
}

func (p *Provider) RecommendBaseImages(ctx context.Context, region, computeClass string) ([]provider.BaseImage, error) {
	region = strings.TrimSpace(region)
	if region == "" {
		return nil, errors.New("region is required")
	}

	cfg, err := p.loadAWSConfig(ctx)
	if err != nil {
		return nil, err
	}
	cfg.Region = region

	switch config.EffectiveComputeClass(computeClass) {
	case config.ComputeClassCPU:
		image, err := p.resolveUbuntu2204(ctx, cfg)
		if err != nil {
			return nil, err
		}
		return []provider.BaseImage{image}, nil
	default:
		image, err := p.resolveDLAMIGPUUbuntu2204(ctx, cfg)
		if err != nil {
			return nil, err
		}
		return []provider.BaseImage{image}, nil
	}
}

func (p *Provider) ListBaseImages(ctx context.Context, region string) ([]provider.BaseImage, error) {
	return p.RecommendBaseImages(ctx, region, p.Config.ComputeClass)
}

func (p *Provider) GetInstance(ctx context.Context, region, instanceID string) (*provider.Instance, error) {
	region = strings.TrimSpace(region)
	instanceID = strings.TrimSpace(instanceID)
	if region == "" {
		return nil, errors.New("region is required")
	}
	if instanceID == "" {
		return nil, errors.New("instance id is required")
	}

	cfg, err := p.loadAWSConfig(ctx)
	if err != nil {
		return nil, err
	}
	cfg.Region = region

	client := p.newEC2Client(cfg)
	describeOut, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{instanceID}})
	if err != nil {
		return nil, fmt.Errorf("describe EC2 instance %s: %w", instanceID, err)
	}
	ec2Instance := findInstance(describeOut, instanceID)
	if ec2Instance == nil {
		return nil, fmt.Errorf("describe EC2 instance %s: instance not found", instanceID)
	}

	publicIP := awsString(ec2Instance.PublicIpAddress)
	privateIP := awsString(ec2Instance.PrivateIpAddress)
	connectionInfo := "connection details unavailable"
	if publicIP != "" {
		connectionInfo = fmt.Sprintf("public IP: %s", publicIP)
	} else if privateIP != "" {
		connectionInfo = fmt.Sprintf("private IP: %s", privateIP)
	}

	return &provider.Instance{
		ID:             instanceID,
		Name:           instanceID,
		Region:         region,
		PublicIP:       publicIP,
		PrivateIP:      privateIP,
		ConnectionInfo: connectionInfo,
	}, nil
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
	if p.newSSMClient == nil {
		p.newSSMClient = func(cfg awsbase.Config) ssmClient { return ssm.NewFromConfig(cfg) }
	}
	if p.newSTSClient == nil {
		p.newSTSClient = func(cfg awsbase.Config) stsClient { return sts.NewFromConfig(cfg) }
	}
	if p.newSQClient == nil {
		p.newSQClient = func(cfg awsbase.Config) serviceQuotasClient { return servicequotas.NewFromConfig(cfg) }
	}
	if p.newEC2Client == nil {
		p.newEC2Client = func(cfg awsbase.Config) ec2Client { return ec2.NewFromConfig(cfg) }
	}
}

func (p *Provider) startQuotaUtilizationReport(ctx context.Context, client serviceQuotasClient) (string, error) {
	out, err := client.StartQuotaUtilizationReport(ctx, &servicequotas.StartQuotaUtilizationReportInput{})
	if err != nil {
		return "", fmt.Errorf("start Service Quotas utilization report: %w", err)
	}
	if out == nil || strings.TrimSpace(awsString(out.ReportId)) == "" {
		return "", errors.New("start Service Quotas utilization report: missing report id")
	}
	return awsString(out.ReportId), nil
}

func (p *Provider) waitForQuotaUtilizationReport(ctx context.Context, client serviceQuotasClient, reportID string) ([]sqtypes.QuotaUtilizationInfo, error) {
	var lastErr error
	for attempt := 0; attempt < quotaReportPollAttempts; attempt++ {
		out, err := client.GetQuotaUtilizationReport(ctx, &servicequotas.GetQuotaUtilizationReportInput{
			ReportId:   awsbase.String(reportID),
			MaxResults: awsbase.Int32(defaultQuotaReportPageSize),
		})
		if err != nil {
			return nil, fmt.Errorf("get Service Quotas utilization report: %w", err)
		}
		if out == nil {
			return nil, errors.New("get Service Quotas utilization report: empty response")
		}

		switch out.Status {
		case sqtypes.ReportStatusCompleted:
			return p.collectQuotaUtilizationPages(ctx, client, reportID, out)
		case sqtypes.ReportStatusFailed:
			if out.ErrorMessage != nil && strings.TrimSpace(*out.ErrorMessage) != "" {
				return nil, fmt.Errorf("service quotas utilization report failed: %s", *out.ErrorMessage)
			}
			return nil, errors.New("service quotas utilization report failed")
		case sqtypes.ReportStatusPending, sqtypes.ReportStatusInProgress:
			lastErr = fmt.Errorf("service quotas utilization report status: %s", out.Status)
		default:
			lastErr = fmt.Errorf("service quotas utilization report status: %s", out.Status)
		}

		if attempt < quotaReportPollAttempts-1 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(quotaReportPollInterval):
			}
		}
	}

	if lastErr == nil {
		lastErr = errors.New("service quotas utilization report not ready")
	}
	return nil, lastErr
}

func (p *Provider) collectQuotaUtilizationPages(ctx context.Context, client serviceQuotasClient, reportID string, first *servicequotas.GetQuotaUtilizationReportOutput) ([]sqtypes.QuotaUtilizationInfo, error) {
	var quotas []sqtypes.QuotaUtilizationInfo
	appendQuotas := func(items []sqtypes.QuotaUtilizationInfo) {
		quotas = append(quotas, items...)
	}

	if first != nil {
		appendQuotas(first.Quotas)
	}
	nextToken := awsString(first.NextToken)
	for strings.TrimSpace(nextToken) != "" {
		out, err := client.GetQuotaUtilizationReport(ctx, &servicequotas.GetQuotaUtilizationReportInput{
			ReportId:   awsbase.String(reportID),
			NextToken:  awsbase.String(nextToken),
			MaxResults: awsbase.Int32(defaultQuotaReportPageSize),
		})
		if err != nil {
			return nil, fmt.Errorf("get Service Quotas utilization report page: %w", err)
		}
		if out == nil {
			return nil, errors.New("get Service Quotas utilization report page: empty response")
		}
		appendQuotas(out.Quotas)
		nextToken = awsString(out.NextToken)
	}

	return quotas, nil
}

func buildQuotaReport(region, family string, targetQuotaNames []string, utilization []sqtypes.QuotaUtilizationInfo) provider.GPUQuotaReport {
	records := make(map[string]sqtypes.QuotaUtilizationInfo, len(utilization))
	for _, item := range utilization {
		if strings.TrimSpace(awsString(item.ServiceCode)) != serviceCodeEC2 {
			continue
		}
		name := strings.TrimSpace(awsString(item.QuotaName))
		if name == "" {
			continue
		}
		records[name] = item
	}

	notes := make([]string, 0, 2)
	checks := make([]provider.GPUQuotaCheck, 0, len(targetQuotaNames))
	likelyCreatable := false

	for _, quotaName := range targetQuotaNames {
		item, ok := records[quotaName]
		if !ok {
			notes = append(notes, fmt.Sprintf("Service Quotas utilization report did not include %q.", quotaName))
			continue
		}

		check := provider.GPUQuotaCheck{QuotaName: quotaName}
		limit, limitAvailable := firstAvailableQuotaValue(item.AppliedValue, item.DefaultValue)
		if limitAvailable {
			check.CurrentLimit = quotaValueToInt(limit)
		}

		if item.Utilization != nil && limitAvailable {
			usage := (limit * *item.Utilization) / 100
			usageValue := quotaValueToInt(usage)
			check.CurrentUsage = &usageValue
			check.UsageIsEstimated = false
			check.EstimatedRemaining = maxInt(check.CurrentLimit-usageValue, 0)
		} else if limitAvailable {
			check.UsageIsEstimated = true
			check.EstimatedRemaining = check.CurrentLimit
		} else {
			check.UsageIsEstimated = true
			notes = append(notes, fmt.Sprintf("Quota limit for %q was not available in the utilization report.", quotaName))
		}

		if check.EstimatedRemaining > 0 {
			likelyCreatable = true
		}
		checks = append(checks, check)
	}

	if len(checks) == 0 {
		notes = append(notes, "No matching EC2 GPU quota records were found in the Service Quotas utilization report.")
	}
	if likelyCreatable {
		notes = append(notes, "At least one relevant EC2 GPU quota has remaining headroom.")
	} else if len(checks) > 0 {
		notes = append(notes, "The relevant EC2 GPU quotas appear exhausted or unavailable.")
	}

	return provider.GPUQuotaReport{
		Source:          QuotaSourceServiceQuotas,
		Region:          region,
		InstanceFamily:  family,
		Checks:          checks,
		LikelyCreatable: likelyCreatable,
		Notes:           notes,
	}
}

func firstAvailableQuotaValue(values ...*float64) (float64, bool) {
	for _, value := range values {
		if value != nil {
			return *value, true
		}
	}
	return 0, false
}

func quotaValueToInt(value float64) int {
	return int(math.Round(value))
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (p *Provider) resolveDLAMIGPUUbuntu2204(ctx context.Context, cfg awsbase.Config) (provider.BaseImage, error) {
	client := p.newSSMClient(cfg)
	const parameterName = "/aws/service/deeplearning/ami/x86_64/base-oss-nvidia-driver-gpu-ubuntu-22.04/latest/ami-id"
	out, err := client.GetParameter(ctx, &ssm.GetParameterInput{Name: awsbase.String(parameterName)})
	if err != nil {
		if isPermissionDenied(err) {
			return provider.BaseImage{}, &AuthError{
				Kind:    "permission_denied",
				Profile: p.Config.Profile,
				Stage:   authStageAPI,
				Cause:   fmt.Errorf("resolve Deep Learning AMI GPU Ubuntu 22.04 for region %s: %w", cfg.Region, err),
			}
		}
		return provider.BaseImage{}, fmt.Errorf("resolve Deep Learning AMI GPU Ubuntu 22.04 for region %s: %w", cfg.Region, err)
	}
	if out == nil || out.Parameter == nil || strings.TrimSpace(awsString(out.Parameter.Value)) == "" {
		return provider.BaseImage{}, fmt.Errorf("resolve Deep Learning AMI GPU Ubuntu 22.04 for region %s: empty SSM parameter %s", cfg.Region, parameterName)
	}
	return provider.BaseImage{
		Name:               "AWS Deep Learning AMI GPU Ubuntu 22.04",
		ID:                 awsString(out.Parameter.Value),
		Description:        "Deep Learning Base OSS Nvidia Driver GPU AMI (Ubuntu 22.04)",
		Architecture:       "x86_64",
		Owner:              "amazon",
		VirtualizationType: "hvm",
		RootDeviceType:     "ebs",
		Region:             cfg.Region,
		Source:             "aws-ssm-public-parameter",
		SSMParameter:       parameterName,
	}, nil
}

func (p *Provider) resolveUbuntu2204(ctx context.Context, cfg awsbase.Config) (provider.BaseImage, error) {
	client := p.newSSMClient(cfg)
	const parameterName = "/aws/service/canonical/ubuntu/server/22.04/stable/current/amd64/hvm/ebs-gp2/ami-id"
	out, err := client.GetParameter(ctx, &ssm.GetParameterInput{Name: awsbase.String(parameterName)})
	if err != nil {
		if isPermissionDenied(err) {
			return provider.BaseImage{}, &AuthError{
				Kind:    "permission_denied",
				Profile: p.Config.Profile,
				Stage:   authStageAPI,
				Cause:   fmt.Errorf("resolve Ubuntu 22.04 LTS for region %s: %w", cfg.Region, err),
			}
		}
		return provider.BaseImage{}, fmt.Errorf("resolve Ubuntu 22.04 LTS for region %s: %w", cfg.Region, err)
	}
	if out == nil || out.Parameter == nil || strings.TrimSpace(awsString(out.Parameter.Value)) == "" {
		return provider.BaseImage{}, fmt.Errorf("resolve Ubuntu 22.04 LTS for region %s: empty SSM parameter %s", cfg.Region, parameterName)
	}
	return provider.BaseImage{
		Name:               "Ubuntu 22.04 LTS",
		ID:                 awsString(out.Parameter.Value),
		Description:        "Ubuntu Server 22.04 LTS",
		Architecture:       "x86_64",
		Owner:              "canonical",
		VirtualizationType: "hvm",
		RootDeviceType:     "ebs",
		Region:             cfg.Region,
		Source:             "canonical-ssm-public-parameter",
		SSMParameter:       parameterName,
	}, nil
}
