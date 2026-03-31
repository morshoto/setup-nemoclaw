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
	regions  []string
	report   provider.GPUQuotaReport
	quotaErr error
}

func (f fakeProvider) AuthCheck(ctx context.Context) (provider.AuthStatus, error) {
	return provider.AuthStatus{}, nil
}
func (f fakeProvider) ListRegions(ctx context.Context) ([]string, error) {
	return f.regions, nil
}
func (f fakeProvider) CheckGPUQuota(ctx context.Context, region, instanceFamily string) (provider.GPUQuotaReport, error) {
	if f.quotaErr != nil {
		return provider.GPUQuotaReport{}, f.quotaErr
	}
	return f.report, nil
}
func (f fakeProvider) ListInstanceTypes(ctx context.Context, region string) ([]provider.InstanceType, error) {
	return []provider.InstanceType{{Name: "t3.medium"}, {Name: "g5.xlarge"}}, nil
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
func (f fakeProvider) CreateInstance(ctx context.Context, req provider.CreateInstanceRequest) (*provider.Instance, error) {
	return nil, errors.New("not implemented")
}
func (f fakeProvider) DeleteInstance(ctx context.Context, instanceID string) error { return nil }
func (f fakeProvider) GetInstance(ctx context.Context, region, instanceID string) (*provider.Instance, error) {
	return nil, errors.New("not implemented")
}

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

	cfg, err := wizard.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if cfg.Region.Name != "us-east-1" {
		t.Fatalf("Region.Name = %q, want us-east-1", cfg.Region.Name)
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

type genericFailingImageProvider struct {
	fakeProvider
}

func (genericFailingImageProvider) ListBaseImages(ctx context.Context, region string) ([]provider.BaseImage, error) {
	return nil, errors.New("dial tcp: lookup ssm.ap-northeast-1.amazonaws.com: no such host")
}
