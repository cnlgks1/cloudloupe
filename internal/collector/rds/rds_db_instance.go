package rds

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsrds "github.com/aws/aws-sdk-go-v2/service/rds"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"

	"github.com/cnlgks1/cloudloupe/internal/collect"
	"github.com/cnlgks1/cloudloupe/internal/model"
)

// describeDBInstancesAPI는 DB 인스턴스 수집에 필요한 SDK 메서드만 담는다.
type describeDBInstancesAPI interface {
	DescribeDBInstances(context.Context, *awsrds.DescribeDBInstancesInput, ...func(*awsrds.Options)) (*awsrds.DescribeDBInstancesOutput, error)
}

// dbInstanceCollector는 RDS DB 인스턴스를 조회한다.
type dbInstanceCollector struct {
	api describeDBInstancesAPI
}

// NewDBInstance는 RDS DB 인스턴스 수집기를 만든다.
func NewDBInstance(api describeDBInstancesAPI) collect.Collector {
	return dbInstanceCollector{api: api}
}

// Type은 이 수집기가 만드는 리소스 타입 ID를 반환한다.
func (c dbInstanceCollector) Type() string { return model.TypeRDSDBInstance }

// Collect는 범위 안의 RDS DB 인스턴스를 모두 조회해 도메인 리소스로 변환한다.
//
// SDK paginator가 중간 페이지에서 실패하면 앞 페이지에서 변환한 리소스는 보존한다.
func (c dbInstanceCollector) Collect(ctx context.Context, req collect.Request) ([]model.Resource, error) {
	paginator := awsrds.NewDescribeDBInstancesPaginator(c.api, &awsrds.DescribeDBInstancesInput{})

	var resources []model.Resource
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return resources, fmt.Errorf("describe DB instances: %w", err)
		}

		for i := range page.DBInstances {
			resources = append(resources, dbInstanceToResource(req.Scope, page.DBInstances[i]))
		}
	}

	return resources, nil
}

// dbInstanceToResource는 SDK DB 인스턴스의 포인터 필드를 도메인 값으로 변환한다.
func dbInstanceToResource(scope collect.Scope, instance rdstypes.DBInstance) model.Resource {
	identifier := aws.ToString(instance.DBInstanceIdentifier)
	endpointAddress := ""
	if instance.Endpoint != nil {
		endpointAddress = aws.ToString(instance.Endpoint.Address)
	}

	resource := model.Resource{
		Type:      model.TypeRDSDBInstance,
		ID:        identifier,
		Name:      identifier,
		ARN:       aws.ToString(instance.DBInstanceArn),
		Region:    scope.Region,
		Profile:   scope.Profile,
		AccountID: scope.AccountID,
		Status:    aws.ToString(instance.DBInstanceStatus),
		CreatedAt: utcTime(instance.InstanceCreateTime),
		Fields: []model.Field{
			{Key: "DBInstanceClass", Value: orDash(aws.ToString(instance.DBInstanceClass))},
			{Key: "Engine", Value: orDash(aws.ToString(instance.Engine))},
			{Key: "EngineVersion", Value: orDash(aws.ToString(instance.EngineVersion))},
			{Key: "DBName", Value: orDash(aws.ToString(instance.DBName))},
			{Key: "AvailabilityZone", Value: orDash(aws.ToString(instance.AvailabilityZone))},
			{Key: "SecondaryAvailabilityZone", Value: orDash(aws.ToString(instance.SecondaryAvailabilityZone))},
			{Key: "MultiAZ", Value: boolValue(instance.MultiAZ)},
			{Key: "Endpoint", Value: endpointValue(instance.Endpoint)},
			{Key: "AllocatedStorage", Value: int32Value(instance.AllocatedStorage)},
			{Key: "MaxAllocatedStorage", Value: int32Value(instance.MaxAllocatedStorage)},
			{Key: "StorageType", Value: orDash(aws.ToString(instance.StorageType))},
			{Key: "StorageEncrypted", Value: boolValue(instance.StorageEncrypted)},
			{Key: "KmsKeyId", Value: orDash(aws.ToString(instance.KmsKeyId))},
			{Key: "Iops", Value: int32Value(instance.Iops)},
			{Key: "StorageThroughput", Value: int32Value(instance.StorageThroughput)},
			{Key: "PubliclyAccessible", Value: boolValue(instance.PubliclyAccessible)},
			{Key: "DBClusterIdentifier", Value: orDash(aws.ToString(instance.DBClusterIdentifier))},
			{Key: "DBSubnetGroup", Value: dbSubnetGroupName(instance.DBSubnetGroup)},
			{Key: "BackupRetentionPeriod", Value: int32Value(instance.BackupRetentionPeriod)},
			{Key: "AutoMinorVersionUpgrade", Value: boolValue(instance.AutoMinorVersionUpgrade)},
			{Key: "PerformanceInsightsEnabled", Value: boolValue(instance.PerformanceInsightsEnabled)},
			{Key: "DeletionProtection", Value: boolValue(instance.DeletionProtection)},
		},
		Tags:        rdsTags(instance.TagList),
		Identifiers: dnsIdentifiers(endpointAddress),
	}

	resource.Related = appendIDRelation(
		resource.Related,
		model.TypeRDSDBCluster,
		"DBClusterIdentifier",
		aws.ToString(instance.DBClusterIdentifier),
	)
	resource.Related = appendDBSubnetGroupRelations(resource.Related, instance.DBSubnetGroup)
	for _, group := range instance.VpcSecurityGroups {
		resource.Related = appendIDRelation(
			resource.Related,
			model.TypeEC2SecurityGroup,
			"VpcSecurityGroups.VpcSecurityGroupId",
			aws.ToString(group.VpcSecurityGroupId),
		)
	}
	resource.Related = appendKMSRelation(resource.Related, aws.ToString(instance.KmsKeyId))

	return resource
}

// dbSubnetGroupName은 SDK 서브넷 그룹 포인터에서 표시 이름을 값으로 꺼낸다.
func dbSubnetGroupName(group *rdstypes.DBSubnetGroup) string {
	if group == nil {
		return "-"
	}

	return orDash(aws.ToString(group.DBSubnetGroupName))
}

// appendDBSubnetGroupRelations는 서브넷 그룹에 포함된 VPC와 서브넷 관계를 추가한다.
func appendDBSubnetGroupRelations(refs []model.Ref, group *rdstypes.DBSubnetGroup) []model.Ref {
	if group == nil {
		return refs
	}

	refs = appendIDRelation(refs, model.TypeEC2VPC, "DBSubnetGroup.VpcId", aws.ToString(group.VpcId))
	for _, subnet := range group.Subnets {
		refs = appendIDRelation(refs, model.TypeEC2Subnet,
			"DBSubnetGroup.Subnets.SubnetIdentifier", aws.ToString(subnet.SubnetIdentifier))
	}

	return refs
}
