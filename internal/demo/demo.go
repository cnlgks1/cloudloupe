// Package demo는 AWS 없이 TUI를 체험하고 스크린샷을 찍을 수 있는 가짜 데이터를 제공한다.
//
// cloudloupe --demo로 진입하며, 실제 AWS를 전혀 호출하지 않는다. 조회 전용 도구라 화면이
// 실제 인프라 정보로 채워지므로, 스크린샷에 실제 계정·ARN·리소스가 노출되는 것을 막기 위해
// 완전히 가짜인 데이터를 쓴다. 계정 ID는 AWS 문서의 예제 계정 123456789012이고, 리소스 이름과
// IP는 모두 지어낸 값이다.
//
// 이 패키지는 tui.Deps에 주입할 함수들(프로필·신원·수집)을 만들어 반환할 뿐이며, tui나 collect
// 로직을 바꾸지 않는다. 실제 조회 경로와 같은 화면 코드가 이 데이터로 렌더링된다.
//
// 각 리소스의 Fields 키는 그 타입 수집기가 만드는 키와 같아야 한다. 카탈로그의 목록 열과
// 어긋나면 데모 화면에서 열은 있는데 셀이 비므로, TestFieldsCoverCatalogColumns가 이를 막는다.
package demo

import (
	"context"
	"time"

	"github.com/cnlgks1/cloudloupe/internal/awsclient"
	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// AccountID는 데모가 쓰는 계정 ID다. AWS 문서의 예제 계정이라 실제 계정과 겹치지 않는다.
const AccountID = "123456789012"

// Profile은 데모가 노출하는 유일한 프로필 이름이다.
const Profile = "demo"

// Region은 데모 리소스가 속한 리전이다.
const Region = "ap-northeast-2"

// Profiles는 데모용 프로필 하나를 반환한다. 실제 파일을 읽지 않는다.
func Profiles() []awsclient.Profile {
	return []awsclient.Profile{
		{
			Name:   Profile,
			Region: Region,
			Kind:   awsclient.KindStatic,
			Source: awsclient.SourceConfig,
		},
	}
}

// Identity는 데모 계정의 신원 정보를 반환한다. STS를 호출하지 않고 예제 계정을 그대로 준다.
func Identity() awsclient.Identity {
	return awsclient.Identity{
		AccountID: AccountID,
		ARN:       "arn:aws:iam::" + AccountID + ":user/demo",
		UserID:    "AIDADEMODEMODEMODEMO",
	}
}

// Resources는 데모 리소스 스냅샷을 반환한다.
//
// 관계가 서로 이어지도록 ID·ARN을 맞춰 두었다. 상세 화면의 정방향·역방향 관계와 그래프가
// 실제처럼 보이게 하려는 것이다. 여러 서비스를 대표로 담되 화면이 복잡하지 않을 만큼만 둔다.
// 각 리소스의 Fields 키는 그 타입 수집기가 만드는 키와 같아야 한다(카탈로그 열과 일치).
func Resources() []model.Resource {
	created := time.Date(2025, 3, 14, 9, 26, 53, 0, time.UTC)

	vpc := "vpc-0a1b2c3d4e5f60718"
	subnetA := "subnet-0a1b2c3d4e5f60001"
	subnetB := "subnet-0a1b2c3d4e5f60002"
	sgWeb := "sg-0a1b2c3d4e5f60010"
	sgDB := "sg-0a1b2c3d4e5f60011"
	tgWeb := "arn:aws:elasticloadbalancing:" + Region + ":" + AccountID + ":targetgroup/web/0a1b2c3d4e5f6071"
	role := "arn:aws:iam::" + AccountID + ":role/ecsTaskExecutionRole"
	taskDef := "arn:aws:ecs:" + Region + ":" + AccountID + ":task-definition/web:7"
	cluster := "arn:aws:ecs:" + Region + ":" + AccountID + ":cluster/app"
	kmsKey := "arn:aws:kms:" + Region + ":" + AccountID + ":key/0a1b2c3d-4e5f-6071-8293-a4b5c6d7e8f9"

	ref := func(typ, id, relation string) model.Ref {
		return model.Ref{Type: typ, ID: id, Relation: relation}
	}
	arnRef := func(typ, arn, relation string) model.Ref {
		return model.Ref{Type: typ, ID: arn, IdentifierKind: model.IdentifierARN, Relation: relation}
	}
	field := func(k, v string) model.Field { return model.Field{Key: k, Value: v} }
	at := func(t time.Time) *time.Time { return &t }

	return []model.Resource{
		{
			Type: model.TypeEC2VPC, ID: vpc, Name: "app-vpc",
			Region: Region, Profile: Profile, AccountID: AccountID, Status: "available",
			Fields: []model.Field{
				field("CidrBlock", "10.0.0.0/16"), field("IsDefault", "false"),
				field("InstanceTenancy", "default"), field("DhcpOptionsId", "dopt-0a1b2c3d4e5f60000"),
				field("OwnerId", AccountID),
			},
		},
		{
			Type: model.TypeEC2Subnet, ID: subnetA, Name: "app-subnet-a",
			Region: Region, Profile: Profile, AccountID: AccountID, Status: "available",
			Fields: []model.Field{
				field("CidrBlock", "10.0.1.0/24"), field("AvailabilityZone", Region+"a"),
				field("AvailabilityZoneId", "apne2-az1"), field("AvailableIpAddressCount", "251"),
				field("VpcId", vpc), field("MapPublicIpOnLaunch", "false"), field("DefaultForAz", "false"),
				field("Ipv6Native", "false"), field("AssignIpv6AddressOnCreation", "false"), field("OwnerId", AccountID),
			},
			Related: []model.Ref{ref(model.TypeEC2VPC, vpc, "VpcId")},
		},
		{
			Type: model.TypeEC2Subnet, ID: subnetB, Name: "app-subnet-c",
			Region: Region, Profile: Profile, AccountID: AccountID, Status: "available",
			Fields: []model.Field{
				field("CidrBlock", "10.0.2.0/24"), field("AvailabilityZone", Region+"c"),
				field("AvailabilityZoneId", "apne2-az3"), field("AvailableIpAddressCount", "249"),
				field("VpcId", vpc), field("MapPublicIpOnLaunch", "false"), field("DefaultForAz", "false"),
				field("Ipv6Native", "false"), field("AssignIpv6AddressOnCreation", "false"), field("OwnerId", AccountID),
			},
			Related: []model.Ref{ref(model.TypeEC2VPC, vpc, "VpcId")},
		},
		{
			Type: model.TypeEC2SecurityGroup, ID: sgWeb, Name: "web-sg",
			Region: Region, Profile: Profile, AccountID: AccountID,
			Fields: []model.Field{
				field("VpcId", vpc), field("InboundRules", "tcp/443 0.0.0.0/0, tcp/80 0.0.0.0/0"),
				field("OutboundRules", "-1 0.0.0.0/0"), field("Description", "web tier"), field("OwnerId", AccountID),
			},
			Related: []model.Ref{ref(model.TypeEC2VPC, vpc, "VpcId")},
		},
		{
			Type: model.TypeEC2SecurityGroup, ID: sgDB, Name: "db-sg",
			Region: Region, Profile: Profile, AccountID: AccountID,
			Fields: []model.Field{
				field("VpcId", vpc), field("InboundRules", "tcp/5432 "+sgWeb),
				field("OutboundRules", "-1 0.0.0.0/0"), field("Description", "database tier"), field("OwnerId", AccountID),
			},
			Related: []model.Ref{ref(model.TypeEC2VPC, vpc, "VpcId")},
		},
		{
			Type: model.TypeEC2Instance, ID: "i-0a1b2c3d4e5f60021", Name: "web-01",
			Region: Region, Profile: Profile, AccountID: AccountID, Status: "running", CreatedAt: at(created),
			Fields: []model.Field{
				field("InstanceType", "t3.medium"), field("AvailabilityZone", Region+"a"),
				field("PrivateIpAddress", "10.0.1.23"), field("PublicIp", "-"),
			},
			Related: []model.Ref{
				ref(model.TypeEC2Subnet, subnetA, "SubnetId"),
				ref(model.TypeEC2VPC, vpc, "VpcId"),
				ref(model.TypeEC2SecurityGroup, sgWeb, "SecurityGroups.GroupId"),
			},
		},
		{
			Type: model.TypeEC2Instance, ID: "i-0a1b2c3d4e5f60022", Name: "web-02",
			Region: Region, Profile: Profile, AccountID: AccountID, Status: "running", CreatedAt: at(created),
			Fields: []model.Field{
				field("InstanceType", "t3.medium"), field("AvailabilityZone", Region+"c"),
				field("PrivateIpAddress", "10.0.2.31"), field("PublicIp", "-"),
			},
			Related: []model.Ref{
				ref(model.TypeEC2Subnet, subnetB, "SubnetId"),
				ref(model.TypeEC2VPC, vpc, "VpcId"),
				ref(model.TypeEC2SecurityGroup, sgWeb, "SecurityGroups.GroupId"),
			},
		},
		{
			Type: model.TypeEC2Volume, ID: "vol-0a1b2c3d4e5f60031", Name: "web-01-root",
			Region: Region, Profile: Profile, AccountID: AccountID, Status: "in-use",
			Fields: []model.Field{
				field("VolumeType", "gp3"), field("Size", "30"), field("Iops", "3000"),
				field("AvailabilityZone", Region+"a"), field("Encrypted", "true"),
			},
			Related: []model.Ref{
				ref(model.TypeEC2Instance, "i-0a1b2c3d4e5f60021", "Attachments.InstanceId"),
				arnRef(model.TypeKMSKey, kmsKey, "KmsKeyId"),
			},
		},
		{
			Type: model.TypeELBv2LoadBalancer, ID: "app-alb", Name: "app-alb",
			ARN:    "arn:aws:elasticloadbalancing:" + Region + ":" + AccountID + ":loadbalancer/app/app-alb/0a1b2c3d4e5f6071",
			Region: Region, Profile: Profile, AccountID: AccountID, Status: "active", CreatedAt: at(created),
			Fields: []model.Field{
				field("Type", "application"), field("Scheme", "internet-facing"),
				field("DNSName", "app-alb-123.ap-northeast-2.elb.amazonaws.com"), field("VpcId", vpc),
			},
			Related: []model.Ref{
				ref(model.TypeEC2VPC, vpc, "VpcId"),
				ref(model.TypeEC2SecurityGroup, sgWeb, "SecurityGroups"),
			},
		},
		{
			Type: model.TypeELBv2TargetGroup, ID: "web", Name: "web",
			ARN:    tgWeb,
			Region: Region, Profile: Profile, AccountID: AccountID,
			Fields: []model.Field{
				field("Protocol", "HTTP"), field("Port", "80"), field("TargetType", "instance"), field("Targets", "2"),
			},
			Related: []model.Ref{
				ref(model.TypeEC2Instance, "i-0a1b2c3d4e5f60021", "Targets.Id"),
				ref(model.TypeEC2Instance, "i-0a1b2c3d4e5f60022", "Targets.Id"),
			},
		},
		{
			Type: model.TypeECSCluster, ID: "app", Name: "app",
			ARN:    cluster,
			Region: Region, Profile: Profile, AccountID: AccountID, Status: "ACTIVE",
			Fields: []model.Field{
				field("Status", "ACTIVE"), field("RunningTasksCount", "2"), field("ActiveServicesCount", "1"),
				field("RegisteredContainerInstancesCount", "0"), field("CapacityProviders", "FARGATE, FARGATE_SPOT"),
			},
		},
		{
			Type: model.TypeECSService, ID: "web", Name: "web",
			ARN:    "arn:aws:ecs:" + Region + ":" + AccountID + ":service/app/web",
			Region: Region, Profile: Profile, AccountID: AccountID, Status: "ACTIVE",
			Fields: []model.Field{
				field("Status", "ACTIVE"), field("LaunchType", "FARGATE"), field("DesiredCount", "2"),
				field("RunningCount", "2"), field("PendingCount", "0"), field("TaskDefinition", taskDef),
				field("PlatformVersion", "LATEST"), field("SchedulingStrategy", "REPLICA"),
			},
			Related: []model.Ref{
				arnRef(model.TypeECSCluster, cluster, "ClusterArn"),
				arnRef(model.TypeECSTaskDefinition, taskDef, "TaskDefinition"),
				ref(model.TypeEC2Subnet, subnetA, "NetworkConfiguration.AwsvpcConfiguration.Subnets"),
				ref(model.TypeEC2Subnet, subnetB, "NetworkConfiguration.AwsvpcConfiguration.Subnets"),
				ref(model.TypeEC2SecurityGroup, sgWeb, "NetworkConfiguration.AwsvpcConfiguration.SecurityGroups"),
				arnRef(model.TypeELBv2TargetGroup, tgWeb, "LoadBalancers.TargetGroupArn"),
			},
		},
		{
			Type: model.TypeECSTaskDefinition, ID: "web:7", Name: "web:7",
			ARN:    taskDef,
			Region: Region, Profile: Profile, AccountID: AccountID, Status: "ACTIVE",
			Fields: []model.Field{
				field("Family", "web"), field("Revision", "7"), field("Status", "ACTIVE"),
				field("Cpu", "512"), field("Memory", "1024"), field("NetworkMode", "awsvpc"),
				field("RequiresCompatibilities", "FARGATE"), field("ExecutionRoleArn", role), field("TaskRoleArn", "-"),
			},
			Related: []model.Ref{arnRef(model.TypeIAMRole, role, "ExecutionRoleArn")},
		},
		{
			Type: model.TypeRDSDBInstance, ID: "app-db", Name: "app-db",
			ARN:    "arn:aws:rds:" + Region + ":" + AccountID + ":db:app-db",
			Region: Region, Profile: Profile, AccountID: AccountID, Status: "available", CreatedAt: at(created),
			Fields: []model.Field{
				field("DBInstanceClass", "db.t3.medium"), field("Engine", "postgres"),
				field("AvailabilityZone", Region+"a"), field("MultiAZ", "true"),
				field("Endpoint", "app-db.abcdef.ap-northeast-2.rds.amazonaws.com:5432"),
				field("StorageType", "gp3"), field("StorageEncrypted", "true"),
			},
			Related: []model.Ref{
				ref(model.TypeEC2SecurityGroup, sgDB, "VpcSecurityGroups.VpcSecurityGroupId"),
				arnRef(model.TypeKMSKey, kmsKey, "KmsKeyId"),
			},
		},
		{
			Type: model.TypeDynamoDBTable, ID: "sessions", Name: "sessions",
			ARN:    "arn:aws:dynamodb:" + Region + ":" + AccountID + ":table/sessions",
			Region: Region, Profile: Profile, AccountID: AccountID, Status: "ACTIVE", CreatedAt: at(created),
			Fields: []model.Field{
				field("TableStatus", "ACTIVE"), field("KeySchema", "pk(HASH)"), field("BillingMode", "PAY_PER_REQUEST"),
				field("ItemCount", "10432"), field("TableSizeBytes", "2097152"),
				field("ReadCapacityUnits", "-"), field("WriteCapacityUnits", "-"),
				field("GlobalSecondaryIndexes", "-"), field("SSEType", "-"),
			},
		},
		{
			Type: model.TypeIAMRole, ID: "ecsTaskExecutionRole", Name: "ecsTaskExecutionRole",
			ARN:    role,
			Region: model.RegionGlobal, Profile: Profile, AccountID: AccountID, CreatedAt: at(created),
			Fields: []model.Field{
				field("Path", "/"), field("Description", "ECS task execution"),
				field("MaxSessionDuration", "3600"), field("PermissionsBoundary", "-"),
				field("RoleLastUsed", "-"), field("RoleId", "AROADEMODEMODEMODEMO"),
			},
		},
		{
			Type: model.TypeKMSKey, ID: "0a1b2c3d-4e5f-6071-8293-a4b5c6d7e8f9", Name: "alias/app-data",
			ARN:    kmsKey,
			Region: Region, Profile: Profile, AccountID: AccountID, Status: "Enabled", CreatedAt: at(created),
			Fields: []model.Field{
				field("Aliases", "alias/app-data"), field("KeyManager", "CUSTOMER"),
				field("KeyUsage", "ENCRYPT_DECRYPT"), field("KeySpec", "SYMMETRIC_DEFAULT"),
				field("Origin", "AWS_KMS"), field("MultiRegion", "false"),
				field("Enabled", "true"), field("DeletionDate", "-"), field("Description", "애플리케이션 데이터"),
			},
		},
		{
			Type: model.TypeS3Bucket, ID: "app-assets-123456789012", Name: "app-assets-123456789012",
			Region: model.RegionGlobal, Profile: Profile, AccountID: AccountID, CreatedAt: at(created),
			Fields: []model.Field{field("BucketRegion", Region)},
		},
	}
}

// Deps는 tui.Deps에 넣을 함수 묶음을 만든다.
//
// 반환하는 각 함수는 인자를 무시하고 항상 데모 데이터를 준다. AWS·파일·네트워크에 접근하지
// 않는다.
type Deps struct {
	LoadProfiles func(awsclient.Override) ([]awsclient.Profile, awsclient.Locations, error)
	Identify     func(ctx context.Context, profile, region string, locations awsclient.Locations) (awsclient.Identity, error)
	Collect      func(ctx context.Context, profile string, regions, types []string, locations awsclient.Locations) collect.Result
}

// NewDeps는 데모 주입 함수들을 만든다.
func NewDeps() Deps {
	return Deps{
		LoadProfiles: func(awsclient.Override) ([]awsclient.Profile, awsclient.Locations, error) {
			return Profiles(), awsclient.Locations{}, nil
		},
		Identify: func(context.Context, string, string, awsclient.Locations) (awsclient.Identity, error) {
			return Identity(), nil
		},
		Collect: func(_ context.Context, _ string, _, types []string, _ awsclient.Locations) collect.Result {
			return collect.Result{Resources: filterByTypes(Resources(), types)}
		},
	}
}

// filterByTypes는 선택한 타입만 남긴다. types가 비면 전부 반환한다.
//
// 실제 Collect는 선택 타입만 조회하므로, 데모도 같은 동작을 흉내 내 사용자가 일부만 골랐을 때
// 화면이 실제와 같게 좁혀지도록 한다.
func filterByTypes(resources []model.Resource, types []string) []model.Resource {
	if len(types) == 0 {
		return resources
	}

	want := make(map[string]struct{}, len(types))
	for _, t := range types {
		want[t] = struct{}{}
	}

	out := make([]model.Resource, 0, len(resources))
	for _, r := range resources {
		if _, ok := want[r.Type]; ok {
			out = append(out, r)
		}
	}

	return out
}
