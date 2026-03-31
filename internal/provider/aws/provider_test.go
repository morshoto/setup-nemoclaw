package aws

import (
	"context"
	"errors"
	"testing"

	awsbase "github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/servicequotas"
	sqtypes "github.com/aws/aws-sdk-go-v2/service/servicequotas/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/smithy-go"

	"openclaw/internal/config"
	"openclaw/internal/provider"
)

func TestAuthCheckReturnsProfileNotFound(t *testing.T) {
	p := &Provider{
		Config: Config{Profile: "missing"},
		loadDefaultConfig: func(ctx context.Context, optFns ...func(*awsconfig.LoadOptions) error) (awsbase.Config, error) {
			return awsbase.Config{}, errors.New("failed to load shared config profile, missing")
		},
	}

	_, err := p.AuthCheck(context.Background())
	if err == nil {
		t.Fatal("AuthCheck() error = nil")
	}
	authErr := mustAuthError(t, err)
	if authErr.Kind != "profile_not_found" {
		t.Fatalf("AuthError.Kind = %q, want profile_not_found", authErr.Kind)
	}
}

func TestAuthCheckReturnsNoCredentials(t *testing.T) {
	p := &Provider{
		Config: Config{Profile: "test-profile"},
		loadDefaultConfig: func(ctx context.Context, optFns ...func(*awsconfig.LoadOptions) error) (awsbase.Config, error) {
			return awsbase.Config{
				Region:      "us-east-1",
				Credentials: awsbase.NewCredentialsCache(failingCredentialsProvider{}),
			}, nil
		},
		newSTSClient: func(cfg awsbase.Config) stsClient {
			t.Fatal("STS client should not be called when credentials cannot be retrieved")
			return nil
		},
	}

	_, err := p.AuthCheck(context.Background())
	if err == nil {
		t.Fatal("AuthCheck() error = nil")
	}
	authErr := mustAuthError(t, err)
	if authErr.Kind != "no_credentials" {
		t.Fatalf("AuthError.Kind = %q, want no_credentials", authErr.Kind)
	}
}

func TestAuthCheckReturnsPermissionDenied(t *testing.T) {
	p := &Provider{
		Config: Config{Profile: "test-profile"},
		loadDefaultConfig: func(ctx context.Context, optFns ...func(*awsconfig.LoadOptions) error) (awsbase.Config, error) {
			return awsbase.Config{
				Region:      "us-east-1",
				Credentials: awsbase.NewCredentialsCache(staticCredentialsProvider{}),
			}, nil
		},
		newSTSClient: func(cfg awsbase.Config) stsClient {
			return failingSTSClient{err: accessDeniedError{}}
		},
	}

	_, err := p.AuthCheck(context.Background())
	if err == nil {
		t.Fatal("AuthCheck() error = nil")
	}
	authErr := mustAuthError(t, err)
	if authErr.Kind != "permission_denied" {
		t.Fatalf("AuthError.Kind = %q, want permission_denied", authErr.Kind)
	}
}

func TestAuthCheckReturnsCallerIdentity(t *testing.T) {
	p := &Provider{
		Config: Config{Profile: "test-profile"},
		loadDefaultConfig: func(ctx context.Context, optFns ...func(*awsconfig.LoadOptions) error) (awsbase.Config, error) {
			return awsbase.Config{
				Region:      "us-east-1",
				Credentials: awsbase.NewCredentialsCache(staticCredentialsProvider{}),
			}, nil
		},
		newSTSClient: func(cfg awsbase.Config) stsClient {
			return failingSTSClient{
				out: &sts.GetCallerIdentityOutput{
					Account: awsbase.String("123456789012"),
					Arn:     awsbase.String("arn:aws:sts::123456789012:assumed-role/test-role/test-session"),
					UserId:  awsbase.String("test-session"),
				},
			}
		},
	}

	status, err := p.AuthCheck(context.Background())
	if err != nil {
		t.Fatalf("AuthCheck() error = %v", err)
	}
	if status.Profile != "test-profile" || status.Account != "123456789012" || status.Arn == "" {
		t.Fatalf("AuthCheck() status = %#v", status)
	}
}

func TestListBaseImagesResolvesRegionSpecificAMI(t *testing.T) {
	p := &Provider{
		Config: Config{Profile: "test-profile"},
		loadDefaultConfig: func(ctx context.Context, optFns ...func(*awsconfig.LoadOptions) error) (awsbase.Config, error) {
			return awsbase.Config{Region: "us-east-1"}, nil
		},
		newSSMClient: func(cfg awsbase.Config) ssmClient {
			if cfg.Region != "ap-northeast-1" {
				t.Fatalf("cfg.Region = %q, want ap-northeast-1", cfg.Region)
			}
			return fakeSSMClient{
				value: "ami-0123456789abcdef0",
			}
		},
	}

	images, err := p.ListBaseImages(context.Background(), "ap-northeast-1")
	if err != nil {
		t.Fatalf("ListBaseImages() error = %v", err)
	}
	if len(images) != 1 {
		t.Fatalf("ListBaseImages() len = %d, want 1", len(images))
	}
	got := images[0]
	if got.Name != "AWS Deep Learning AMI GPU Ubuntu 22.04" {
		t.Fatalf("image name = %q", got.Name)
	}
	if got.ID != "ami-0123456789abcdef0" {
		t.Fatalf("image ID = %q", got.ID)
	}
	if got.Region != "ap-northeast-1" || got.SSMParameter == "" || got.Source != "aws-ssm-public-parameter" {
		t.Fatalf("image metadata = %#v", got)
	}
}

func TestListBaseImagesResolvesUbuntuImageForCPUComputeClass(t *testing.T) {
	p := &Provider{
		Config: Config{Profile: "test-profile", ComputeClass: config.ComputeClassCPU},
		loadDefaultConfig: func(ctx context.Context, optFns ...func(*awsconfig.LoadOptions) error) (awsbase.Config, error) {
			return awsbase.Config{Region: "us-east-1"}, nil
		},
		newSSMClient: func(cfg awsbase.Config) ssmClient {
			if cfg.Region != "ap-northeast-1" {
				t.Fatalf("cfg.Region = %q, want ap-northeast-1", cfg.Region)
			}
			return fakeSSMClient{
				value: "ami-0ubuntu1234567890",
			}
		},
	}

	images, err := p.ListBaseImages(context.Background(), "ap-northeast-1")
	if err != nil {
		t.Fatalf("ListBaseImages() error = %v", err)
	}
	if len(images) != 1 {
		t.Fatalf("ListBaseImages() len = %d, want 1", len(images))
	}
	got := images[0]
	if got.Name != "Ubuntu 22.04 LTS" {
		t.Fatalf("image name = %q", got.Name)
	}
	if got.ID != "ami-0ubuntu1234567890" {
		t.Fatalf("image ID = %q", got.ID)
	}
	if got.Region != "ap-northeast-1" || got.SSMParameter == "" || got.Source != "canonical-ssm-public-parameter" {
		t.Fatalf("image metadata = %#v", got)
	}
}

func TestCheckGPUQuotaUsesServiceQuotasUtilizationReport(t *testing.T) {
	p := &Provider{
		Config: Config{Profile: "test-profile"},
		loadDefaultConfig: func(ctx context.Context, optFns ...func(*awsconfig.LoadOptions) error) (awsbase.Config, error) {
			return awsbase.Config{
				Region:      "us-east-1",
				Credentials: awsbase.NewCredentialsCache(staticCredentialsProvider{}),
			}, nil
		},
		newSQClient: func(cfg awsbase.Config) serviceQuotasClient {
			if cfg.Region != "ap-northeast-1" {
				t.Fatalf("cfg.Region = %q, want ap-northeast-1", cfg.Region)
			}
			return fakeServiceQuotasClient{
				startOut: &servicequotas.StartQuotaUtilizationReportOutput{
					ReportId: awsbase.String("report-123"),
					Status:   sqtypes.ReportStatusPending,
				},
				getOut: &servicequotas.GetQuotaUtilizationReportOutput{
					Status: sqtypes.ReportStatusCompleted,
					Quotas: []sqtypes.QuotaUtilizationInfo{
						{
							ServiceCode:  awsbase.String("ec2"),
							QuotaName:    awsbase.String("Running On-Demand G and VT instances"),
							AppliedValue: awsbase.Float64(2),
							Utilization:  awsbase.Float64(50),
						},
						{
							ServiceCode:  awsbase.String("ec2"),
							QuotaName:    awsbase.String("All G and VT Spot Instance Requests"),
							AppliedValue: awsbase.Float64(8),
							Utilization:  awsbase.Float64(25),
						},
					},
				},
			}
		},
	}

	report, err := p.CheckGPUQuota(context.Background(), "ap-northeast-1", "g5")
	if err != nil {
		t.Fatalf("CheckGPUQuota() error = %v", err)
	}
	if report.Source != QuotaSourceServiceQuotas {
		t.Fatalf("report.Source = %q, want %q", report.Source, QuotaSourceServiceQuotas)
	}
	if !report.LikelyCreatable {
		t.Fatal("report.LikelyCreatable = false, want true")
	}
	if len(report.Checks) != 2 {
		t.Fatalf("len(report.Checks) = %d, want 2", len(report.Checks))
	}
	if report.Checks[0].CurrentLimit != 2 || report.Checks[0].EstimatedRemaining != 1 || report.Checks[0].UsageIsEstimated {
		t.Fatalf("first quota check = %#v", report.Checks[0])
	}
	if report.Checks[1].CurrentLimit != 8 || report.Checks[1].EstimatedRemaining != 6 || report.Checks[1].UsageIsEstimated {
		t.Fatalf("second quota check = %#v", report.Checks[1])
	}
}

func TestCreateInstanceUsesDefaultVpcSubnetAndReturnsMetadata(t *testing.T) {
	fakeEC2 := &fakeEC2Client{
		t: t,
	}
	p := &Provider{
		Config: Config{Profile: "test-profile"},
		loadDefaultConfig: func(ctx context.Context, optFns ...func(*awsconfig.LoadOptions) error) (awsbase.Config, error) {
			return awsbase.Config{
				Region:      "us-east-1",
				Credentials: awsbase.NewCredentialsCache(staticCredentialsProvider{}),
			}, nil
		},
		newEC2Client: func(cfg awsbase.Config) ec2Client {
			if cfg.Region != "us-east-1" {
				t.Fatalf("cfg.Region = %q, want us-east-1", cfg.Region)
			}
			return fakeEC2
		},
	}

	instance, err := p.CreateInstance(context.Background(), provider.CreateInstanceRequest{
		Region:           "us-east-1",
		InstanceType:     "g5.xlarge",
		Image:            "ami-0123456789abcdef0",
		ImageName:        "AWS Deep Learning AMI GPU Ubuntu 22.04",
		DiskSizeGB:       40,
		NetworkMode:      "private",
		ConnectionMethod: "ssh",
		SSHKeyName:       "demo-key",
		SSHCIDR:          "203.0.113.0/24",
	})
	if err != nil {
		t.Fatalf("CreateInstance() error = %v", err)
	}
	if instance.ID != "i-0123456789abcdef0" {
		t.Fatalf("instance.ID = %q", instance.ID)
	}
	if instance.Region != "us-east-1" {
		t.Fatalf("instance.Region = %q", instance.Region)
	}
	if instance.PublicIP != "" || instance.PrivateIP != "10.0.0.10" {
		t.Fatalf("instance IPs = %#v", instance)
	}
	if instance.SecurityGroupID != "sg-0123456789abcdef0" {
		t.Fatalf("instance.SecurityGroupID = %q", instance.SecurityGroupID)
	}
	if len(instance.SecurityGroupRules) != 1 || instance.SecurityGroupRules[0] != "allow tcp/22 from 203.0.113.0/24" {
		t.Fatalf("instance.SecurityGroupRules = %#v", instance.SecurityGroupRules)
	}
	if instance.ConnectionInfo != "ssh -i <your-key>.pem ubuntu@10.0.0.10" {
		t.Fatalf("instance.ConnectionInfo = %q", instance.ConnectionInfo)
	}
	if fakeEC2.createSecurityGroupVpcID != "vpc-123" {
		t.Fatalf("createSecurityGroupVpcID = %q", fakeEC2.createSecurityGroupVpcID)
	}
	if fakeEC2.authorizedCIDR != "203.0.113.0/24" {
		t.Fatalf("authorizedCIDR = %q", fakeEC2.authorizedCIDR)
	}
	if fakeEC2.runImageID != "ami-0123456789abcdef0" || fakeEC2.runInstanceType != "g5.xlarge" {
		t.Fatalf("run launch params = image %q type %q", fakeEC2.runImageID, fakeEC2.runInstanceType)
	}
	if fakeEC2.associatePublicIP {
		t.Fatal("associatePublicIP = true, want false for private networking")
	}
}

func TestCreateInstanceOpensSSHIngressWithCIDROnly(t *testing.T) {
	fakeEC2 := &fakeEC2Client{
		t: t,
	}
	p := &Provider{
		Config: Config{Profile: "test-profile"},
		loadDefaultConfig: func(ctx context.Context, optFns ...func(*awsconfig.LoadOptions) error) (awsbase.Config, error) {
			return awsbase.Config{
				Region:      "us-east-1",
				Credentials: awsbase.NewCredentialsCache(staticCredentialsProvider{}),
			}, nil
		},
		newEC2Client: func(cfg awsbase.Config) ec2Client {
			return fakeEC2
		},
	}

	_, err := p.CreateInstance(context.Background(), provider.CreateInstanceRequest{
		Region:           "us-east-1",
		InstanceType:     "t3.xlarge",
		Image:            "ami-0123456789abcdef0",
		ImageName:        "Ubuntu 22.04 LTS",
		DiskSizeGB:       20,
		NetworkMode:      "public",
		ConnectionMethod: "ssh",
		SSHCIDR:          "203.0.113.0/24",
	})
	if err != nil {
		t.Fatalf("CreateInstance() error = %v", err)
	}
	if fakeEC2.authorizedCIDR != "203.0.113.0/24" {
		t.Fatalf("authorizedCIDR = %q, want 203.0.113.0/24", fakeEC2.authorizedCIDR)
	}
}

func mustAuthError(t *testing.T, err error) *AuthError {
	t.Helper()
	var authErr *AuthError
	if !errors.As(err, &authErr) {
		t.Fatalf("error %v is not *AuthError", err)
	}
	return authErr
}

type failingCredentialsProvider struct{}

func (failingCredentialsProvider) Retrieve(ctx context.Context) (awsbase.Credentials, error) {
	return awsbase.Credentials{}, errors.New("no valid credential sources")
}

type staticCredentialsProvider struct{}

func (staticCredentialsProvider) Retrieve(ctx context.Context) (awsbase.Credentials, error) {
	return awsbase.Credentials{
		AccessKeyID:     "AKIA1234567890TEST",
		SecretAccessKey: "secret",
		SessionToken:    "token",
		Source:          "test",
	}, nil
}

type failingSTSClient struct {
	out *sts.GetCallerIdentityOutput
	err error
}

func (f failingSTSClient) GetCallerIdentity(ctx context.Context, params *sts.GetCallerIdentityInput, optFns ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.out, nil
}

type accessDeniedError struct{}

func (accessDeniedError) Error() string {
	return "AccessDenied: denied"
}

func (accessDeniedError) ErrorCode() string {
	return "AccessDenied"
}

func (accessDeniedError) ErrorMessage() string {
	return "denied"
}

func (accessDeniedError) ErrorFault() smithy.ErrorFault {
	return smithy.FaultClient
}

type fakeSSMClient struct {
	value string
	err   error
}

func (f fakeSSMClient) GetParameter(ctx context.Context, params *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &ssm.GetParameterOutput{
		Parameter: &ssmtypes.Parameter{
			Value: awsbase.String(f.value),
		},
	}, nil
}

type fakeServiceQuotasClient struct {
	startOut *servicequotas.StartQuotaUtilizationReportOutput
	getOut   *servicequotas.GetQuotaUtilizationReportOutput
}

func (f fakeServiceQuotasClient) StartQuotaUtilizationReport(ctx context.Context, params *servicequotas.StartQuotaUtilizationReportInput, optFns ...func(*servicequotas.Options)) (*servicequotas.StartQuotaUtilizationReportOutput, error) {
	return f.startOut, nil
}

func (f fakeServiceQuotasClient) GetQuotaUtilizationReport(ctx context.Context, params *servicequotas.GetQuotaUtilizationReportInput, optFns ...func(*servicequotas.Options)) (*servicequotas.GetQuotaUtilizationReportOutput, error) {
	return f.getOut, nil
}

type fakeEC2Client struct {
	t *testing.T

	createSecurityGroupVpcID string
	authorizedCIDR           string
	runImageID               string
	runInstanceType          string
	associatePublicIP        bool
}

func (f *fakeEC2Client) CreateSecurityGroup(ctx context.Context, params *ec2.CreateSecurityGroupInput, optFns ...func(*ec2.Options)) (*ec2.CreateSecurityGroupOutput, error) {
	if params == nil || params.VpcId == nil || *params.VpcId != "vpc-123" {
		f.t.Fatalf("CreateSecurityGroup VpcId = %#v, want vpc-123", params)
	}
	f.createSecurityGroupVpcID = awsbase.ToString(params.VpcId)
	return &ec2.CreateSecurityGroupOutput{GroupId: awsbase.String("sg-0123456789abcdef0")}, nil
}

func (f *fakeEC2Client) AuthorizeSecurityGroupIngress(ctx context.Context, params *ec2.AuthorizeSecurityGroupIngressInput, optFns ...func(*ec2.Options)) (*ec2.AuthorizeSecurityGroupIngressOutput, error) {
	if params == nil || params.GroupId == nil || *params.GroupId != "sg-0123456789abcdef0" {
		f.t.Fatalf("AuthorizeSecurityGroupIngress GroupId = %#v, want sg-0123456789abcdef0", params)
	}
	if len(params.IpPermissions) != 1 || len(params.IpPermissions[0].IpRanges) != 1 {
		f.t.Fatalf("AuthorizeSecurityGroupIngress IpPermissions = %#v", params.IpPermissions)
	}
	f.authorizedCIDR = awsbase.ToString(params.IpPermissions[0].IpRanges[0].CidrIp)
	return &ec2.AuthorizeSecurityGroupIngressOutput{}, nil
}

func (f *fakeEC2Client) DeleteSecurityGroup(ctx context.Context, params *ec2.DeleteSecurityGroupInput, optFns ...func(*ec2.Options)) (*ec2.DeleteSecurityGroupOutput, error) {
	return &ec2.DeleteSecurityGroupOutput{}, nil
}

func (f *fakeEC2Client) DescribeVpcs(ctx context.Context, params *ec2.DescribeVpcsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeVpcsOutput, error) {
	return &ec2.DescribeVpcsOutput{
		Vpcs: []ec2types.Vpc{{VpcId: awsbase.String("vpc-123")}},
	}, nil
}

func (f *fakeEC2Client) DescribeSubnets(ctx context.Context, params *ec2.DescribeSubnetsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSubnetsOutput, error) {
	return &ec2.DescribeSubnetsOutput{
		Subnets: []ec2types.Subnet{{SubnetId: awsbase.String("subnet-123")}},
	}, nil
}

func (f *fakeEC2Client) RunInstances(ctx context.Context, params *ec2.RunInstancesInput, optFns ...func(*ec2.Options)) (*ec2.RunInstancesOutput, error) {
	if params == nil {
		f.t.Fatal("RunInstances params = nil")
	}
	f.runImageID = awsbase.ToString(params.ImageId)
	f.runInstanceType = string(params.InstanceType)
	if len(params.NetworkInterfaces) != 1 {
		f.t.Fatalf("RunInstances NetworkInterfaces = %#v", params.NetworkInterfaces)
	}
	f.associatePublicIP = awsbase.ToBool(params.NetworkInterfaces[0].AssociatePublicIpAddress)
	return &ec2.RunInstancesOutput{
		Instances: []ec2types.Instance{{
			InstanceId: awsbase.String("i-0123456789abcdef0"),
			State:      &ec2types.InstanceState{Name: ec2types.InstanceStateNameRunning},
		}},
	}, nil
}

func (f *fakeEC2Client) DescribeInstances(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	publicIP := awsbase.String("")
	if f.associatePublicIP {
		publicIP = awsbase.String("203.0.113.10")
	}
	return &ec2.DescribeInstancesOutput{
		Reservations: []ec2types.Reservation{{
			Instances: []ec2types.Instance{{
				InstanceId:       awsbase.String("i-0123456789abcdef0"),
				PublicIpAddress:  publicIP,
				PrivateIpAddress: awsbase.String("10.0.0.10"),
				State:            &ec2types.InstanceState{Name: ec2types.InstanceStateNameRunning},
			}},
		}},
	}, nil
}
