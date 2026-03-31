package provider

import "context"

type CloudProvider interface {
	CheckAuth(ctx context.Context) (AuthStatus, error)
	ListRegions(ctx context.Context) ([]string, error)
	CheckGPUQuota(ctx context.Context, region, instanceFamily string) (GPUQuotaReport, error)
	RecommendInstanceTypes(ctx context.Context, region, computeClass string) ([]InstanceType, error)
	RecommendBaseImages(ctx context.Context, region, computeClass string) ([]BaseImage, error)
	GetInstance(ctx context.Context, region, instanceID string) (*Instance, error)
}

type CloudAdvisor = CloudProvider

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
	Region           string
	InstanceType     string
	Image            string
	ImageName        string
	DiskSizeGB       int
	NetworkMode      string
	ConnectionMethod string
	SSHKeyName       string
	SSHCIDR          string
	UseNemoClaw      bool
	NIMEndpoint      string
	Model            string
}

type Instance struct {
	ID                 string
	Name               string
	Region             string
	PublicIP           string
	PrivateIP          string
	ConnectionInfo     string
	SecurityGroupID    string
	SecurityGroupRules []string
}

type InstanceSpec = CreateInstanceRequest

type CreatedInstance = Instance
