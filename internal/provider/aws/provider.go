package aws

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"openclaw/internal/provider"
)

type Config struct {
	Profile string
}

type Provider struct {
	Config Config
}

func New(cfg Config) *Provider {
	return &Provider{Config: cfg}
}

var _ provider.CloudProvider = (*Provider)(nil)

func (p *Provider) AuthCheck(ctx context.Context) error {
	return errors.New("aws auth check not implemented yet")
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
		Region:         region,
		InstanceFamily: family,
		Checks: []provider.GPUQuotaCheck{
			{
				QuotaName:          "Running On-Demand G and VT instances",
				CurrentLimit:       1,
				CurrentUsage:       intPtr(0),
				EstimatedRemaining: 1,
				UsageIsEstimated:   true,
			},
			{
				QuotaName:          "All G and VT Spot Instance Requests",
				CurrentLimit:       2,
				CurrentUsage:       nil,
				EstimatedRemaining: 2,
				UsageIsEstimated:   true,
			},
		},
		LikelyCreatable: true,
		Notes: []string{
			"Estimated from static regional defaults because live Service Quotas access is not wired yet.",
			"Use the Service Quotas console to request more G and VT capacity if needed.",
		},
	}

	if region == "ap-northeast-1" {
		report.Checks[0].CurrentLimit = 0
		report.Checks[0].CurrentUsage = intPtr(0)
		report.Checks[0].EstimatedRemaining = 0
		report.Checks[1].CurrentLimit = 0
		report.Checks[1].EstimatedRemaining = 0
		report.LikelyCreatable = false
		report.Notes = append(report.Notes, "ap-northeast-1 is treated as quota-constrained in this skeleton implementation.")
	}

	return report, nil
}

func (p *Provider) ListInstanceTypes(ctx context.Context, region string) ([]provider.InstanceType, error) {
	return []provider.InstanceType{
		{Name: "t3.medium", MemoryGB: 4},
		{Name: "g4dn.xlarge", GPUCount: 1, MemoryGB: 16},
		{Name: "g5.xlarge", GPUCount: 1, MemoryGB: 16},
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

func intPtr(v int) *int {
	return &v
}
