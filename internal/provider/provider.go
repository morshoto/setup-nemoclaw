package provider

import "context"

type CloudProvider interface {
	AuthCheck(ctx context.Context) (AuthStatus, error)
	ListRegions(ctx context.Context) ([]string, error)
	CheckGPUQuota(ctx context.Context, region, instanceFamily string) (GPUQuotaReport, error)
	ListInstanceTypes(ctx context.Context, region string) ([]InstanceType, error)
	ListBaseImages(ctx context.Context, region string) ([]BaseImage, error)
	CreateInstance(ctx context.Context, req CreateInstanceRequest) (*Instance, error)
	DeleteInstance(ctx context.Context, instanceID string) error
}

type AuthStatus struct {
	Profile string
	Account string
	Arn     string
	UserID  string
}

type GPUQuotaReport struct {
	Source          string
	Region          string
	InstanceFamily  string
	Checks          []GPUQuotaCheck
	LikelyCreatable bool
	Notes           []string
}

type GPUQuotaCheck struct {
	QuotaName          string
	CurrentLimit       int
	CurrentUsage       *int
	EstimatedRemaining int
	UsageIsEstimated   bool
}

type InstanceType struct {
	Name     string
	GPUCount int
	MemoryGB int
}

type BaseImage struct {
	Name               string
	ID                 string
	Description        string
	Architecture       string
	Owner              string
	VirtualizationType string
	RootDeviceType     string
	Region             string
	Source             string
	SSMParameter       string
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
