package aws

import (
	"context"
	"errors"

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
	return []string{"us-east-1", "us-west-2"}, nil
}

func (p *Provider) CheckGPUQuota(ctx context.Context, region string) (provider.GPUQuota, error) {
	return provider.GPUQuota{}, errors.New("aws gpu quota check not implemented yet")
}

func (p *Provider) ListInstanceTypes(ctx context.Context, region string) ([]provider.InstanceType, error) {
	return []provider.InstanceType{
		{Name: "t3.medium", MemoryGB: 4},
		{Name: "g4dn.xlarge", GPUCount: 1, MemoryGB: 16},
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
