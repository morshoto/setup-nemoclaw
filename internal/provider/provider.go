package provider

import "context"

type CloudProvider interface {
	AuthCheck(ctx context.Context) error
	ListRegions(ctx context.Context) ([]string, error)
	CheckGPUQuota(ctx context.Context, region string) (GPUQuota, error)
	ListInstanceTypes(ctx context.Context, region string) ([]InstanceType, error)
	ListBaseImages(ctx context.Context, region string) ([]BaseImage, error)
	CreateInstance(ctx context.Context, req CreateInstanceRequest) (*Instance, error)
	DeleteInstance(ctx context.Context, instanceID string) error
}

type GPUQuota struct {
	Total     int
	Available int
}

type InstanceType struct {
	Name     string
	GPUCount int
	MemoryGB int
}

type BaseImage struct {
	Name string
	ID   string
}

type CreateInstanceRequest struct {
	Region       string
	InstanceType string
	Image        string
	DiskSizeGB   int
	NetworkMode  string
	UseNemoClaw  bool
	NIMEndpoint  string
	Model        string
}

type Instance struct {
	ID   string
	Name string
}
