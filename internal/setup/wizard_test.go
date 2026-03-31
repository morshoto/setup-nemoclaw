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
)

type fakeProvider struct {
	regions []string
	report  provider.GPUQuotaReport
}

func (f fakeProvider) AuthCheck(ctx context.Context) error { return nil }
func (f fakeProvider) ListRegions(ctx context.Context) ([]string, error) {
	return f.regions, nil
}
func (f fakeProvider) CheckGPUQuota(ctx context.Context, region, instanceFamily string) (provider.GPUQuotaReport, error) {
	return f.report, nil
}
func (f fakeProvider) ListInstanceTypes(ctx context.Context, region string) ([]provider.InstanceType, error) {
	return []provider.InstanceType{{Name: "t3.medium"}, {Name: "g5.xlarge"}}, nil
}
func (f fakeProvider) ListBaseImages(ctx context.Context, region string) ([]provider.BaseImage, error) {
	return []provider.BaseImage{{Name: "ubuntu-24.04"}}, nil
}
func (f fakeProvider) CreateInstance(ctx context.Context, req provider.CreateInstanceRequest) (*provider.Instance, error) {
	return nil, errors.New("not implemented")
}
func (f fakeProvider) DeleteInstance(ctx context.Context, instanceID string) error { return nil }

func TestWizardWarnsAndContinuesWhenQuotaInsufficient(t *testing.T) {
	input := strings.Join([]string{
		"1", // platform aws
		"1", // region
		"y", // continue despite quota warning
		"1", // instance type
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
		fakeProvider{
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
