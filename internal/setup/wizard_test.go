package setup

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"openclaw/internal/config"
	"openclaw/internal/prompt"
	"openclaw/internal/provider"
	awsprovider "openclaw/internal/provider/aws"
)

type fakeProvider struct {
	regions          []string
	report           provider.GPUQuotaReport
	quotaErr         error
	regionsErr       error
	instanceTypesErr error
}

func (f fakeProvider) AuthCheck(ctx context.Context) (provider.AuthStatus, error) {
	return provider.AuthStatus{}, nil
}
func (f fakeProvider) CheckAuth(ctx context.Context) (provider.AuthStatus, error) {
	return f.AuthCheck(ctx)
}
func (f fakeProvider) ListRegions(ctx context.Context) ([]string, error) {
	if f.regionsErr != nil {
		return nil, f.regionsErr
	}
	return f.regions, nil
}
func (f fakeProvider) CheckGPUQuota(ctx context.Context, region, instanceFamily string) (provider.GPUQuotaReport, error) {
	if f.quotaErr != nil {
		return provider.GPUQuotaReport{}, f.quotaErr
	}
	return f.report, nil
}
func (f fakeProvider) ListInstanceTypes(ctx context.Context, region string) ([]provider.InstanceType, error) {
	if f.instanceTypesErr != nil {
		return nil, f.instanceTypesErr
	}
	return []provider.InstanceType{{Name: "t3.medium"}, {Name: "g5.xlarge"}}, nil
}
func (f fakeProvider) RecommendInstanceTypes(ctx context.Context, region, computeClass string) ([]provider.InstanceType, error) {
	if f.instanceTypesErr != nil {
		return nil, f.instanceTypesErr
	}
	if config.EffectiveComputeClass(computeClass) == config.ComputeClassCPU {
		return []provider.InstanceType{{Name: "t3.xlarge"}, {Name: "t3.2xlarge"}}, nil
	}
	return []provider.InstanceType{{Name: "g5.xlarge"}, {Name: "g4dn.xlarge"}}, nil
}
func (f fakeProvider) ListBaseImages(ctx context.Context, region string) ([]provider.BaseImage, error) {
	return []provider.BaseImage{{
		Name:               "AWS Deep Learning AMI GPU Ubuntu 22.04",
		ID:                 "ami-0123456789abcdef0",
		Architecture:       "x86_64",
		Owner:              "amazon",
		VirtualizationType: "hvm",
		RootDeviceType:     "ebs",
		Region:             region,
		Source:             "mock",
		SSMParameter:       "/aws/service/deeplearning/ami/x86_64/base-oss-nvidia-driver-gpu-ubuntu-22.04/latest/ami-id",
	}}, nil
}
func (f fakeProvider) RecommendBaseImages(ctx context.Context, region, computeClass string) ([]provider.BaseImage, error) {
	if config.EffectiveComputeClass(computeClass) == config.ComputeClassCPU {
		return []provider.BaseImage{{
			Name:               "Ubuntu 22.04 LTS",
			ID:                 "ami-0ubuntu1234567890",
			Architecture:       "x86_64",
			Owner:              "canonical",
			VirtualizationType: "hvm",
			RootDeviceType:     "ebs",
			Region:             region,
			Source:             "mock",
			SSMParameter:       "/aws/service/canonical/ubuntu/server/22.04/stable/current/amd64/hvm/ebs-gp2/ami-id",
		}}, nil
	}
	return f.ListBaseImages(ctx, region)
}
func (f fakeProvider) GetInstance(ctx context.Context, region, instanceID string) (*provider.Instance, error) {
	return nil, errors.New("not implemented")
}
func (f fakeProvider) CreateInstance(ctx context.Context, req provider.CreateInstanceRequest) (*provider.Instance, error) {
	return nil, errors.New("not implemented")
}
func (f fakeProvider) DeleteInstance(ctx context.Context, instanceID string) error { return nil }

func TestWizardWarnsAndContinuesWhenQuotaInsufficient(t *testing.T) {
	input := strings.Join([]string{
		"1", // platform aws
		"",  // accept default GPU compute mode
		"1", // region
		"y", // continue despite quota warning
		"",  // accept default instance type (g5.xlarge)
		"1", // base image
		"20",
		"1",
		"y",
		"1",
		"http://localhost:11434",
		"llama3.2",
		"y",
	}, "\n") + "\n"

	quotaUsage := 1
	wizard := NewWizard(
		prompt.NewSession(strings.NewReader(input), &bytes.Buffer{}),
		&bytes.Buffer{},
		func(platform, computeClass string) provider.CloudProvider {
			return fakeProvider{
				regions: []string{"us-east-1", "us-west-2"},
				report: provider.GPUQuotaReport{
					Region:         "us-east-1",
					InstanceFamily: "g5",
					Checks: []provider.GPUQuotaCheck{{
						QuotaName:          "Running On-Demand G and VT instances",
						CurrentLimit:       0,
						CurrentUsage:       &quotaUsage,
						EstimatedRemaining: 0,
						UsageIsEstimated:   true,
					}},
					LikelyCreatable: false,
					Notes:           []string{"request more quota"},
				},
			}
		},
		&config.Config{Region: config.RegionConfig{Name: "us-west-2"}},
	)
	wizard.AWSProfile = "sso-dev"

	cfg, err := wizard.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if cfg.Region.Name != "us-east-1" {
		t.Fatalf("Region.Name = %q, want us-east-1", cfg.Region.Name)
	}
}

func TestWizardFallsBackToBundledLookupsWhenAWSDataIsUnavailable(t *testing.T) {
	input := strings.Join([]string{
		"1", // platform aws
		"",  // accept default GPU compute mode
		"1", // fallback region us-east-1
		"",  // accept fallback instance type
		"1", // base image
		"20",
		"1",
		"y",
		"1",
		"http://localhost:11434",
		"llama3.2",
		"y",
	}, "\n") + "\n"

	out := &bytes.Buffer{}
	wizard := NewWizard(
		prompt.NewSession(strings.NewReader(input), out),
		out,
		func(platform, computeClass string) provider.CloudProvider {
			return fakeProvider{
				regionsErr:       errors.New("access denied"),
				instanceTypesErr: errors.New("timeout"),
				quotaErr:         errors.New("quota unavailable"),
			}
		},
		&config.Config{},
	)
	wizard.AWSProfile = "sso-dev"

	cfg, err := wizard.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if cfg.Region.Name != "us-east-1" {
		t.Fatalf("Region.Name = %q, want us-east-1", cfg.Region.Name)
	}
	if cfg.Instance.Type != "g5.xlarge" {
		t.Fatalf("Instance.Type = %q, want g5.xlarge", cfg.Instance.Type)
	}
	got := out.String()
	for _, fragment := range []string{
		"Warning: AWS region lookup unavailable; using bundled fallback regions.",
		"Warning: AWS instance type lookup unavailable; using bundled fallback instance types.",
	} {
		if !strings.Contains(got, fragment) {
			t.Fatalf("output = %q, want %q", got, fragment)
		}
	}
}

func TestWizardWarnsAndContinuesWhenQuotaCheckUnavailable(t *testing.T) {
	input := strings.Join([]string{
		"1", // platform aws
		"",  // accept default GPU compute mode
		"1", // region
		"",  // accept default instance type
		"1", // base image
		"20",
		"1",
		"y",
		"1",
		"http://localhost:11434",
		"llama3.2",
		"y",
	}, "\n") + "\n"

	out := &bytes.Buffer{}
	wizard := NewWizard(
		prompt.NewSession(strings.NewReader(input), out),
		out,
		func(platform, computeClass string) provider.CloudProvider {
			return fakeProvider{
				regions:  []string{"us-east-1", "us-west-2"},
				quotaErr: errors.New("security token invalid"),
			}
		},
		&config.Config{},
	)
	wizard.AWSProfile = "sso-dev"

	cfg, err := wizard.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if cfg.Region.Name != "us-east-1" {
		t.Fatalf("Region.Name = %q, want us-east-1", cfg.Region.Name)
	}
	if got := out.String(); !strings.Contains(got, "Warning: GPU quota check unavailable; continuing.") {
		t.Fatalf("output = %q, want quota warning", got)
	}
}

func TestWizardFallsBackToBundledImagesWhenSSMIsUnavailable(t *testing.T) {
	input := strings.Join([]string{
		"1", // platform aws
		"",  // accept default GPU compute mode
		"1", // region
		"",  // accept default instance type
		"1", // bundled fallback image
		"20",
		"1",
		"y",
		"1",
		"http://localhost:11434",
		"llama3.2",
		"y",
	}, "\n") + "\n"

	out := &bytes.Buffer{}
	wizard := NewWizard(
		prompt.NewSession(strings.NewReader(input), out),
		out,
		func(platform, computeClass string) provider.CloudProvider {
			return fakeProvider{
				regions: []string{"us-east-1", "us-west-2"},
				report: provider.GPUQuotaReport{
					Region:          "us-east-1",
					InstanceFamily:  "g5",
					LikelyCreatable: true,
				},
			}
		},
		&config.Config{},
	)
	wizard.AWSProfile = "sso-dev"
	wizard.Provider = failingImageProvider{fakeProvider: fakeProvider{
		regions: []string{"us-east-1", "us-west-2"},
		report: provider.GPUQuotaReport{
			Region:          "us-east-1",
			InstanceFamily:  "g5",
			LikelyCreatable: true,
		},
	}}

	cfg, err := wizard.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if cfg.Image.Name != "AWS Deep Learning AMI GPU Ubuntu 22.04" {
		t.Fatalf("Image.Name = %q, want bundled fallback image", cfg.Image.Name)
	}
	if got := out.String(); !strings.Contains(got, "Warning: AWS image lookup unavailable; using bundled fallback images.") {
		t.Fatalf("output = %q, want image lookup warning", got)
	}
}

func TestWizardFallsBackToBundledImagesWhenImageLookupFails(t *testing.T) {
	input := strings.Join([]string{
		"1", // platform aws
		"",  // accept default GPU compute mode
		"1", // region
		"",  // accept default instance type
		"1", // bundled fallback image
		"20",
		"1",
		"y",
		"1",
		"http://localhost:11434",
		"llama3.2",
		"y",
	}, "\n") + "\n"

	out := &bytes.Buffer{}
	wizard := NewWizard(
		prompt.NewSession(strings.NewReader(input), out),
		out,
		func(platform, computeClass string) provider.CloudProvider {
			return fakeProvider{
				regions: []string{"us-east-1", "us-west-2"},
				report: provider.GPUQuotaReport{
					Region:          "us-east-1",
					InstanceFamily:  "g5",
					LikelyCreatable: true,
				},
			}
		},
		&config.Config{},
	)
	wizard.AWSProfile = "sso-dev"
	wizard.Provider = genericFailingImageProvider{fakeProvider: fakeProvider{
		regions: []string{"us-east-1", "us-west-2"},
		report: provider.GPUQuotaReport{
			Region:          "us-east-1",
			InstanceFamily:  "g5",
			LikelyCreatable: true,
		},
	}}

	cfg, err := wizard.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if cfg.Image.Name != "AWS Deep Learning AMI GPU Ubuntu 22.04" {
		t.Fatalf("Image.Name = %q, want bundled fallback image", cfg.Image.Name)
	}
	if got := out.String(); !strings.Contains(got, "Warning: AWS image lookup unavailable; using bundled fallback images.") {
		t.Fatalf("output = %q, want image lookup warning", got)
	}
}

type failingImageProvider struct {
	fakeProvider
}

func (failingImageProvider) ListBaseImages(ctx context.Context, region string) ([]provider.BaseImage, error) {
	return nil, &awsprovider.AuthError{
		Kind:    "api_call_failed",
		Profile: "test-profile",
		Stage:   "api",
		Cause:   errors.New("security token invalid"),
	}
}
func (failingImageProvider) RecommendBaseImages(ctx context.Context, region, computeClass string) ([]provider.BaseImage, error) {
	return nil, &awsprovider.AuthError{
		Kind:    "api_call_failed",
		Profile: "test-profile",
		Stage:   "api",
		Cause:   errors.New("security token invalid"),
	}
}

type genericFailingImageProvider struct {
	fakeProvider
}

func (genericFailingImageProvider) ListBaseImages(ctx context.Context, region string) ([]provider.BaseImage, error) {
	return nil, errors.New("dial tcp: lookup ssm.ap-northeast-1.amazonaws.com: no such host")
}
func (genericFailingImageProvider) RecommendBaseImages(ctx context.Context, region, computeClass string) ([]provider.BaseImage, error) {
	return nil, errors.New("dial tcp: lookup ssm.ap-northeast-1.amazonaws.com: no such host")
}
